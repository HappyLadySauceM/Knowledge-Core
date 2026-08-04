use knowledge_core_collaboration::{
    app::Application,
    config::Config,
    error::{Result, ServiceError},
};

#[tokio::main]
async fn main() {
    if let Err(error) = Box::pin(run()).await {
        eprintln!(
            "knowledge-core Collaboration stopped with {}: {}",
            error.key(),
            error.detail()
        );
        std::process::exit(1);
    }
}

async fn run() -> Result<()> {
    let application = Box::pin(Application::start(Config::load().await?)).await?;
    let component_failed = tokio::select! {
        signal = shutdown_signal() => {
            signal?;
            tracing::info!(component = "collaboration.runtime", "shutdown signal received");
            false
        }
        () = application.wait_for_failure() => true,
    };
    let shutdown = application.shutdown().await;
    if component_failed {
        shutdown?;
        return Err(ServiceError::unavailable(anyhow::anyhow!(
            "a required Collaboration runtime component stopped"
        )));
    }
    shutdown
}

#[cfg(unix)]
async fn shutdown_signal() -> Result<()> {
    use tokio::signal::unix::{SignalKind, signal};

    let mut terminate = signal(SignalKind::terminate()).map_err(|error| {
        ServiceError::internal(anyhow::Error::new(error).context("install SIGTERM listener"))
    })?;
    tokio::select! {
        result = tokio::signal::ctrl_c() => result.map_err(|error| {
            ServiceError::internal(anyhow::Error::new(error).context("wait for SIGINT"))
        }),
        _ = terminate.recv() => Ok(()),
    }
}

#[cfg(not(unix))]
async fn shutdown_signal() -> Result<()> {
    tokio::signal::ctrl_c().await.map_err(|error| {
        ServiceError::internal(anyhow::Error::new(error).context("wait for Ctrl+C"))
    })
}
