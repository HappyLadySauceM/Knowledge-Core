use std::{
    net::SocketAddr,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

use axum::{
    Router,
    body::Body,
    http::{HeaderValue, Response, StatusCode, header},
    routing::get,
};
use tokio::{net::TcpListener, sync::Mutex, task::JoinHandle};
use tokio_util::sync::CancellationToken;

use crate::{
    config::AdminConfig,
    error::{Result, ServiceError},
    telemetry::Metrics,
};

const HEALTH_CONTENT_TYPE: HeaderValue =
    HeaderValue::from_static("application/json; charset=utf-8");
const METRICS_CONTENT_TYPE: HeaderValue =
    HeaderValue::from_static("text/plain; version=0.0.4; charset=utf-8");

#[derive(Clone, Default)]
pub struct HealthState {
    inner: Arc<HealthInner>,
}

#[derive(Default)]
struct HealthInner {
    live: AtomicBool,
    ready: AtomicBool,
}

impl HealthState {
    pub fn start(&self) {
        self.inner.live.store(true, Ordering::Release);
    }

    pub fn set_ready(&self, ready: bool) {
        self.inner.ready.store(ready, Ordering::Release);
    }

    pub fn stop(&self) {
        self.inner.ready.store(false, Ordering::Release);
        self.inner.live.store(false, Ordering::Release);
    }

    pub fn is_live(&self) -> bool {
        self.inner.live.load(Ordering::Acquire)
    }

    pub fn is_ready(&self) -> bool {
        self.is_live() && self.inner.ready.load(Ordering::Acquire)
    }
}

#[derive(Clone)]
struct AdminState {
    health: HealthState,
    metrics: Metrics,
}

pub struct AdminServer {
    local_address: SocketAddr,
    cancellation: CancellationToken,
    running: Arc<AtomicBool>,
    task: Mutex<Option<JoinHandle<Result<()>>>>,
}

impl AdminServer {
    /// Binds and starts the internal health and metrics listener.
    ///
    /// # Errors
    ///
    /// Returns an error when the listener cannot bind or its address cannot be read.
    pub async fn start(
        config: &AdminConfig,
        health: HealthState,
        prometheus_metrics: Metrics,
        parent_cancellation: &CancellationToken,
    ) -> Result<Self> {
        let listener = TcpListener::bind(config.address).await.map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("bind admin listener"))
        })?;
        let local_address = listener.local_addr().map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("read admin listener address"))
        })?;
        let state = AdminState {
            health: health.clone(),
            metrics: prometheus_metrics,
        };
        let router = Router::new()
            .route("/health/live", get(live))
            .route("/health/ready", get(ready))
            .route("/metrics", get(metrics_handler))
            .with_state(state);
        let cancellation = parent_cancellation.child_token();
        let server_cancellation = cancellation.clone();
        let running = Arc::new(AtomicBool::new(true));
        let task_running = Arc::clone(&running);
        let task = tokio::spawn(async move {
            let result = axum::serve(listener, router)
                .with_graceful_shutdown(server_cancellation.cancelled_owned())
                .await
                .map_err(|error| {
                    ServiceError::internal(
                        anyhow::anyhow!(error).context("serve admin HTTP listener"),
                    )
                });
            task_running.store(false, Ordering::Release);
            health.set_ready(false);
            result
        });
        Ok(Self {
            local_address,
            cancellation,
            running,
            task: Mutex::new(Some(task)),
        })
    }

    pub fn local_address(&self) -> SocketAddr {
        self.local_address
    }

    pub fn is_running(&self) -> bool {
        self.running.load(Ordering::Acquire)
    }

    /// Stops the admin listener within the supplied shutdown budget.
    ///
    /// # Errors
    ///
    /// Returns an error when the listener task fails or exceeds the deadline.
    pub async fn shutdown(&self, maximum_wait: Duration) -> Result<()> {
        self.cancellation.cancel();
        let Some(mut task) = self.task.lock().await.take() else {
            return Ok(());
        };
        if let Ok(result) = tokio::time::timeout(maximum_wait, &mut task).await {
            result.map_err(|error| {
                ServiceError::internal(anyhow::anyhow!(error).context("join admin server task"))
            })?
        } else {
            task.abort();
            let _ = task.await;
            Err(ServiceError::internal(anyhow::anyhow!(
                "admin server did not stop before the shutdown deadline"
            )))
        }
    }
}

async fn live(axum::extract::State(state): axum::extract::State<AdminState>) -> Response<Body> {
    health_response(state.health.is_live())
}

async fn ready(axum::extract::State(state): axum::extract::State<AdminState>) -> Response<Body> {
    health_response(state.health.is_ready())
}

async fn metrics_handler(
    axum::extract::State(state): axum::extract::State<AdminState>,
) -> Response<Body> {
    match state.metrics.encode() {
        Ok(body) => response(StatusCode::OK, METRICS_CONTENT_TYPE, body),
        Err(_) => response(
            StatusCode::INTERNAL_SERVER_ERROR,
            HEALTH_CONTENT_TYPE,
            br#"{"status":"error"}"#.to_vec(),
        ),
    }
}

fn health_response(healthy: bool) -> Response<Body> {
    if healthy {
        response(
            StatusCode::OK,
            HEALTH_CONTENT_TYPE,
            br#"{"status":"ok","service":"collaboration"}"#.to_vec(),
        )
    } else {
        response(
            StatusCode::SERVICE_UNAVAILABLE,
            HEALTH_CONTENT_TYPE,
            br#"{"status":"unavailable","service":"collaboration"}"#.to_vec(),
        )
    }
}

fn response(status: StatusCode, content_type: HeaderValue, body: Vec<u8>) -> Response<Body> {
    let mut response = Response::new(Body::from(body));
    *response.status_mut() = status;
    let headers = response.headers_mut();
    headers.insert(header::CONTENT_TYPE, content_type);
    headers.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    headers.insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
    response
}
