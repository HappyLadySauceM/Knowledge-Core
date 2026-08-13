use std::{
    collections::HashMap,
    future::Future,
    net::SocketAddr,
    sync::{Arc, Mutex as StdMutex},
    time::{Duration, Instant},
};

use async_trait::async_trait;
use tokio::{sync::Mutex, task::JoinHandle};
use tokio_util::sync::CancellationToken;

use crate::{
    actor::{ActorLimits, ActorRegistry},
    admin::{AdminServer, HealthState},
    config::Config,
    domain::RequestContext,
    error::{Result, ServiceError},
    ports::KnowledgePort,
    remote_config::RemoteRuntime,
    routing::{RedisRoutingStore, RoutingService, parse_instance_ordinal},
    rpc::{
        CollaborationHandler, KnowledgeClient, RpcReadiness, RpcServer,
        etcd::{EtcdDiscovery, EtcdRegistration},
        tls::RpcIncoming,
    },
    storage::{DocumentStore, EventSubjects, PostgresStore, VersionStore, WorkerStore},
    telemetry::{Metrics, Telemetry},
    ticket::{RedisTicketBackend, TicketBackend, TicketService},
    websocket::WebSocketServer,
    worker::{NatsClient, WorkerRuntime},
};

const READINESS_INTERVAL: Duration = Duration::from_secs(1);

#[derive(Clone)]
struct ApplicationReadiness {
    health: HealthState,
}

impl ApplicationReadiness {
    fn new(health: HealthState) -> Self {
        Self { health }
    }
}

#[async_trait]
impl RpcReadiness for ApplicationReadiness {
    async fn ready(&self) -> Result<()> {
        if self.health.is_ready() {
            Ok(())
        } else {
            Err(ServiceError::unavailable(anyhow::anyhow!(
                "Collaboration application is not ready"
            )))
        }
    }
}

pub struct Application {
    shutdown_timeout: Duration,
    root_cancellation: CancellationToken,
    supervisor_cancellation: CancellationToken,
    rpc_exit_expected: CancellationToken,
    rpc_startup_gate: Arc<RpcStartupGate>,
    failure: CancellationToken,
    health: HealthState,
    telemetry: Mutex<Option<Telemetry>>,
    remote: Option<RemoteRuntime>,
    postgres: Arc<PostgresStore>,
    tickets: TicketService,
    knowledge: Arc<dyn KnowledgePort>,
    actors: ActorRegistry,
    workers: Arc<WorkerRuntime>,
    discovery: EtcdDiscovery,
    registration: Arc<EtcdRegistration>,
    rpc: Arc<RpcServer<CollaborationHandler>>,
    rpc_task: Mutex<Option<JoinHandle<Result<()>>>>,
    public: Arc<WebSocketServer>,
    admin: Arc<AdminServer>,
    supervisor_task: Mutex<Option<JoinHandle<()>>>,
    stopped: Mutex<bool>,
}

struct RpcTaskExitGuard<F>
where
    F: FnOnce(),
{
    expected_exit: CancellationToken,
    startup_gate: Arc<RpcStartupGate>,
    on_unexpected_exit: Option<F>,
}

impl<F> RpcTaskExitGuard<F>
where
    F: FnOnce(),
{
    fn new(
        expected_exit: CancellationToken,
        startup_gate: Arc<RpcStartupGate>,
        on_unexpected_exit: F,
    ) -> Self {
        Self {
            expected_exit,
            startup_gate,
            on_unexpected_exit: Some(on_unexpected_exit),
        }
    }
}

impl<F> Drop for RpcTaskExitGuard<F>
where
    F: FnOnce(),
{
    fn drop(&mut self) {
        if let Some(on_unexpected_exit) = self.on_unexpected_exit.take() {
            self.startup_gate
                .task_exit(&self.expected_exit, on_unexpected_exit);
        }
    }
}

#[derive(Default)]
struct RpcStartupGate {
    state: StdMutex<RpcStartupState>,
}

#[derive(Default, PartialEq, Eq)]
enum RpcStartupState {
    #[default]
    Starting,
    Ready,
    Stopping,
    Exited,
}

impl RpcStartupGate {
    fn while_running<T>(
        &self,
        failure: &CancellationToken,
        action: impl FnOnce() -> Result<T>,
    ) -> Result<T> {
        let state = self
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        Self::ensure_running(&state, failure)?;
        action()
    }

    fn commit_ready(&self, failure: &CancellationToken, action: impl FnOnce()) -> Result<()> {
        let mut state = self
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        Self::ensure_running(&state, failure)?;
        action();
        *state = RpcStartupState::Ready;
        Ok(())
    }

    fn expect_exit(&self, expected_exit: &CancellationToken) {
        {
            let mut state = self
                .state
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            if *state != RpcStartupState::Exited {
                *state = RpcStartupState::Stopping;
            }
        }
        expected_exit.cancel();
    }

    fn task_exit(&self, expected_exit: &CancellationToken, action: impl FnOnce()) {
        let mut state = self
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if *state == RpcStartupState::Stopping || expected_exit.is_cancelled() {
            return;
        }
        *state = RpcStartupState::Exited;
        action();
    }

    fn ensure_running(state: &RpcStartupState, failure: &CancellationToken) -> Result<()> {
        if *state != RpcStartupState::Starting || failure.is_cancelled() {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "RPC server stopped during startup"
            )));
        }
        Ok(())
    }
}

impl Application {
    /// Starts every Collaboration dependency and listener, then opens traffic after readiness.
    ///
    /// # Errors
    ///
    /// Returns an error when configuration, telemetry, a required dependency, registration, or a
    /// listener cannot become ready. Resources created before a failure are shut down in reverse
    /// dependency order before the error is returned.
    pub async fn start(config: Config) -> Result<Self> {
        crate::rpc::tls::install_crypto_provider();
        let telemetry = Telemetry::initialize(config.environment, &config.telemetry)?;
        let metrics = match Metrics::new() {
            Ok(metrics) => metrics,
            Err(error) => {
                let _ = telemetry.shutdown();
                return Err(error);
            }
        };
        let mut startup = Startup::new(telemetry);
        match Self::assemble(config, metrics, &mut startup).await {
            Ok(application) => Ok(application),
            Err(error) => {
                startup.rollback().await;
                Err(error)
            }
        }
    }

    #[allow(clippy::too_many_lines)]
    async fn assemble(config: Config, metrics: Metrics, startup: &mut Startup) -> Result<Self> {
        let subjects = EventSubjects::new(
            config.nats.update_subject.clone(),
            config.nats.invalidation_subject.clone(),
        );
        let postgres = Arc::new(PostgresStore::open(&config.postgres, subjects).await?);
        startup.postgres = Some(Arc::clone(&postgres));

        let redis: Arc<dyn TicketBackend> =
            Arc::new(RedisTicketBackend::open(&config.redis).await?);
        let tickets = TicketService::new(redis, &config.ticket)?;
        startup.tickets = Some(tickets.clone());

        let local_ordinal =
            parse_instance_ordinal(&config.instance_id, config.routing.instance_count)?;
        let max_connections = u32::try_from(config.public.max_connections).map_err(|error| {
            ServiceError::internal(
                anyhow::anyhow!(error).context("encode collaboration max connections"),
            )
        })?;
        let routing = RoutingService::new(
            Arc::new(RedisRoutingStore::open(&config.redis).await?),
            config.routing.instance_count,
            local_ordinal,
            max_connections,
        )?;
        routing.publish_load(0).await?;

        let nats = Arc::new(NatsClient::connect(&config.nats, &config.instance_id).await?);
        startup.nats = Some(Arc::clone(&nats));

        let etcd = crate::rpc::etcd::EtcdClient::connect(&config.etcd).await?;
        let discovery = etcd
            .discover(
                &config.knowledge.service_name,
                startup.root_cancellation.child_token(),
            )
            .await?;
        startup.discovery = Some(discovery.clone());
        let knowledge: Arc<dyn KnowledgePort> =
            Arc::new(KnowledgeClient::new(&config.knowledge, discovery.clone())?);
        let startup_context = RequestContext::new("collaboration-startup");
        knowledge.ping(&startup_context).await?;
        startup.knowledge = Some(Arc::clone(&knowledge));

        let document_store: Arc<dyn DocumentStore> = postgres.clone();
        let actor_limits = ActorLimits::from_config(&config.actor, &config.public)?;
        let actors = ActorRegistry::new(
            document_store,
            actor_limits,
            metrics.clone(),
            startup.root_cancellation.child_token(),
        );
        startup.actors = Some(actors.clone());

        let worker_store: Arc<dyn WorkerStore> = postgres.clone();
        let workers = Arc::new(
            WorkerRuntime::start(
                &config.workers,
                &config.nats,
                worker_store,
                Arc::clone(&knowledge),
                Arc::clone(&nats),
                actors.clone(),
                metrics.clone(),
                startup.health.clone(),
                &startup.root_cancellation,
            )
            .await?,
        );
        startup.workers = Some(Arc::clone(&workers));

        let incoming = RpcIncoming::bind(
            config.rpc.address,
            &config.rpc.tls,
            config.rpc.request_timeout,
            startup.root_cancellation.child_token(),
        )
        .await?;
        let rpc_address = incoming.local_addr()?;

        let public = Arc::new(
            WebSocketServer::start(
                &config.public,
                &config.ticket,
                tickets.clone(),
                routing.clone(),
                actors.clone(),
                metrics.clone(),
                startup.health.clone(),
                &startup.root_cancellation,
            )
            .await?,
        );
        startup.public = Some(Arc::clone(&public));

        if let Some(remote) = &config.remote {
            let log = startup
                .telemetry
                .as_ref()
                .ok_or_else(|| {
                    ServiceError::internal(anyhow::anyhow!(
                        "telemetry was not retained during application startup"
                    ))
                })?
                .log_controller();
            let targets = crate::remote_config::RuntimeTargets {
                log,
                startup_log_level: config.telemetry.log_level.clone(),
                tickets: tickets.clone(),
                actors: actors.clone(),
                public: Arc::clone(&public),
                startup_public: config.public.clone(),
                startup_ticket: config.ticket.clone(),
                startup_actor: config.actor.clone(),
                startup_workers: config.workers.clone(),
                startup_routing: config.routing.clone(),
            };
            startup.remote = Some(
                remote
                    .start(
                        targets,
                        metrics.clone(),
                        startup.root_cancellation.child_token(),
                    )
                    .await?,
            );
        }

        let admin = Arc::new(
            AdminServer::start(
                &config.admin,
                startup.health.clone(),
                metrics,
                &startup.root_cancellation,
            )
            .await?,
        );
        startup.admin = Some(Arc::clone(&admin));
        startup.health.start();

        let mut tags = HashMap::new();
        tags.insert("instance_id".to_owned(), config.instance_id.clone());
        let registration = Arc::new(
            etcd.register(
                &config.rpc.service_name,
                &config.rpc.advertised_address,
                tags,
                startup.root_cancellation.child_token(),
            )
            .await?,
        );
        startup.registration = Some(Arc::clone(&registration));
        let rpc_readiness: Arc<dyn RpcReadiness> = registration.clone();
        let application_readiness: Arc<dyn RpcReadiness> =
            Arc::new(ApplicationReadiness::new(startup.health.clone()));
        let versions: Arc<dyn VersionStore> = postgres.clone();
        let handler = CollaborationHandler::new(
            Arc::clone(&knowledge),
            tickets.clone(),
            versions,
            actors.clone(),
            routing,
            &config.ticket,
            application_readiness,
        )?;
        let rpc = Arc::new(RpcServer::new(
            &config.rpc,
            handler,
            incoming,
            rpc_readiness,
        )?);
        startup.rpc = Some(Arc::clone(&rpc));
        let rpc_startup_gate = Arc::clone(&startup.rpc_startup_gate);
        let rpc_exit_guard = RpcTaskExitGuard::new(
            startup.rpc_exit_expected.clone(),
            Arc::clone(&rpc_startup_gate),
            {
                let health = startup.health.clone();
                let public = Arc::clone(&public);
                let failure = startup.failure.clone();
                move || {
                    health.set_ready(false);
                    public.stop_accepting();
                    tracing::error!(
                        component = "collaboration.runtime",
                        "RPC server task exited unexpectedly"
                    );
                    failure.cancel();
                }
            },
        );
        let rpc_task = tokio::spawn({
            let rpc = Arc::clone(&rpc);
            async move {
                let _exit_guard = rpc_exit_guard;
                rpc.serve().await
            }
        });
        wait_for_rpc(&rpc, &rpc_task, config.rpc.request_timeout).await?;
        startup.rpc_task = Some(rpc_task);

        workers.ready().await?;
        discovery.ready().await?;
        registration.ready().await?;
        postgres.ping().await?;
        tickets.ping().await?;
        knowledge
            .ping(&RequestContext::new("collaboration-startup-readiness"))
            .await?;
        if !admin.is_running() {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "admin listener stopped during startup"
            )));
        }
        rpc_startup_gate.while_running(&startup.failure, || public.start_accepting())?;
        public.ready().await?;
        rpc.ready().await?;
        rpc_startup_gate.commit_ready(&startup.failure, || startup.health.set_ready(true))?;

        let application = Self {
            shutdown_timeout: config.shutdown_timeout,
            root_cancellation: startup.root_cancellation.clone(),
            supervisor_cancellation: startup.supervisor_cancellation.clone(),
            rpc_exit_expected: startup.rpc_exit_expected.clone(),
            rpc_startup_gate: Arc::clone(&startup.rpc_startup_gate),
            failure: startup.failure.clone(),
            health: startup.health.clone(),
            telemetry: Mutex::new(startup.telemetry.take()),
            remote: startup.remote.take(),
            postgres,
            tickets,
            knowledge,
            actors,
            workers,
            discovery,
            registration,
            rpc,
            rpc_task: Mutex::new(startup.rpc_task.take()),
            public,
            admin,
            supervisor_task: Mutex::new(None),
            stopped: Mutex::new(false),
        };
        application.start_supervisor().await;
        tracing::info!(
            component = "collaboration.runtime",
            public.address = %application.public.local_address(),
            rpc.address = %rpc_address,
            admin.address = %application.admin.local_address(),
            "Collaboration service is ready"
        );
        Ok(application)
    }

    async fn start_supervisor(&self) {
        let task = tokio::spawn(supervise(SupervisedComponents {
            health: self.health.clone(),
            postgres: Arc::clone(&self.postgres),
            tickets: self.tickets.clone(),
            knowledge: Arc::clone(&self.knowledge),
            workers: Arc::clone(&self.workers),
            discovery: self.discovery.clone(),
            registration: Arc::clone(&self.registration),
            rpc: Arc::clone(&self.rpc),
            public: Arc::clone(&self.public),
            admin: Arc::clone(&self.admin),
            cancellation: self.supervisor_cancellation.child_token(),
            failure: self.failure.clone(),
        }));
        *self.supervisor_task.lock().await = Some(task);
    }

    pub fn public_address(&self) -> SocketAddr {
        self.public.local_address()
    }

    pub fn admin_address(&self) -> SocketAddr {
        self.admin.local_address()
    }

    pub fn health(&self) -> HealthState {
        self.health.clone()
    }

    pub async fn wait_for_failure(&self) {
        self.failure.cancelled().await;
    }

    /// Stops all traffic and joins every task within the configured process deadline.
    ///
    /// # Errors
    ///
    /// Returns the first shutdown error after still attempting every remaining cleanup step.
    pub async fn shutdown(&self) -> Result<()> {
        let mut stopped = self.stopped.lock().await;
        if *stopped {
            return Ok(());
        }
        *stopped = true;
        let deadline = Instant::now() + self.shutdown_timeout;
        let mut first_error = None;

        self.rpc_startup_gate.expect_exit(&self.rpc_exit_expected);
        self.health.set_ready(false);
        self.public.stop_accepting();
        self.supervisor_cancellation.cancel();
        record(
            &mut first_error,
            join_unit_task(
                &self.supervisor_task,
                remaining(deadline),
                "readiness supervisor",
            )
            .await,
        );
        if let Some(remote) = &self.remote {
            record(&mut first_error, remote.shutdown(remaining(deadline)).await);
        }
        record(
            &mut first_error,
            bounded_cleanup(
                remaining(deadline),
                self.registration.shutdown(),
                "Etcd registration",
            )
            .await,
        );
        record(
            &mut first_error,
            bounded_cleanup(remaining(deadline), self.rpc.shutdown(), "RPC server").await,
        );
        record(
            &mut first_error,
            self.public.shutdown(remaining(deadline)).await,
        );
        record(
            &mut first_error,
            join_result_task(&self.rpc_task, remaining(deadline), "RPC server").await,
        );
        record(
            &mut first_error,
            self.actors.shutdown(remaining(deadline)).await,
        );
        record(
            &mut first_error,
            self.workers.shutdown(remaining(deadline)).await,
        );
        record(
            &mut first_error,
            bounded_cleanup(
                remaining(deadline),
                self.discovery.shutdown(),
                "Etcd discovery",
            )
            .await,
        );
        record(
            &mut first_error,
            close_postgres(&self.postgres, remaining(deadline)).await,
        );
        self.root_cancellation.cancel();
        self.health.stop();
        record(
            &mut first_error,
            self.admin.shutdown(remaining(deadline)).await,
        );
        if let Some(telemetry) = self.telemetry.lock().await.take() {
            record(
                &mut first_error,
                telemetry.shutdown_with_timeout(remaining(deadline)),
            );
        }
        tracing::info!(
            component = "collaboration.runtime",
            "Collaboration service stopped"
        );
        first_error.map_or(Ok(()), Err)
    }
}

impl Drop for Application {
    fn drop(&mut self) {
        self.rpc_startup_gate.expect_exit(&self.rpc_exit_expected);
        self.health.stop();
        self.public.stop_accepting();
        self.supervisor_cancellation.cancel();
        self.root_cancellation.cancel();
        if let Some(remote) = &self.remote {
            remote.stop();
        }
    }
}

struct SupervisedComponents {
    health: HealthState,
    postgres: Arc<PostgresStore>,
    tickets: TicketService,
    knowledge: Arc<dyn KnowledgePort>,
    workers: Arc<WorkerRuntime>,
    discovery: EtcdDiscovery,
    registration: Arc<EtcdRegistration>,
    rpc: Arc<RpcServer<CollaborationHandler>>,
    public: Arc<WebSocketServer>,
    admin: Arc<AdminServer>,
    cancellation: CancellationToken,
    failure: CancellationToken,
}

async fn supervise(components: SupervisedComponents) {
    let mut interval = tokio::time::interval(READINESS_INTERVAL);
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            biased;
            () = components.cancellation.cancelled() => break,
            _ = interval.tick() => {
                if let Err(error) = components_ready(&components).await {
                    components.health.set_ready(false);
                    components.public.stop_accepting();
                    tracing::error!(
                        component = "collaboration.runtime",
                        error_key = error.key(),
                        "required runtime component became unhealthy"
                    );
                    components.failure.cancel();
                    break;
                }
            }
        }
    }
}

async fn components_ready(components: &SupervisedComponents) -> Result<()> {
    if !components.admin.is_running() {
        return Err(ServiceError::unavailable(anyhow::anyhow!(
            "admin listener is not running"
        )));
    }
    components.postgres.ping().await?;
    components.tickets.ping().await?;
    components.workers.ready().await?;
    components.discovery.ready().await?;
    components.registration.ready().await?;
    components
        .knowledge
        .ping(&RequestContext::new("collaboration-readiness"))
        .await?;
    components.rpc.ready().await?;
    components.public.ready().await
}

async fn wait_for_rpc(
    rpc: &RpcServer<CollaborationHandler>,
    task: &JoinHandle<Result<()>>,
    maximum_wait: Duration,
) -> Result<()> {
    tokio::time::timeout(maximum_wait, async {
        loop {
            if task.is_finished() {
                return Err(ServiceError::unavailable(anyhow::anyhow!(
                    "RPC server stopped during startup"
                )));
            }
            if rpc.ready().await.is_ok() {
                return Ok(());
            }
            tokio::task::yield_now().await;
        }
    })
    .await
    .map_err(|_| {
        ServiceError::unavailable(anyhow::anyhow!(
            "RPC server did not become ready before the startup deadline"
        ))
    })?
}

struct Startup {
    telemetry: Option<Telemetry>,
    remote: Option<RemoteRuntime>,
    root_cancellation: CancellationToken,
    supervisor_cancellation: CancellationToken,
    rpc_exit_expected: CancellationToken,
    rpc_startup_gate: Arc<RpcStartupGate>,
    failure: CancellationToken,
    health: HealthState,
    postgres: Option<Arc<PostgresStore>>,
    tickets: Option<TicketService>,
    knowledge: Option<Arc<dyn KnowledgePort>>,
    nats: Option<Arc<NatsClient>>,
    actors: Option<ActorRegistry>,
    workers: Option<Arc<WorkerRuntime>>,
    discovery: Option<EtcdDiscovery>,
    registration: Option<Arc<EtcdRegistration>>,
    rpc: Option<Arc<RpcServer<CollaborationHandler>>>,
    rpc_task: Option<JoinHandle<Result<()>>>,
    public: Option<Arc<WebSocketServer>>,
    admin: Option<Arc<AdminServer>>,
}

impl Startup {
    fn new(telemetry: Telemetry) -> Self {
        Self {
            telemetry: Some(telemetry),
            remote: None,
            root_cancellation: CancellationToken::new(),
            supervisor_cancellation: CancellationToken::new(),
            rpc_exit_expected: CancellationToken::new(),
            rpc_startup_gate: Arc::new(RpcStartupGate::default()),
            failure: CancellationToken::new(),
            health: HealthState::default(),
            postgres: None,
            tickets: None,
            knowledge: None,
            nats: None,
            actors: None,
            workers: None,
            discovery: None,
            registration: None,
            rpc: None,
            rpc_task: None,
            public: None,
            admin: None,
        }
    }

    async fn rollback(&mut self) {
        let deadline = Instant::now() + Duration::from_secs(5);
        self.rpc_startup_gate.expect_exit(&self.rpc_exit_expected);
        self.health.set_ready(false);
        if let Some(public) = &self.public {
            public.stop_accepting();
        }
        if let Some(remote) = &self.remote {
            let _ = remote.shutdown(remaining(deadline)).await;
        }
        if let Some(registration) = &self.registration {
            let _ = bounded_cleanup(
                remaining(deadline),
                registration.shutdown(),
                "Etcd registration",
            )
            .await;
        }
        if let Some(rpc) = &self.rpc {
            let _ = bounded_cleanup(remaining(deadline), rpc.shutdown(), "RPC server").await;
        }
        if let Some(public) = &self.public {
            let _ = public.shutdown(remaining(deadline)).await;
        }
        if let Some(task) = self.rpc_task.take() {
            let _ = join_owned_result_task(task, remaining(deadline), "RPC server").await;
        }
        if let Some(actors) = &self.actors {
            let _ = actors.shutdown(remaining(deadline)).await;
        }
        if let Some(workers) = &self.workers {
            let _ = workers.shutdown(remaining(deadline)).await;
        } else if let Some(nats) = &self.nats {
            let _ = nats.shutdown(remaining(deadline)).await;
        }
        if let Some(discovery) = &self.discovery {
            let _ =
                bounded_cleanup(remaining(deadline), discovery.shutdown(), "Etcd discovery").await;
        }
        if let Some(postgres) = &self.postgres {
            let _ = close_postgres(postgres, remaining(deadline)).await;
        }
        self.root_cancellation.cancel();
        self.health.stop();
        if let Some(admin) = &self.admin {
            let _ = admin.shutdown(remaining(deadline)).await;
        }
        if let Some(telemetry) = self.telemetry.take() {
            let _ = telemetry.shutdown_with_timeout(remaining(deadline));
        }
    }
}

async fn join_result_task(
    task: &Mutex<Option<JoinHandle<Result<()>>>>,
    maximum_wait: Duration,
    component: &'static str,
) -> Result<()> {
    let Some(task) = task.lock().await.take() else {
        return Ok(());
    };
    join_owned_result_task(task, maximum_wait, component).await
}

async fn join_unit_task(
    task: &Mutex<Option<JoinHandle<()>>>,
    maximum_wait: Duration,
    component: &'static str,
) -> Result<()> {
    let Some(mut task) = task.lock().await.take() else {
        return Ok(());
    };
    if let Ok(result) = tokio::time::timeout(maximum_wait, &mut task).await {
        result.map_err(|error| {
            ServiceError::internal(anyhow::Error::new(error).context(format!("join {component}")))
        })
    } else {
        task.abort();
        let _ = task.await;
        Err(shutdown_timeout(component))
    }
}

async fn join_owned_result_task(
    mut task: JoinHandle<Result<()>>,
    maximum_wait: Duration,
    component: &'static str,
) -> Result<()> {
    if let Ok(result) = tokio::time::timeout(maximum_wait, &mut task).await {
        result.map_err(|error| {
            ServiceError::internal(anyhow::Error::new(error).context(format!("join {component}")))
        })?
    } else {
        task.abort();
        let _ = task.await;
        Err(shutdown_timeout(component))
    }
}

async fn close_postgres(store: &PostgresStore, maximum_wait: Duration) -> Result<()> {
    tokio::time::timeout(maximum_wait, store.close())
        .await
        .map_err(|_| shutdown_timeout("PostgreSQL"))?;
    Ok(())
}

fn remaining(deadline: Instant) -> Duration {
    deadline
        .checked_duration_since(Instant::now())
        .unwrap_or(Duration::ZERO)
}

async fn bounded_cleanup<F>(
    maximum_wait: Duration,
    cleanup: F,
    component: &'static str,
) -> Result<()>
where
    F: Future<Output = Result<()>>,
{
    tokio::time::timeout(maximum_wait, cleanup)
        .await
        .map_err(|_| shutdown_timeout(component))?
}

fn shutdown_timeout(component: &'static str) -> ServiceError {
    ServiceError::internal(anyhow::anyhow!(
        "{component} did not stop before the process shutdown deadline"
    ))
}

fn record(first_error: &mut Option<ServiceError>, result: Result<()>) {
    if first_error.is_none() {
        *first_error = result.err();
    }
}

#[cfg(test)]
mod tests {
    use std::{
        sync::{
            Arc,
            atomic::{AtomicBool, Ordering},
        },
        time::Duration,
    };

    use tokio_util::sync::CancellationToken;

    use crate::error::ServiceError;

    use super::{
        ApplicationReadiness, HealthState, RpcReadiness, RpcStartupGate, RpcTaskExitGuard,
        bounded_cleanup,
    };

    fn ready_health() -> HealthState {
        let health = HealthState::default();
        health.start();
        health.set_ready(true);
        health
    }

    fn started_health() -> HealthState {
        let health = HealthState::default();
        health.start();
        health
    }

    fn test_rpc_exit_guard(
        expected_exit: CancellationToken,
        startup_gate: Arc<RpcStartupGate>,
        health: HealthState,
        accepting: Arc<AtomicBool>,
        failure: CancellationToken,
    ) -> RpcTaskExitGuard<impl FnOnce()> {
        RpcTaskExitGuard::new(expected_exit, startup_gate, move || {
            health.set_ready(false);
            accepting.store(false, Ordering::Release);
            failure.cancel();
        })
    }

    #[tokio::test]
    async fn application_readiness_fails_closed_outside_the_ready_state() {
        let health = HealthState::default();
        let readiness = ApplicationReadiness::new(health.clone());

        assert!(readiness.ready().await.is_err());
        health.start();
        assert!(readiness.ready().await.is_err());

        health.set_ready(true);
        assert!(readiness.ready().await.is_ok());

        health.set_ready(false);
        assert!(readiness.ready().await.is_err());
        health.set_ready(true);
        health.stop();
        assert!(readiness.ready().await.is_err());
    }

    #[tokio::test]
    async fn bounded_cleanup_obeys_an_exhausted_process_deadline() {
        let result =
            bounded_cleanup(Duration::ZERO, std::future::pending(), "pending cleanup").await;
        assert!(result.is_err());
    }

    #[tokio::test]
    async fn rpc_exit_before_readiness_commit_cannot_reopen_startup() {
        let health = started_health();
        let accepting = Arc::new(AtomicBool::new(false));
        let failure = CancellationToken::new();
        let startup_gate = Arc::new(RpcStartupGate::default());
        let guard = test_rpc_exit_guard(
            CancellationToken::new(),
            Arc::clone(&startup_gate),
            health.clone(),
            Arc::clone(&accepting),
            failure.clone(),
        );

        tokio::spawn(async move {
            let _exit_guard = guard;
        })
        .await
        .expect("join simulated RPC task");

        let open_result = startup_gate.while_running(&failure, || {
            accepting.store(true, Ordering::Release);
            Ok(())
        });
        let commit_result = startup_gate.commit_ready(&failure, || health.set_ready(true));

        assert!(open_result.is_err());
        assert!(commit_result.is_err());
        assert!(!health.is_ready());
        assert!(!accepting.load(Ordering::Acquire));
        assert!(failure.is_cancelled());
    }

    #[tokio::test]
    async fn readiness_commit_before_rpc_exit_is_immediately_failed_closed() {
        let health = started_health();
        let accepting = Arc::new(AtomicBool::new(false));
        let failure = CancellationToken::new();
        let startup_gate = Arc::new(RpcStartupGate::default());
        let guard = test_rpc_exit_guard(
            CancellationToken::new(),
            Arc::clone(&startup_gate),
            health.clone(),
            Arc::clone(&accepting),
            failure.clone(),
        );

        startup_gate
            .while_running(&failure, || {
                accepting.store(true, Ordering::Release);
                Ok(())
            })
            .expect("open simulated public listener");
        startup_gate
            .commit_ready(&failure, || health.set_ready(true))
            .expect("commit simulated application readiness");
        assert!(health.is_ready());
        assert!(accepting.load(Ordering::Acquire));

        tokio::spawn(async move {
            let _exit_guard = guard;
        })
        .await
        .expect("join simulated RPC task");

        assert!(!health.is_ready());
        assert!(!accepting.load(Ordering::Acquire));
        assert!(failure.is_cancelled());
    }

    #[tokio::test]
    async fn normal_rpc_task_exit_fails_the_application_closed() {
        let health = ready_health();
        let accepting = Arc::new(AtomicBool::new(true));
        let failure = CancellationToken::new();
        let startup_gate = Arc::new(RpcStartupGate::default());
        let guard = test_rpc_exit_guard(
            CancellationToken::new(),
            startup_gate,
            health.clone(),
            Arc::clone(&accepting),
            failure.clone(),
        );

        tokio::spawn(async move {
            let _exit_guard = guard;
        })
        .await
        .expect("join simulated RPC task");

        assert!(!health.is_ready());
        assert!(!accepting.load(Ordering::Acquire));
        assert!(failure.is_cancelled());
    }

    #[tokio::test]
    async fn rpc_task_error_fails_the_application_closed() {
        let health = ready_health();
        let accepting = Arc::new(AtomicBool::new(true));
        let failure = CancellationToken::new();
        let startup_gate = Arc::new(RpcStartupGate::default());
        let guard = test_rpc_exit_guard(
            CancellationToken::new(),
            startup_gate,
            health.clone(),
            Arc::clone(&accepting),
            failure.clone(),
        );

        let task_result = tokio::spawn(async move {
            let _exit_guard = guard;
            Err::<(), ServiceError>(ServiceError::unavailable(anyhow::anyhow!(
                "simulated RPC listener failure"
            )))
        })
        .await
        .expect("join simulated RPC task");

        assert!(task_result.is_err());
        assert!(!health.is_ready());
        assert!(!accepting.load(Ordering::Acquire));
        assert!(failure.is_cancelled());
    }

    #[tokio::test]
    async fn rpc_task_panic_fails_the_application_closed() {
        let health = ready_health();
        let accepting = Arc::new(AtomicBool::new(true));
        let failure = CancellationToken::new();
        let startup_gate = Arc::new(RpcStartupGate::default());
        let guard = test_rpc_exit_guard(
            CancellationToken::new(),
            startup_gate,
            health.clone(),
            Arc::clone(&accepting),
            failure.clone(),
        );

        let join_error = tokio::spawn(async move {
            let _exit_guard = guard;
            panic!("simulated RPC listener panic");
        })
        .await
        .expect_err("simulated RPC task should panic");

        assert!(join_error.is_panic());
        assert!(!health.is_ready());
        assert!(!accepting.load(Ordering::Acquire));
        assert!(failure.is_cancelled());
    }

    #[tokio::test]
    async fn successful_startup_and_expected_rpc_exit_do_not_report_a_failure() {
        let health = started_health();
        let accepting = Arc::new(AtomicBool::new(false));
        let failure = CancellationToken::new();
        let expected_exit = CancellationToken::new();
        let startup_gate = Arc::new(RpcStartupGate::default());
        let guard = test_rpc_exit_guard(
            expected_exit.clone(),
            Arc::clone(&startup_gate),
            health.clone(),
            Arc::clone(&accepting),
            failure.clone(),
        );
        startup_gate
            .while_running(&failure, || {
                accepting.store(true, Ordering::Release);
                Ok(())
            })
            .expect("open simulated public listener");
        startup_gate
            .commit_ready(&failure, || health.set_ready(true))
            .expect("commit simulated application readiness");
        startup_gate.expect_exit(&expected_exit);

        tokio::spawn(async move {
            let _exit_guard = guard;
        })
        .await
        .expect("join simulated RPC task");

        assert!(health.is_ready());
        assert!(accepting.load(Ordering::Acquire));
        assert!(!failure.is_cancelled());
    }

    #[tokio::test]
    async fn expected_rpc_exit_during_startup_rollback_does_not_report_a_failure() {
        let health = started_health();
        let accepting = Arc::new(AtomicBool::new(false));
        let failure = CancellationToken::new();
        let expected_exit = CancellationToken::new();
        let startup_gate = Arc::new(RpcStartupGate::default());
        let guard = test_rpc_exit_guard(
            expected_exit.clone(),
            Arc::clone(&startup_gate),
            health.clone(),
            Arc::clone(&accepting),
            failure.clone(),
        );
        startup_gate.expect_exit(&expected_exit);

        tokio::spawn(async move {
            let _exit_guard = guard;
        })
        .await
        .expect("join simulated RPC task");

        assert!(!health.is_ready());
        assert!(!accepting.load(Ordering::Acquire));
        assert!(!failure.is_cancelled());
    }
}
