use knowledge_core_collaboration::{
    app::Application,
    config::Config,
    domain::RequestContext,
    error::{Result, ServiceError},
    storage::{EventSubjects, PostgresStore},
    worker::NatsClient,
};
use std::{env, time::Duration};
use uuid::Uuid;

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
    if env::args().nth(1).as_deref() == Some("maintenance") {
        return run_maintenance().await;
    }
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

async fn run_maintenance() -> Result<()> {
    let mut arguments = env::args().skip(2);
    let Some(command) = arguments.next() else {
        return Err(ServiceError::invalid_input(
            "maintenance command is required (outbox-redrive or parking-redrive)",
        ));
    };
    let arguments = arguments.collect::<Vec<_>>();
    match command.as_str() {
        "outbox-redrive" => run_outbox_redrive(arguments).await,
        "parking-redrive" => run_parking_redrive(arguments).await,
        _ => Err(ServiceError::invalid_input(
            "unsupported maintenance command",
        )),
    }
}

struct MaintenanceOptions {
    limit: usize,
    operator: String,
    event_id: Option<Uuid>,
}

fn parse_maintenance_options(
    arguments: Vec<String>,
    allow_event_id: bool,
) -> Result<MaintenanceOptions> {
    let mut confirmed = false;
    let mut limit = 1_usize;
    let mut event_id = None;
    let mut operator = env::var("COLLABORATION_MAINTENANCE_OPERATOR").unwrap_or_default();
    let mut arguments = arguments.into_iter();
    while let Some(argument) = arguments.next() {
        match argument.as_str() {
            "--confirm" => confirmed = true,
            "--limit" => {
                let value = arguments.next().ok_or_else(|| {
                    ServiceError::invalid_input("--limit requires a positive integer")
                })?;
                limit = value.parse().map_err(|error| {
                    ServiceError::invalid_input("--limit must be a positive integer")
                        .with_source(error)
                })?;
            }
            "--event-id" => {
                if !allow_event_id {
                    return Err(ServiceError::invalid_input(
                        "--event-id is only valid for outbox-redrive",
                    ));
                }
                let value = arguments
                    .next()
                    .ok_or_else(|| ServiceError::invalid_input("--event-id requires a UUID"))?;
                event_id = Some(Uuid::parse_str(&value).map_err(|error| {
                    ServiceError::invalid_input("--event-id must be a UUID").with_source(error)
                })?);
            }
            "--operator" => {
                operator = arguments.next().ok_or_else(|| {
                    ServiceError::invalid_input("--operator requires a non-empty value")
                })?;
            }
            _ => {
                return Err(ServiceError::invalid_input(
                    "unsupported maintenance option",
                ));
            }
        }
    }
    if !confirmed {
        return Err(ServiceError::invalid_input("redrive requires --confirm"));
    }
    Ok(MaintenanceOptions {
        limit,
        operator,
        event_id,
    })
}

async fn run_outbox_redrive(arguments: Vec<String>) -> Result<()> {
    let options = parse_maintenance_options(arguments, true)?;
    let limit = i64::try_from(options.limit)
        .map_err(|error| ServiceError::invalid_input("--limit is too large").with_source(error))?;
    let config = Config::from_environment()?;
    let store = PostgresStore::open(
        &config.postgres,
        EventSubjects::new(config.nats.update_subject, config.nats.invalidation_subject),
    )
    .await?;
    let mut context = RequestContext::new(format!("maintenance-{}", Uuid::now_v7()));
    context.deadline = std::time::Instant::now().checked_add(Duration::from_secs(30));
    let redriven = store
        .redrive_outbox(&context, limit, &options.operator, options.event_id)
        .await;
    store.close().await;
    let ids = redriven?;
    println!("outbox_redriven={}", ids.len());
    Ok(())
}

async fn run_parking_redrive(arguments: Vec<String>) -> Result<()> {
    let options = parse_maintenance_options(arguments, false)?;
    let config = Config::from_environment()?;
    let nats = NatsClient::connect(&config.nats, &config.instance_id).await?;
    let redriven = nats.redrive_parked(options.limit, &options.operator).await;
    let shutdown = nats.shutdown(Duration::from_secs(5)).await;
    let count = redriven?;
    shutdown?;
    println!("parking_redriven={count}");
    Ok(())
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
