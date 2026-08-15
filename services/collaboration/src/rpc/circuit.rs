use std::{
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};

use anyhow::anyhow;

use crate::error::{Result, ServiceError};

pub const DEFAULT_FAILURE_THRESHOLD: u32 = 5;
pub const DEFAULT_OPEN_DURATION: Duration = Duration::from_secs(5);
pub const DEFAULT_HALF_OPEN_PROBES: u32 = 1;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum State {
    Closed,
    HalfOpen,
    Open,
}

struct Inner {
    state: State,
    consecutive_failures: u32,
    opened_at: Option<Instant>,
    half_open_in_flight: u32,
}

/// Consecutive-failure breaker for outbound Knowledge RPC.
/// 用于出站 Knowledge RPC 的连续失败熔断器。
#[derive(Clone)]
pub struct Breaker {
    failure_threshold: u32,
    open_duration: Duration,
    half_open_probes: u32,
    now: Arc<dyn Fn() -> Instant + Send + Sync>,
    inner: Arc<Mutex<Inner>>,
}

impl Breaker {
    pub fn new() -> Self {
        Self::with_clock(Instant::now)
    }

    pub fn with_clock(now: impl Fn() -> Instant + Send + Sync + 'static) -> Self {
        Self {
            failure_threshold: DEFAULT_FAILURE_THRESHOLD,
            open_duration: DEFAULT_OPEN_DURATION,
            half_open_probes: DEFAULT_HALF_OPEN_PROBES,
            now: Arc::new(now),
            inner: Arc::new(Mutex::new(Inner {
                state: State::Closed,
                consecutive_failures: 0,
                opened_at: None,
                half_open_in_flight: 0,
            })),
        }
    }

    pub fn allow(&self) -> Result<()> {
        let mut inner = self.lock();
        let now = (self.now)();
        match inner.state {
            State::Open => {
                let opened_at = inner.opened_at.unwrap_or(now);
                if now.saturating_duration_since(opened_at) < self.open_duration {
                    return Err(open_error());
                }
                inner.state = State::HalfOpen;
                inner.half_open_in_flight = 1;
                Ok(())
            }
            State::HalfOpen => {
                if inner.half_open_in_flight >= self.half_open_probes {
                    return Err(open_error());
                }
                inner.half_open_in_flight += 1;
                Ok(())
            }
            State::Closed => Ok(()),
        }
    }

    pub fn success(&self) {
        let mut inner = self.lock();
        inner.consecutive_failures = 0;
        inner.half_open_in_flight = 0;
        inner.opened_at = None;
        inner.state = State::Closed;
    }

    pub fn failure(&self) {
        let mut inner = self.lock();
        if inner.state == State::HalfOpen {
            self.open_locked(&mut inner);
            return;
        }
        inner.consecutive_failures += 1;
        if inner.consecutive_failures >= self.failure_threshold {
            self.open_locked(&mut inner);
        }
    }

    #[cfg(test)]
    pub fn state(&self) -> State {
        let inner = self.lock();
        if inner.state == State::Open
            && let Some(opened_at) = inner.opened_at
            && (self.now)().saturating_duration_since(opened_at) >= self.open_duration
        {
            return State::HalfOpen;
        }
        inner.state
    }

    fn open_locked(&self, inner: &mut Inner) {
        inner.state = State::Open;
        inner.opened_at = Some((self.now)());
        inner.half_open_in_flight = 0;
        inner.consecutive_failures = self.failure_threshold;
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, Inner> {
        self.inner
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }
}

impl Default for Breaker {
    fn default() -> Self {
        Self::new()
    }
}

fn open_error() -> ServiceError {
    ServiceError::unavailable(anyhow!("circuit breaker is open"))
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Arc,
        atomic::{AtomicU32, Ordering},
    };

    use super::{Breaker, DEFAULT_FAILURE_THRESHOLD, DEFAULT_OPEN_DURATION, State};

    #[test]
    fn opens_after_consecutive_failures() {
        let now = Arc::new(std::sync::Mutex::new(std::time::Instant::now()));
        let breaker = clocked(&now);
        for _ in 0..DEFAULT_FAILURE_THRESHOLD - 1 {
            breaker.allow().expect("allow");
            breaker.failure();
            assert_eq!(breaker.state(), State::Closed);
        }
        breaker.allow().expect("allow at threshold");
        breaker.failure();
        assert_eq!(breaker.state(), State::Open);
        let error = breaker.allow().expect_err("open circuit");
        assert_eq!(error.code(), crate::error::ErrorCode::Unavailable);
        assert_eq!(error.key(), "collaboration.unavailable");
    }

    #[test]
    fn success_resets_consecutive_failures() {
        let now = Arc::new(std::sync::Mutex::new(std::time::Instant::now()));
        let breaker = clocked(&now);
        for _ in 0..DEFAULT_FAILURE_THRESHOLD - 1 {
            breaker.allow().expect("allow");
            breaker.failure();
        }
        breaker.allow().expect("allow");
        breaker.success();
        for _ in 0..DEFAULT_FAILURE_THRESHOLD - 1 {
            breaker.allow().expect("allow after reset");
            breaker.failure();
        }
        assert_eq!(breaker.state(), State::Closed);
    }

    #[test]
    fn half_open_success_closes() {
        let now = Arc::new(std::sync::Mutex::new(std::time::Instant::now()));
        let breaker = clocked(&now);
        open(&breaker);
        advance(&now, DEFAULT_OPEN_DURATION);
        breaker.allow().expect("probe");
        assert_eq!(breaker.state(), State::HalfOpen);
        breaker.success();
        assert_eq!(breaker.state(), State::Closed);
    }

    #[test]
    fn half_open_failure_reopens() {
        let now = Arc::new(std::sync::Mutex::new(std::time::Instant::now()));
        let breaker = clocked(&now);
        open(&breaker);
        advance(&now, DEFAULT_OPEN_DURATION);
        breaker.allow().expect("probe");
        breaker.failure();
        assert_eq!(breaker.state(), State::Open);
        assert!(breaker.allow().is_err());
    }

    #[test]
    fn only_one_half_open_probe() {
        let now = Arc::new(std::sync::Mutex::new(std::time::Instant::now()));
        let breaker = Arc::new(clocked(&now));
        open(&breaker);
        advance(&now, DEFAULT_OPEN_DURATION);
        let allowed = Arc::new(AtomicU32::new(0));
        std::thread::scope(|scope| {
            for _ in 0..8 {
                let breaker = Arc::clone(&breaker);
                let allowed = Arc::clone(&allowed);
                scope.spawn(move || {
                    if breaker.allow().is_ok() {
                        allowed.fetch_add(1, Ordering::SeqCst);
                    }
                });
            }
        });
        assert_eq!(allowed.load(Ordering::SeqCst), 1);
    }

    fn clocked(now: &Arc<std::sync::Mutex<std::time::Instant>>) -> Breaker {
        let now = Arc::clone(now);
        Breaker::with_clock(move || *now.lock().expect("clock"))
    }

    fn open(breaker: &Breaker) {
        for _ in 0..DEFAULT_FAILURE_THRESHOLD {
            breaker.allow().expect("allow");
            breaker.failure();
        }
        assert_eq!(breaker.state(), State::Open);
    }

    fn advance(now: &Arc<std::sync::Mutex<std::time::Instant>>, duration: std::time::Duration) {
        let mut current = now.lock().expect("clock");
        *current += duration;
    }
}
