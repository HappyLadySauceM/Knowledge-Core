use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

use async_trait::async_trait;
use tokio::sync::Mutex;

use crate::{
    config::RpcConfig,
    error::{Result, ServiceError},
    generated::collaboration::{CollaborationService, CollaborationServiceServer},
};

use super::{RequestContextLayer, biz::KitexBizCompatibilityLayer, tls::RpcIncoming};

/// Provides an extra readiness gate composed into the RPC server.
/// 为 RPC 服务提供可组合的额外就绪检查。
#[async_trait]
pub trait RpcReadiness: Send + Sync {
    /// Checks whether the composed readiness dependency is healthy.
    /// 检查组合进来的就绪依赖是否健康。
    ///
    /// # Errors
    ///
    /// Returns an error while the dependency is absent or unhealthy.
    async fn ready(&self) -> Result<()>;
}

/// Marks RPC listener readiness as sufficient once the server is serving.
/// 在 listener 已开始 serving 后，不再依赖外部注册握手。
pub struct AlwaysReady;

#[async_trait]
impl RpcReadiness for AlwaysReady {
    async fn ready(&self) -> Result<()> {
        Ok(())
    }
}

pub struct RpcServer<H> {
    handler: H,
    incoming: Mutex<Option<RpcIncoming>>,
    readiness: Arc<dyn RpcReadiness>,
    request_timeout: Duration,
    shutdown: tokio_util::sync::CancellationToken,
    started: AtomicBool,
    stopped: AtomicBool,
}

struct ServeExitGuard<'a> {
    stopped: &'a AtomicBool,
}

impl<'a> ServeExitGuard<'a> {
    fn new(stopped: &'a AtomicBool) -> Self {
        Self { stopped }
    }
}

impl Drop for ServeExitGuard<'_> {
    fn drop(&mut self) {
        self.stopped.store(true, Ordering::Release);
    }
}

impl<H> RpcServer<H>
where
    H: CollaborationService + Clone + Send + Sync + 'static,
{
    /// Creates a server from a listener that was bound before service registration.
    ///
    /// # Errors
    ///
    /// Returns an error when configuration or required dependencies are invalid.
    pub fn new(
        config: &RpcConfig,
        handler: H,
        incoming: RpcIncoming,
        readiness: Arc<dyn RpcReadiness>,
    ) -> Result<Self> {
        if config.request_timeout.is_zero()
            || config.service_name.trim() != config.service_name
            || config.service_name.is_empty()
            || config.service_name.contains('/')
        {
            return Err(ServiceError::invalid_input(
                "Collaboration RPC server configuration is invalid",
            ));
        }
        let shutdown = incoming.shutdown_token();
        Ok(Self {
            handler,
            incoming: Mutex::new(Some(incoming)),
            readiness,
            request_timeout: config.request_timeout,
            shutdown,
            started: AtomicBool::new(false),
            stopped: AtomicBool::new(false),
        })
    }

    /// Runs the Volo accept loop in the calling task.
    ///
    /// # Errors
    ///
    /// Returns an error when called more than once or when the transport fails.
    pub async fn serve(&self) -> Result<()> {
        if self.started.swap(true, Ordering::AcqRel) {
            return Err(ServiceError::conflict(
                "Collaboration RPC server has already started",
            ));
        }
        let _exit_guard = ServeExitGuard::new(&self.stopped);
        let incoming = self.incoming.lock().await.take().ok_or_else(|| {
            ServiceError::internal(anyhow::anyhow!("RPC listener is unavailable"))
        })?;
        let layer = RequestContextLayer::new(self.request_timeout)?;
        let result = CollaborationServiceServer::new(self.handler.clone())
            .layer(layer)
            .layer(KitexBizCompatibilityLayer)
            .run(incoming)
            .await;
        result.map_err(|error| {
            ServiceError::unavailable(anyhow::anyhow!(error.to_string()).context("serve RPC"))
        })
    }

    /// Checks listener state and the composed readiness probe.
    /// 检查 listener 状态以及组合进来的就绪探针。
    ///
    /// # Errors
    ///
    /// Returns an error before serving, after shutdown, or while the probe is unhealthy.
    pub async fn ready(&self) -> Result<()> {
        if !self.started.load(Ordering::Acquire)
            || self.stopped.load(Ordering::Acquire)
            || self.shutdown.is_cancelled()
        {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "Collaboration RPC server is not serving"
            )));
        }
        self.readiness.ready().await
    }

    /// Stops accepting new connections. The owner must await the `serve` task separately.
    ///
    /// # Errors
    ///
    /// This operation is idempotent and currently cannot fail.
    pub fn shutdown(&self) -> std::future::Ready<Result<()>> {
        self.shutdown.cancel();
        std::future::ready(Ok(()))
    }
}

#[cfg(test)]
mod tests {
    use std::{
        panic::{AssertUnwindSafe, catch_unwind},
        sync::atomic::{AtomicBool, Ordering},
    };

    use super::ServeExitGuard;

    #[test]
    fn serve_exit_guard_marks_stopped_after_normal_exit() {
        let stopped = AtomicBool::new(false);

        {
            let _guard = ServeExitGuard::new(&stopped);
        }

        assert!(stopped.load(Ordering::Acquire));
    }

    #[test]
    fn serve_exit_guard_marks_stopped_during_panic_unwind() {
        let stopped = AtomicBool::new(false);

        let unwind = catch_unwind(AssertUnwindSafe(|| {
            let _guard = ServeExitGuard::new(&stopped);
            panic!("simulated RPC listener panic");
        }));

        assert!(unwind.is_err());
        assert!(stopped.load(Ordering::Acquire));
    }
}
