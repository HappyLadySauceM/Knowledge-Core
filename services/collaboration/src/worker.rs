use std::{
    future::Future,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use async_nats::jetstream::{
    self,
    consumer::{self, AckPolicy, DeliverPolicy, pull},
    message::AckKind,
    stream::{self, DiscardPolicy, RetentionPolicy, StorageType},
};
use async_trait::async_trait;
use bytes::Bytes;
use futures_util::StreamExt as _;
use opentelemetry::{Context as OpenTelemetryContext, global, propagation::Extractor};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use tokio::{sync::Mutex, task::AbortHandle};
use tokio_util::{sync::CancellationToken, task::TaskTracker};
use tracing::Instrument as _;
use tracing_opentelemetry::OpenTelemetrySpanExt as _;

use crate::{
    actor::{ActorRegistry, CLOSE_DOCUMENT_INVALIDATED},
    admin::HealthState,
    config::{MAX_TICKET_TTL_MS, NatsConfig, WorkerConfig},
    domain::{DocumentId, RequestContext},
    error::{Result, ServiceError},
    ports::KnowledgePort,
    richtext,
    storage::{DocumentEvent, OutboxEvent, ProjectionJob, WorkerStore},
    telemetry::Metrics,
};

const NATS_RECONNECT_ATTEMPTS: usize = 10;
const HEX_DIGITS: &[u8; 16] = b"0123456789abcdef";
const STREAM_MAX_AGE: Duration = Duration::from_hours(24);
const STREAM_DUPLICATE_WINDOW: Duration = STREAM_MAX_AGE;
const DOCUMENT_STREAM_MAX_BYTES: i64 = 1_073_741_824;
const PERMISSION_STREAM_MAX_BYTES: i64 = -1;
const STREAM_MAX_MESSAGE_SIZE: i32 = 1_048_576;
const PERMISSION_REPLAY_MINIMUM: Duration = Duration::from_millis(MAX_TICKET_TTL_MS);
const PERMISSION_REPLAY_POLL_INTERVAL: Duration = Duration::from_millis(10);
const _: () = assert!(STREAM_MAX_AGE.as_millis() >= PERMISSION_REPLAY_MINIMUM.as_millis());

#[async_trait]
pub trait EventPublisher: Send + Sync {
    async fn publish(&self, subject: &str, payload: Vec<u8>) -> Result<()>;

    async fn publish_with_headers(
        &self,
        subject: &str,
        payload: Vec<u8>,
        headers: &std::collections::BTreeMap<String, String>,
    ) -> Result<()> {
        let _ = headers;
        self.publish(subject, payload).await
    }

    async fn ping(&self) -> Result<()>;
}

#[derive(Clone)]
pub struct NatsClient {
    client: async_nats::Client,
    jetstream: jetstream::Context,
    document_stream: Arc<str>,
    document_stream_contract: Arc<StreamContract>,
    permission_stream: Arc<str>,
    permission_stream_contract: Arc<StreamContract>,
    consumer_prefix: Arc<str>,
    operation_timeout: Duration,
}

#[derive(Clone, Debug)]
struct StreamContract {
    subjects: Vec<String>,
    max_bytes: i64,
    description: &'static str,
}

#[derive(Clone, Debug)]
struct StreamContracts {
    documents: StreamContract,
    permissions: StreamContract,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum StreamKind {
    Documents,
    Permissions,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ConsumerStartPolicy {
    New,
    AllRetained,
}

impl ConsumerStartPolicy {
    const fn deliver_policy(self) -> DeliverPolicy {
        match self {
            Self::New => DeliverPolicy::New,
            Self::AllRetained => DeliverPolicy::All,
        }
    }
}

struct NatsSubscription {
    messages: pull::Stream,
    consumer: consumer::PullConsumer,
    startup_target: u64,
}

impl StreamContracts {
    fn from_config(config: &NatsConfig) -> Result<Self> {
        config.validate_protocol_contract()?;
        let documents = StreamContract::new(
            vec![
                config.update_subject.clone(),
                config.invalidation_subject.clone(),
            ],
            DOCUMENT_STREAM_MAX_BYTES,
            "Knowledge Core Collaboration document events",
        )?;
        let permissions = StreamContract::new(
            vec![config.permission_subject.clone()],
            PERMISSION_STREAM_MAX_BYTES,
            "Knowledge Core Collaboration permission events",
        )?;
        if documents
            .subjects
            .iter()
            .any(|subject| permissions.subjects.contains(subject))
        {
            return Err(ServiceError::invalid_input(
                "NATS document and permission stream subjects must not overlap",
            ));
        }
        Ok(Self {
            documents,
            permissions,
        })
    }
}

impl StreamContract {
    fn new(mut subjects: Vec<String>, max_bytes: i64, description: &'static str) -> Result<Self> {
        if subjects
            .iter()
            .any(|subject| subject.is_empty() || subject.trim() != subject)
        {
            return Err(ServiceError::invalid_input(
                "NATS JetStream subjects must be non-empty and trimmed",
            ));
        }
        subjects.sort_unstable();
        subjects.dedup();
        Ok(Self {
            subjects,
            max_bytes,
            description,
        })
    }

    fn config(&self, name: &str) -> stream::Config {
        stream::Config {
            name: name.to_owned(),
            description: Some(self.description.to_owned()),
            subjects: self.subjects.clone(),
            retention: RetentionPolicy::Limits,
            discard: DiscardPolicy::Old,
            storage: StorageType::File,
            max_age: STREAM_MAX_AGE,
            duplicate_window: STREAM_DUPLICATE_WINDOW,
            max_bytes: self.max_bytes,
            max_message_size: STREAM_MAX_MESSAGE_SIZE,
            ..Default::default()
        }
    }

    fn validate(&self, name: &str, actual: &stream::Config) -> Result<()> {
        let mut subjects = actual.subjects.clone();
        subjects.sort_unstable();
        subjects.dedup();
        if actual.name != name
            || subjects != self.subjects
            || actual.retention != RetentionPolicy::Limits
            || actual.discard != DiscardPolicy::Old
            || actual.storage != StorageType::File
            || actual.max_age != STREAM_MAX_AGE
            || actual.duplicate_window != STREAM_DUPLICATE_WINDOW
            || actual.max_bytes != self.max_bytes
            || actual.max_message_size != STREAM_MAX_MESSAGE_SIZE
        {
            return Err(ServiceError::conflict(
                "NATS JetStream stream configuration does not match the declared contract",
            ));
        }
        Ok(())
    }
}

impl NatsClient {
    /// Connects to NATS and verifies the configured `JetStream` stream.
    ///
    /// # Errors
    ///
    /// Returns an error when configuration is invalid or NATS cannot be reached and verified.
    pub async fn connect(config: &NatsConfig, instance_id: &str) -> Result<Self> {
        let StreamContracts {
            documents,
            permissions,
        } = StreamContracts::from_config(config)?;
        let mut options = async_nats::ConnectOptions::new()
            .name(&config.name)
            .no_echo()
            .connection_timeout(config.connect_timeout)
            .max_reconnects(Some(NATS_RECONNECT_ATTEMPTS));
        if let Some(token) = &config.token {
            options = options.token(token.clone());
        }
        if let (Some(username), Some(password)) = (&config.username, &config.password) {
            options = options.user_and_password(username.clone(), password.clone());
        }
        if config.tls.enabled {
            options = options.require_tls(true);
            if let Some(ca_file) = &config.tls.ca_file {
                options = options.add_root_certificates(ca_file.clone());
            }
            if let (Some(cert_file), Some(key_file)) = (&config.tls.cert_file, &config.tls.key_file)
            {
                options = options.add_client_certificate(cert_file.clone(), key_file.clone());
            }
        }
        let servers = config
            .servers
            .iter()
            .map(|server| server.parse::<async_nats::ServerAddr>())
            .collect::<std::result::Result<Vec<_>, _>>()
            .map_err(|error| {
                ServiceError::invalid_input("COLLABORATION_NATS_SERVERS is invalid")
                    .with_source(error)
            })?;
        let client = tokio::time::timeout(config.connect_timeout, options.connect(servers))
            .await
            .map_err(|_| dependency_timeout("connect NATS"))?
            .map_err(|error| dependency_error(error, "connect NATS"))?;
        let jetstream = jetstream::new(client.clone());
        let result = Self {
            client,
            jetstream,
            document_stream: Arc::from(config.stream.as_str()),
            document_stream_contract: Arc::new(documents),
            permission_stream: Arc::from(config.permission_stream.as_str()),
            permission_stream_contract: Arc::new(permissions),
            consumer_prefix: Arc::from(consumer_prefix(instance_id)?.as_str()),
            operation_timeout: config.operation_timeout,
        };
        result.ensure_stream(StreamKind::Documents).await?;
        result.ensure_stream(StreamKind::Permissions).await?;
        result.ping().await?;
        Ok(result)
    }

    fn stream_binding(&self, kind: StreamKind) -> (&str, &StreamContract) {
        match kind {
            StreamKind::Documents => (
                self.document_stream.as_ref(),
                self.document_stream_contract.as_ref(),
            ),
            StreamKind::Permissions => (
                self.permission_stream.as_ref(),
                self.permission_stream_contract.as_ref(),
            ),
        }
    }

    async fn ensure_stream(&self, kind: StreamKind) -> Result<()> {
        let (name, contract) = self.stream_binding(kind);
        let stream = tokio::time::timeout(self.operation_timeout, async {
            let create_error = match self
                .jetstream
                .get_or_create_stream(contract.config(name))
                .await
            {
                Ok(stream) => return Ok(stream),
                Err(error) => error,
            };
            // Multiple replicas may observe an absent stream concurrently. If another replica
            // won creation, opening and validating that stream is the successful outcome.
            self.jetstream
                .get_stream(name)
                .await
                .map_err(|_| create_error)
        })
        .await
        .map_err(|_| dependency_timeout("create or open NATS JetStream stream"))?
        .map_err(|error| dependency_error(error, "create or open NATS JetStream stream"))?;
        contract.validate(name, &stream.cached_info().config)
    }

    async fn verified_stream(
        &self,
        kind: StreamKind,
        operation: &'static str,
    ) -> Result<stream::Stream> {
        let (name, contract) = self.stream_binding(kind);
        let stream = tokio::time::timeout(self.operation_timeout, self.jetstream.get_stream(name))
            .await
            .map_err(|_| dependency_timeout(operation))?
            .map_err(|error| dependency_error(error, operation))?;
        contract.validate(name, &stream.cached_info().config)?;
        Ok(stream)
    }

    /// Opens a durable, per-instance pull consumer for one bounded subject.
    ///
    /// # Errors
    ///
    /// Returns an error when the consumer inputs are invalid or its server-side configuration
    /// cannot be created or verified.
    pub async fn subscribe(
        &self,
        consumer_role: &str,
        subject: &str,
        handler_timeout: Duration,
    ) -> Result<pull::Stream> {
        Ok(self
            .open_subscription(
                consumer_role,
                subject,
                handler_timeout,
                ConsumerStartPolicy::New,
                StreamKind::Documents,
            )
            .await?
            .messages)
    }

    async fn subscribe_permissions(
        &self,
        subject: &str,
        handler_timeout: Duration,
    ) -> Result<NatsSubscription> {
        self.open_subscription(
            "permissions",
            subject,
            handler_timeout,
            ConsumerStartPolicy::AllRetained,
            StreamKind::Permissions,
        )
        .await
    }

    async fn open_subscription(
        &self,
        consumer_role: &str,
        subject: &str,
        handler_timeout: Duration,
        start_policy: ConsumerStartPolicy,
        stream_kind: StreamKind,
    ) -> Result<NatsSubscription> {
        validate_consumer_role(consumer_role)?;
        if subject.trim() != subject || subject.is_empty() || handler_timeout.is_zero() {
            return Err(ServiceError::invalid_input(
                "NATS consumer subject and timeout are invalid",
            ));
        }
        let durable_name = format!("{}-{consumer_role}", self.consumer_prefix);
        let ack_wait = handler_timeout
            .checked_mul(2)
            .unwrap_or(Duration::from_mins(5))
            .max(Duration::from_secs(5));
        let expected = consumer_config(
            &durable_name,
            consumer_role,
            subject,
            ack_wait,
            start_policy,
        );
        let stream = self
            .verified_stream(stream_kind, "open NATS JetStream stream")
            .await?;
        let consumer = tokio::time::timeout(
            self.operation_timeout,
            stream.get_or_create_consumer(&durable_name, expected.clone()),
        )
        .await
        .map_err(|_| dependency_timeout("create NATS JetStream consumer"))?
        .map_err(|error| dependency_error(error, "create NATS JetStream consumer"))?;
        validate_consumer_config(&expected, &consumer.cached_info().config)?;
        let startup_target = if stream_kind == StreamKind::Permissions {
            let info = tokio::time::timeout(self.operation_timeout, stream.get_info())
                .await
                .map_err(|_| dependency_timeout("snapshot NATS permission stream"))?
                .map_err(|error| dependency_error(error, "snapshot NATS permission stream"))?;
            let (name, contract) = self.stream_binding(StreamKind::Permissions);
            contract.validate(name, &info.config)?;
            info.state.last_sequence
        } else {
            0
        };
        let startup_consumer = consumer.clone();
        let messages = tokio::time::timeout(self.operation_timeout, consumer.messages())
            .await
            .map_err(|_| dependency_timeout("start NATS JetStream consumer"))?
            .map_err(|error| dependency_error(error, "start NATS JetStream consumer"))?;
        Ok(NatsSubscription {
            messages,
            consumer: startup_consumer,
            startup_target,
        })
    }

    /// Drains the NATS connection within the supplied shutdown budget.
    ///
    /// # Errors
    ///
    /// Returns an error when the connection cannot drain before the deadline.
    pub async fn shutdown(&self, maximum_wait: Duration) -> Result<()> {
        tokio::time::timeout(maximum_wait, self.client.drain())
            .await
            .map_err(|_| dependency_timeout("drain NATS"))?
            .map_err(|error| dependency_error(error, "drain NATS"))
    }
}

fn consumer_config(
    durable_name: &str,
    consumer_role: &str,
    subject: &str,
    ack_wait: Duration,
    start_policy: ConsumerStartPolicy,
) -> pull::Config {
    pull::Config {
        durable_name: Some(durable_name.to_owned()),
        name: Some(durable_name.to_owned()),
        description: Some(format!(
            "Knowledge Core Collaboration {consumer_role} fanout"
        )),
        deliver_policy: start_policy.deliver_policy(),
        ack_policy: AckPolicy::Explicit,
        ack_wait,
        filter_subject: subject.to_owned(),
        max_ack_pending: 256,
        max_deliver: 8,
        ..Default::default()
    }
}

fn validate_consumer_config(expected: &pull::Config, actual: &consumer::Config) -> Result<()> {
    if actual.deliver_subject.is_some()
        || actual.durable_name != expected.durable_name
        || actual.filter_subject != expected.filter_subject
        || actual.deliver_policy != expected.deliver_policy
        || actual.ack_policy != expected.ack_policy
        || actual.ack_wait != expected.ack_wait
        || actual.max_ack_pending != expected.max_ack_pending
        || actual.max_deliver != expected.max_deliver
    {
        return Err(ServiceError::conflict(
            "NATS JetStream consumer configuration does not match",
        ));
    }
    Ok(())
}

#[async_trait]
impl EventPublisher for NatsClient {
    async fn publish(&self, subject: &str, payload: Vec<u8>) -> Result<()> {
        self.publish_with_headers(subject, payload, &std::collections::BTreeMap::new())
            .await
    }

    async fn publish_with_headers(
        &self,
        subject: &str,
        payload: Vec<u8>,
        headers: &std::collections::BTreeMap<String, String>,
    ) -> Result<()> {
        tokio::time::timeout(self.operation_timeout, async {
            let mut nats_headers = async_nats::HeaderMap::new();
            for (key, value) in headers {
                if key.eq_ignore_ascii_case("baggage") && value.len() > 8_192 {
                    continue;
                }
                nats_headers.insert(key.as_str(), value.as_str());
            }
            let acknowledgement = self
                .jetstream
                .publish_with_headers(subject.to_owned(), nats_headers, Bytes::from(payload))
                .await
                .map_err(|error| dependency_error(error, "publish NATS JetStream event"))?
                .await
                .map_err(|error| dependency_error(error, "acknowledge NATS JetStream publish"))?;
            if acknowledgement.stream != self.document_stream.as_ref()
                && acknowledgement.stream != self.permission_stream.as_ref()
            {
                return Err(ServiceError::unavailable(anyhow::anyhow!(
                    "NATS acknowledged the event from an unexpected stream"
                )));
            }
            Ok(())
        })
        .await
        .map_err(|_| dependency_timeout("publish NATS JetStream event"))?
    }

    async fn ping(&self) -> Result<()> {
        self.verified_stream(StreamKind::Documents, "ping NATS document JetStream")
            .await?;
        self.verified_stream(StreamKind::Permissions, "ping NATS permission JetStream")
            .await?;
        tokio::time::timeout(self.operation_timeout, self.client.flush())
            .await
            .map_err(|_| dependency_timeout("flush NATS connection"))?
            .map_err(|error| dependency_error(error, "flush NATS connection"))
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct ReplayWatermark {
    target_stream_sequence: u64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct ReplayProgress {
    ack_floor_stream_sequence: u64,
    num_pending: u64,
    num_ack_pending: usize,
}

impl ReplayWatermark {
    const fn is_complete(self, progress: ReplayProgress) -> bool {
        progress.ack_floor_stream_sequence >= self.target_stream_sequence
            || (progress.num_pending == 0 && progress.num_ack_pending == 0)
    }
}

impl From<&consumer::Info> for ReplayProgress {
    fn from(info: &consumer::Info) -> Self {
        Self {
            ack_floor_stream_sequence: info.ack_floor.stream_sequence,
            num_pending: info.num_pending,
            num_ack_pending: info.num_ack_pending,
        }
    }
}

#[derive(Clone)]
struct StartupReplay {
    consumer: consumer::PullConsumer,
    watermark: ReplayWatermark,
    failed: Arc<AtomicBool>,
}

impl StartupReplay {
    fn new(consumer: consumer::PullConsumer, target_stream_sequence: u64) -> Self {
        Self {
            consumer,
            watermark: ReplayWatermark {
                target_stream_sequence,
            },
            failed: Arc::new(AtomicBool::new(false)),
        }
    }

    fn failed(&self) {
        self.failed.store(true, Ordering::Release);
    }

    async fn wait(&self, maximum_wait: Duration) -> Result<()> {
        tokio::time::timeout(maximum_wait, async {
            loop {
                if self.failed.load(Ordering::Acquire) {
                    return Err(ServiceError::unavailable(anyhow::anyhow!(
                        "NATS permission replay failed"
                    )));
                }
                let info =
                    self.consumer.get_info().await.map_err(|error| {
                        dependency_error(error, "inspect NATS permission replay")
                    })?;
                if self.failed.load(Ordering::Acquire) {
                    return Err(ServiceError::unavailable(anyhow::anyhow!(
                        "NATS permission replay failed"
                    )));
                }
                if self.watermark.is_complete(ReplayProgress::from(&info)) {
                    return Ok(());
                }
                tokio::time::sleep(PERMISSION_REPLAY_POLL_INTERVAL).await;
            }
        })
        .await
        .map_err(|_| dependency_timeout("replay NATS permission events"))?
    }
}

pub struct WorkerRuntime {
    cancellation: CancellationToken,
    tracker: TaskTracker,
    abort_handles: Vec<AbortHandle>,
    healthy: Arc<AtomicBool>,
    permission_replay: StartupReplay,
    operation_timeout: Duration,
    nats: Arc<NatsClient>,
    stopped: Mutex<bool>,
}

impl WorkerRuntime {
    #[allow(clippy::too_many_arguments)]
    #[allow(clippy::too_many_lines)]
    /// Starts all persistence, outbox, and `JetStream` subscription workers.
    ///
    /// # Errors
    ///
    /// Returns an error when a required `JetStream` consumer cannot be created and verified.
    pub async fn start(
        config: &WorkerConfig,
        nats_config: &NatsConfig,
        store: Arc<dyn WorkerStore>,
        knowledge: Arc<dyn KnowledgePort>,
        nats: Arc<NatsClient>,
        actors: ActorRegistry,
        metrics: Metrics,
        health: HealthState,
        parent_cancellation: &CancellationToken,
    ) -> Result<Self> {
        let update_subscription = nats
            .subscribe(
                "updates",
                &nats_config.update_subject,
                config.operation_timeout,
            )
            .await?;
        let invalidation_subscription = nats
            .subscribe(
                "invalidations",
                &nats_config.invalidation_subject,
                config.operation_timeout,
            )
            .await?;
        let permission_subscription = nats
            .subscribe_permissions(&nats_config.permission_subject, config.operation_timeout)
            .await?;
        let permission_replay = StartupReplay::new(
            permission_subscription.consumer,
            permission_subscription.startup_target,
        );
        let cancellation = parent_cancellation.child_token();
        let tracker = TaskTracker::new();
        let mut abort_handles = Vec::with_capacity(7);
        let healthy = Arc::new(AtomicBool::new(true));

        spawn_worker(
            &tracker,
            &mut abort_handles,
            projection_loop(
                Arc::clone(&store),
                Arc::clone(&knowledge),
                config.clone(),
                metrics.clone(),
                cancellation.child_token(),
            ),
        );
        spawn_worker(
            &tracker,
            &mut abort_handles,
            maintenance_loop(
                Arc::clone(&store),
                config.clone(),
                metrics.clone(),
                cancellation.child_token(),
            ),
        );
        spawn_worker(
            &tracker,
            &mut abort_handles,
            cleanup_loop(
                Arc::clone(&store),
                config.clone(),
                metrics.clone(),
                cancellation.child_token(),
            ),
        );
        spawn_worker(
            &tracker,
            &mut abort_handles,
            outbox_loop(
                store,
                Arc::clone(&nats) as Arc<dyn EventPublisher>,
                config.clone(),
                metrics.clone(),
                cancellation.child_token(),
            ),
        );
        spawn_worker(
            &tracker,
            &mut abort_handles,
            subscription_loop(
                SubscriptionKind::Update,
                update_subscription,
                actors.clone(),
                Arc::clone(&healthy),
                health.clone(),
                metrics.clone(),
                config.operation_timeout,
                cancellation.child_token(),
                None,
            ),
        );
        spawn_worker(
            &tracker,
            &mut abort_handles,
            subscription_loop(
                SubscriptionKind::Invalidation,
                invalidation_subscription,
                actors.clone(),
                Arc::clone(&healthy),
                health.clone(),
                metrics.clone(),
                config.operation_timeout,
                cancellation.child_token(),
                None,
            ),
        );
        spawn_worker(
            &tracker,
            &mut abort_handles,
            subscription_loop(
                SubscriptionKind::Permission,
                permission_subscription.messages,
                actors,
                Arc::clone(&healthy),
                health,
                metrics,
                config.operation_timeout,
                cancellation.child_token(),
                Some(permission_replay.clone()),
            ),
        );

        Ok(Self {
            cancellation,
            tracker,
            abort_handles,
            healthy,
            permission_replay,
            operation_timeout: config.operation_timeout,
            nats,
            stopped: Mutex::new(false),
        })
    }

    /// Verifies that every required subscription is running and NATS remains reachable.
    ///
    /// # Errors
    ///
    /// Returns an unavailable error when a subscription stopped or NATS is not ready.
    pub async fn ready(&self) -> Result<()> {
        if !self.healthy.load(Ordering::Acquire) {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "a required NATS subscription is not running"
            )));
        }
        self.permission_replay.wait(self.operation_timeout).await?;
        if !self.healthy.load(Ordering::Acquire) {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "a required NATS subscription is not running"
            )));
        }
        self.nats.ping().await
    }

    /// Stops workers, aborts any task that exceeds the deadline, and drains NATS.
    ///
    /// # Errors
    ///
    /// Returns an error when worker cancellation or NATS draining exceeds the shutdown budget.
    pub async fn shutdown(&self, maximum_wait: Duration) -> Result<()> {
        let mut stopped = self.stopped.lock().await;
        if *stopped {
            return Ok(());
        }
        *stopped = true;
        self.cancellation.cancel();
        self.tracker.close();
        let deadline = Instant::now() + maximum_wait;
        let worker_result = if tokio::time::timeout(maximum_wait, self.tracker.wait())
            .await
            .is_ok()
        {
            Ok(())
        } else {
            for handle in &self.abort_handles {
                handle.abort();
            }
            self.tracker.wait().await;
            Err(ServiceError::internal(anyhow::anyhow!(
                "Collaboration workers did not stop before the shutdown deadline"
            )))
        };
        let nats_result = self.nats.shutdown(remaining(deadline)).await;
        worker_result.and(nats_result)
    }
}

fn spawn_worker<F>(tracker: &TaskTracker, abort_handles: &mut Vec<AbortHandle>, future: F)
where
    F: Future<Output = ()> + Send + 'static,
{
    let task = tracker.spawn(future);
    abort_handles.push(task.abort_handle());
}

async fn projection_loop(
    store: Arc<dyn WorkerStore>,
    knowledge: Arc<dyn KnowledgePort>,
    config: WorkerConfig,
    metrics: Metrics,
    cancellation: CancellationToken,
) {
    let mut interval = tokio::time::interval(config.poll_interval);
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            biased;
            () = cancellation.cancelled() => break,
            _ = interval.tick() => {
                let result = tokio::time::timeout(
                    config.operation_timeout,
                    project_one(store.as_ref(), knowledge.as_ref(), &config),
                ).await;
                metrics.worker_operation("projection", matches!(result, Ok(Ok(()))));
            }
        }
    }
}

async fn project_one(
    store: &dyn WorkerStore,
    knowledge: &dyn KnowledgePort,
    config: &WorkerConfig,
) -> Result<()> {
    let context = operation_context(config.operation_timeout);
    let Some(job) = store
        .claim_projection_job(&context, config.projection_lease)
        .await?
    else {
        return Ok(());
    };
    let result = project_claimed(knowledge, &context, &job).await;
    match result {
        Ok(()) => store.complete_projection(&context, &job).await,
        Err(error) => {
            store.retry_projection(&context, &job, error.key()).await?;
            Err(error)
        }
    }
}

async fn project_claimed(
    knowledge: &dyn KnowledgePort,
    context: &RequestContext,
    job: &ProjectionJob,
) -> Result<()> {
    let projection = richtext::projection_from_state(&job.state)?;
    knowledge
        .project(context, job.document_id, job.sequence, &projection)
        .await
}

async fn maintenance_loop(
    store: Arc<dyn WorkerStore>,
    config: WorkerConfig,
    metrics: Metrics,
    cancellation: CancellationToken,
) {
    let mut interval = tokio::time::interval(config.poll_interval);
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            biased;
            () = cancellation.cancelled() => break,
            _ = interval.tick() => {
                let context = operation_context(config.operation_timeout);
                let compacted = tokio::time::timeout(
                    config.operation_timeout,
                    store.compact_next(
                        &context,
                        config.snapshot_update_threshold,
                        config.snapshot_byte_threshold,
                    ),
                ).await;
                metrics.worker_operation("compaction", matches!(compacted, Ok(Ok(_))));
                let context = operation_context(config.operation_timeout);
                let versioned = tokio::time::timeout(
                    config.operation_timeout,
                    store.create_automatic_version(&context, config.automatic_version_interval),
                ).await;
                metrics.worker_operation("automatic_version", matches!(versioned, Ok(Ok(_))));
            }
        }
    }
}

async fn cleanup_loop(
    store: Arc<dyn WorkerStore>,
    config: WorkerConfig,
    metrics: Metrics,
    cancellation: CancellationToken,
) {
    let cleanup_interval = config
        .poll_interval
        .checked_mul(300)
        .unwrap_or(Duration::from_mins(5))
        .max(Duration::from_mins(1));
    let mut interval = tokio::time::interval(cleanup_interval);
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            biased;
            () = cancellation.cancelled() => break,
            _ = interval.tick() => {
                let context = operation_context(config.operation_timeout);
                let result = tokio::time::timeout(
                    config.operation_timeout,
                    store.cleanup(&context),
                ).await;
                metrics.worker_operation("cleanup", matches!(result, Ok(Ok(()))));
            }
        }
    }
}

async fn outbox_loop(
    store: Arc<dyn WorkerStore>,
    publisher: Arc<dyn EventPublisher>,
    config: WorkerConfig,
    metrics: Metrics,
    cancellation: CancellationToken,
) {
    let mut interval = tokio::time::interval(config.poll_interval);
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            biased;
            () = cancellation.cancelled() => break,
            _ = interval.tick() => {
                let result = tokio::time::timeout(
                    config.operation_timeout,
                    publish_outbox_batch(store.as_ref(), publisher.as_ref(), &config),
                ).await;
                metrics.worker_operation("outbox", matches!(result, Ok(Ok(()))));
            }
        }
    }
}

async fn publish_outbox_batch(
    store: &dyn WorkerStore,
    publisher: &dyn EventPublisher,
    config: &WorkerConfig,
) -> Result<()> {
    let context = operation_context(config.operation_timeout);
    let events = store
        .claim_outbox(&context, config.outbox_batch_size, config.projection_lease)
        .await?;
    for event in events {
        if let Err(error) = publish_outbox_event(publisher, &event).await {
            store.retry_outbox(&context, &event, error.key()).await?;
            continue;
        }
        store.complete_outbox(&context, event.id).await?;
    }
    Ok(())
}

async fn publish_outbox_event(publisher: &dyn EventPublisher, event: &OutboxEvent) -> Result<()> {
    let payload = serde_json::to_vec(&event.payload).map_err(|error| {
        ServiceError::internal(anyhow::anyhow!(error).context("encode outbox payload"))
    })?;
    let mut headers = event.trace_headers.clone();
    headers.insert("X-Message-ID".to_owned(), event.id.to_string());
    headers.insert("X-Message-Type".to_owned(), event.subject.clone());
    headers.insert("X-Event-Key".to_owned(), event.event_key.clone());
    let span = tracing::info_span!(
        "collaboration.nats.publish",
        messaging.system = "nats",
        messaging.destination = %event.subject,
        messaging.message_id = %event.id,
    );
    let parent = global::get_text_map_propagator(|propagator| {
        propagator.extract_with_context(
            &OpenTelemetryContext::new(),
            &StoredHeaderExtractor::new(&event.trace_headers),
        )
    });
    let _ = span.set_parent(parent);
    publisher
        .publish_with_headers(&event.subject, payload, &headers)
        .instrument(span)
        .await
}

#[derive(Clone, Copy)]
enum SubscriptionKind {
    Update,
    Invalidation,
    Permission,
}

#[allow(clippy::too_many_arguments)]
async fn subscription_loop(
    kind: SubscriptionKind,
    mut subscription: pull::Stream,
    actors: ActorRegistry,
    healthy: Arc<AtomicBool>,
    health: HealthState,
    metrics: Metrics,
    operation_timeout: Duration,
    cancellation: CancellationToken,
    startup_replay: Option<StartupReplay>,
) {
    loop {
        tokio::select! {
            biased;
            () = cancellation.cancelled() => break,
            message = subscription.next() => {
                let Some(message) = message else {
                    if let Some(replay) = &startup_replay {
                        replay.failed();
                    }
                    healthy.store(false, Ordering::Release);
                    health.set_ready(false);
                    metrics.worker_operation("nats_subscription", false);
                    break;
                };
                let Ok(message) = message else {
                    if let Some(replay) = &startup_replay {
                        replay.failed();
                    }
                    healthy.store(false, Ordering::Release);
                    health.set_ready(false);
                    metrics.worker_operation("nats_subscription", false);
                    break;
                };
                if message.info().is_ok_and(|info| info.delivered > 1) {
                    metrics.worker_operation("nats_redelivery", true);
                }
                let delivery_attempt = message.info().map_or(1, |info| info.delivered);
                let consume_span = tracing::info_span!(
                    "collaboration.nats.consume",
                    messaging.system = "nats",
                    messaging.destination = %message.message.subject,
                    messaging.delivery_attempt = delivery_attempt,
                );
                let parent = message.message.headers.as_ref().map_or_else(
                    OpenTelemetryContext::new,
                    |headers| {
                        global::get_text_map_propagator(|propagator| {
                            propagator.extract_with_context(
                                &OpenTelemetryContext::new(),
                                &AsyncNatsHeaderExtractor { headers },
                            )
                        })
                    },
                );
                let _ = consume_span.set_parent(parent);
                let handled = tokio::time::timeout(
                    operation_timeout,
                    handle_event(kind, &message.payload, &actors),
                )
                .instrument(consume_span)
                .await;
                if !matches!(handled, Ok(Ok(()))) {
                    if let Some(replay) = &startup_replay {
                        replay.failed();
                    }
                    metrics.worker_operation("nats_event", false);
                    let acknowledgement = if delivery_attempt >= 8 {
                        metrics.worker_operation("nats_event_parked", true);
                        message.double_ack_with(AckKind::Term)
                    } else {
                        let delay = Duration::from_secs(1_u64 << delivery_attempt.min(7));
                        message.double_ack_with(AckKind::Nak(Some(delay)))
                    };
                    if acknowledgement.await.is_err() {
                        healthy.store(false, Ordering::Release);
                        health.set_ready(false);
                        metrics.worker_operation("nats_ack", false);
                        break;
                    }
                    continue;
                }
                let acknowledged = tokio::time::timeout(operation_timeout, message.double_ack()).await;
                if !matches!(acknowledged, Ok(Ok(()))) {
                    if let Some(replay) = &startup_replay {
                        replay.failed();
                    }
                    healthy.store(false, Ordering::Release);
                    health.set_ready(false);
                    metrics.worker_operation("nats_ack", false);
                    break;
                }
                metrics.worker_operation("nats_event", true);
            }
        }
    }
}

struct StoredHeaderExtractor<'a> {
    headers: &'a std::collections::BTreeMap<String, String>,
}

impl<'a> StoredHeaderExtractor<'a> {
    const fn new(headers: &'a std::collections::BTreeMap<String, String>) -> Self {
        Self { headers }
    }
}

impl Extractor for StoredHeaderExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.headers
            .iter()
            .find(|(name, _)| name.eq_ignore_ascii_case(key))
            .map(|(_, value)| value.as_str())
    }

    fn keys(&self) -> Vec<&str> {
        self.headers.keys().map(String::as_str).collect()
    }
}

struct AsyncNatsHeaderExtractor<'a> {
    headers: &'a async_nats::HeaderMap,
}

impl Extractor for AsyncNatsHeaderExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.headers.get(key).map(async_nats::HeaderValue::as_str)
    }

    fn keys(&self) -> Vec<&str> {
        vec!["traceparent", "tracestate", "baggage"]
    }
}

async fn handle_event(
    kind: SubscriptionKind,
    payload: &[u8],
    actors: &ActorRegistry,
) -> Result<()> {
    match kind {
        SubscriptionKind::Update => {
            let event: DocumentEvent = serde_json::from_slice(payload).map_err(|error| {
                ServiceError::invalid_input("NATS update event is invalid").with_source(error)
            })?;
            actors
                .apply_remote(event.document_id, event.generation, event.sequence)
                .await
        }
        SubscriptionKind::Invalidation => {
            let event: DocumentReference = serde_json::from_slice(payload).map_err(|error| {
                ServiceError::invalid_input("NATS invalidation event is invalid").with_source(error)
            })?;
            actors
                .invalidate(event.document_id, CLOSE_DOCUMENT_INVALIDATED)
                .await
        }
        SubscriptionKind::Permission => {
            let event: PermissionEvent = serde_json::from_slice(payload).map_err(|error| {
                ServiceError::invalid_input("NATS permission event is invalid").with_source(error)
            })?;
            if event.permission_revision <= 0 {
                return Err(ServiceError::invalid_input(
                    "NATS permission event revision must be positive",
                ));
            }
            actors
                .invalidate_permissions(event.document_id, event.permission_revision)
                .await
        }
    }
}

#[derive(Deserialize)]
struct DocumentReference {
    document_id: DocumentId,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PermissionEvent {
    document_id: DocumentId,
    permission_revision: i64,
    #[serde(rename = "deleted")]
    _deleted: bool,
}

fn operation_context(maximum_wait: Duration) -> RequestContext {
    let mut context = RequestContext::new(uuid::Uuid::now_v7().simple().to_string());
    context.deadline = Instant::now().checked_add(maximum_wait);
    context
}

fn remaining(deadline: Instant) -> Duration {
    deadline
        .checked_duration_since(Instant::now())
        .unwrap_or(Duration::ZERO)
}

fn consumer_prefix(instance_id: &str) -> Result<String> {
    if instance_id.trim() != instance_id || instance_id.is_empty() || instance_id.len() > 1_024 {
        return Err(ServiceError::invalid_input(
            "Collaboration instance identifier is invalid for NATS consumers",
        ));
    }
    let digest = Sha256::digest(instance_id.as_bytes());
    let mut suffix = String::with_capacity(24);
    for byte in digest.iter().take(12) {
        suffix.push(char::from(HEX_DIGITS[usize::from(byte >> 4)]));
        suffix.push(char::from(HEX_DIGITS[usize::from(byte & 0x0f)]));
    }
    Ok(format!("kc-collaboration-{suffix}"))
}

fn validate_consumer_role(role: &str) -> Result<()> {
    if role.is_empty()
        || role.len() > 32
        || !role
            .bytes()
            .all(|value| value.is_ascii_alphanumeric() || matches!(value, b'-' | b'_'))
    {
        return Err(ServiceError::invalid_input(
            "NATS JetStream consumer role is invalid",
        ));
    }
    Ok(())
}

fn dependency_timeout(operation: &'static str) -> ServiceError {
    ServiceError::unavailable(anyhow::anyhow!("{operation} timed out"))
}

fn dependency_error(
    error: impl std::error::Error + Send + Sync + 'static,
    operation: &'static str,
) -> ServiceError {
    ServiceError::unavailable(anyhow::Error::new(error).context(operation))
}

#[cfg(test)]
mod tests {
    use std::sync::{Mutex as StdMutex, atomic::AtomicUsize};

    use super::*;
    use crate::{
        actor::{ActorLimits, ActorSession, CLOSE_FORBIDDEN, CloseSignal, ConnectionEvent},
        config::{
            NATS_INVALIDATION_SUBJECT, NATS_PERMISSION_SUBJECT, NATS_UPDATE_SUBJECT, TlsConfig,
        },
        domain::{Access, Authorization, Projection, PublicUser},
        storage::{
            CommittedUpdate, DocumentStore, LoadedDocument, RestorationCandidate, RestoreVersion,
            StoredUpdate, UpdateLimits, WorkerStore,
        },
    };
    use uuid::Uuid;

    type Observation = (&'static str, Arc<str>, Option<Instant>);

    struct ActorStore;

    #[async_trait]
    impl DocumentStore for ActorStore {
        async fn initialize_document(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
        ) -> Result<()> {
            Ok(())
        }

        async fn load_document(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
        ) -> Result<LoadedDocument> {
            Ok(LoadedDocument {
                generation: 0,
                sequence: 0,
                state: richtext::initial_state(),
            })
        }

        async fn append_update(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
            _update: &[u8],
            _actor: &PublicUser,
            _limits: UpdateLimits,
        ) -> Result<CommittedUpdate> {
            unused_operation()
        }

        async fn commit_restoration(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
            _candidate: RestorationCandidate<'_>,
        ) -> Result<RestoreVersion> {
            unused_operation()
        }

        async fn updates_after(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
            _sequence: i64,
            _limit: i64,
        ) -> Result<Vec<StoredUpdate>> {
            unused_operation()
        }

        async fn current_sequence(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
        ) -> Result<i64> {
            unused_operation()
        }
    }

    struct ProjectionStore {
        observations: Arc<StdMutex<Vec<Observation>>>,
    }

    #[async_trait]
    impl WorkerStore for ProjectionStore {
        async fn claim_projection_job(
            &self,
            context: &RequestContext,
            _lease: Duration,
        ) -> Result<Option<ProjectionJob>> {
            observe(&self.observations, "claim", context);
            Ok(Some(ProjectionJob {
                document_id: DocumentId::new(),
                generation: 0,
                sequence: 0,
                state: richtext::initial_state(),
                attempts: 0,
            }))
        }

        async fn complete_projection(
            &self,
            context: &RequestContext,
            _job: &ProjectionJob,
        ) -> Result<()> {
            observe(&self.observations, "complete", context);
            Ok(())
        }

        async fn retry_projection(
            &self,
            _context: &RequestContext,
            _job: &ProjectionJob,
            _error_key: &str,
        ) -> Result<()> {
            unused_operation()
        }

        async fn compact_next(
            &self,
            _context: &RequestContext,
            _update_threshold: i64,
            _byte_threshold: i64,
        ) -> Result<bool> {
            unused_operation()
        }

        async fn create_automatic_version(
            &self,
            _context: &RequestContext,
            _interval: Duration,
        ) -> Result<bool> {
            unused_operation()
        }

        async fn claim_outbox(
            &self,
            _context: &RequestContext,
            _batch_size: i64,
            _lease: Duration,
        ) -> Result<Vec<OutboxEvent>> {
            unused_operation()
        }

        async fn complete_outbox(&self, _context: &RequestContext, _id: Uuid) -> Result<()> {
            unused_operation()
        }

        async fn retry_outbox(
            &self,
            _context: &RequestContext,
            _event: &OutboxEvent,
            _error_key: &str,
        ) -> Result<()> {
            unused_operation()
        }

        async fn cleanup(&self, _context: &RequestContext) -> Result<()> {
            unused_operation()
        }
    }

    struct ProjectionKnowledge {
        observations: Arc<StdMutex<Vec<Observation>>>,
    }

    #[async_trait]
    impl KnowledgePort for ProjectionKnowledge {
        async fn authorize(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
        ) -> Result<Authorization> {
            unused_operation()
        }

        async fn project(
            &self,
            context: &RequestContext,
            _document_id: DocumentId,
            _sequence: i64,
            _projection: &Projection,
        ) -> Result<()> {
            observe(&self.observations, "project", context);
            Ok(())
        }

        async fn ping(&self, _context: &RequestContext) -> Result<()> {
            unused_operation()
        }
    }

    struct OutboxStore {
        event: OutboxEvent,
        observations: Arc<StdMutex<Vec<Observation>>>,
    }

    #[async_trait]
    impl WorkerStore for OutboxStore {
        async fn claim_projection_job(
            &self,
            _context: &RequestContext,
            _lease: Duration,
        ) -> Result<Option<ProjectionJob>> {
            unused_operation()
        }

        async fn complete_projection(
            &self,
            _context: &RequestContext,
            _job: &ProjectionJob,
        ) -> Result<()> {
            unused_operation()
        }

        async fn retry_projection(
            &self,
            _context: &RequestContext,
            _job: &ProjectionJob,
            _error_key: &str,
        ) -> Result<()> {
            unused_operation()
        }

        async fn compact_next(
            &self,
            _context: &RequestContext,
            _update_threshold: i64,
            _byte_threshold: i64,
        ) -> Result<bool> {
            unused_operation()
        }

        async fn create_automatic_version(
            &self,
            _context: &RequestContext,
            _interval: Duration,
        ) -> Result<bool> {
            unused_operation()
        }

        async fn claim_outbox(
            &self,
            context: &RequestContext,
            _batch_size: i64,
            _lease: Duration,
        ) -> Result<Vec<OutboxEvent>> {
            observe(&self.observations, "claim", context);
            Ok(vec![self.event.clone()])
        }

        async fn complete_outbox(&self, context: &RequestContext, id: Uuid) -> Result<()> {
            assert_eq!(id, self.event.id);
            observe(&self.observations, "complete", context);
            Ok(())
        }

        async fn retry_outbox(
            &self,
            context: &RequestContext,
            event: &OutboxEvent,
            error_key: &str,
        ) -> Result<()> {
            assert_eq!(event.id, self.event.id);
            assert_eq!(error_key, "collaboration.unavailable");
            observe(&self.observations, "retry", context);
            Ok(())
        }

        async fn cleanup(&self, _context: &RequestContext) -> Result<()> {
            unused_operation()
        }
    }

    struct FailOncePublisher {
        subject: String,
        payload: serde_json::Value,
        attempts: AtomicUsize,
    }

    #[async_trait]
    impl EventPublisher for FailOncePublisher {
        async fn publish(&self, subject: &str, payload: Vec<u8>) -> Result<()> {
            assert_eq!(subject, self.subject.as_str());
            assert_eq!(
                payload,
                serde_json::to_vec(&self.payload).expect("expected payload")
            );
            if self.attempts.fetch_add(1, Ordering::SeqCst) == 0 {
                return Err(ServiceError::unavailable(anyhow::anyhow!(
                    "injected publish failure"
                )));
            }
            Ok(())
        }

        async fn ping(&self) -> Result<()> {
            unused_operation()
        }
    }

    #[tokio::test]
    async fn permission_event_closes_active_connection_with_4403() {
        assert_event_closes_active_connection(SubscriptionKind::Permission, CLOSE_FORBIDDEN).await;
    }

    #[tokio::test]
    async fn permission_event_rejects_missing_or_non_positive_revision() {
        let actors = ActorRegistry::new(
            Arc::new(ActorStore),
            ActorLimits::for_test(),
            Metrics::new().expect("metrics"),
            CancellationToken::new(),
        );
        let document_id = DocumentId::new();
        for payload in [
            format!(r#"{{"document_id":"{document_id}","deleted":false}}"#),
            format!(r#"{{"document_id":"{document_id}","permission_revision":0,"deleted":false}}"#),
            format!(
                r#"{{"document_id":"{document_id}","permission_revision":-1,"deleted":false}}"#
            ),
        ] {
            let error = handle_event(SubscriptionKind::Permission, payload.as_bytes(), &actors)
                .await
                .expect_err("invalid permission revision");
            assert_eq!(error.code(), crate::error::ErrorCode::InvalidInput);
        }
        actors
            .shutdown(Duration::from_secs(1))
            .await
            .expect("actor registry shutdown");
    }

    #[tokio::test]
    async fn invalidation_event_closes_active_connection_with_4409() {
        assert_event_closes_active_connection(
            SubscriptionKind::Invalidation,
            CLOSE_DOCUMENT_INVALIDATED,
        )
        .await;
    }

    #[tokio::test]
    async fn projection_chain_reuses_one_bounded_request_context() {
        let observations = Arc::new(StdMutex::new(Vec::new()));
        let store = ProjectionStore {
            observations: Arc::clone(&observations),
        };
        let knowledge = ProjectionKnowledge {
            observations: Arc::clone(&observations),
        };
        let config = WorkerConfig {
            poll_interval: Duration::from_millis(10),
            operation_timeout: Duration::from_secs(1),
            projection_lease: Duration::from_secs(5),
            snapshot_update_threshold: 100,
            snapshot_byte_threshold: 1024,
            automatic_version_interval: Duration::from_mins(1),
            outbox_batch_size: 10,
        };

        project_one(&store, &knowledge, &config)
            .await
            .expect("project one document");

        let observations = observations.lock().expect("observation lock");
        assert_eq!(
            observations
                .iter()
                .map(|(operation, _, _)| *operation)
                .collect::<Vec<_>>(),
            ["claim", "project", "complete"]
        );
        let (_, request_id, deadline) = &observations[0];
        assert!(deadline.is_some_and(|value| value > Instant::now()));
        assert!(
            observations
                .iter()
                .all(|(_, observed_id, observed_deadline)| {
                    observed_id == request_id && observed_deadline == deadline
                })
        );
    }

    #[tokio::test]
    async fn outbox_publish_failure_retries_then_completes_after_recovery() {
        let observations = Arc::new(StdMutex::new(Vec::new()));
        let event = OutboxEvent {
            id: Uuid::now_v7(),
            event_key: "document.updated:test".to_owned(),
            subject: NATS_UPDATE_SUBJECT.to_owned(),
            payload: serde_json::json!({"document_id": DocumentId::new()}),
            trace_headers: std::collections::BTreeMap::new(),
            attempts: 0,
        };
        let store = OutboxStore {
            event: event.clone(),
            observations: Arc::clone(&observations),
        };
        let publisher = FailOncePublisher {
            subject: event.subject.clone(),
            payload: event.payload.clone(),
            attempts: AtomicUsize::new(0),
        };
        let config = WorkerConfig {
            poll_interval: Duration::from_millis(10),
            operation_timeout: Duration::from_secs(1),
            projection_lease: Duration::from_secs(5),
            snapshot_update_threshold: 100,
            snapshot_byte_threshold: 1024,
            automatic_version_interval: Duration::from_mins(1),
            outbox_batch_size: 10,
        };

        publish_outbox_batch(&store, &publisher, &config)
            .await
            .expect("persist retry after publish failure");
        {
            let observations = observations.lock().expect("observation lock");
            assert_eq!(
                observations
                    .iter()
                    .map(|(operation, _, _)| *operation)
                    .collect::<Vec<_>>(),
                ["claim", "retry"]
            );
            assert_eq!(observations[0].1, observations[1].1);
            assert_eq!(observations[0].2, observations[1].2);
        }

        publish_outbox_batch(&store, &publisher, &config)
            .await
            .expect("complete outbox event after publish recovery");

        let observations = observations.lock().expect("observation lock");
        assert_eq!(
            observations
                .iter()
                .map(|(operation, _, _)| *operation)
                .collect::<Vec<_>>(),
            ["claim", "retry", "claim", "complete"]
        );
        assert_eq!(observations[2].1, observations[3].1);
        assert_eq!(observations[2].2, observations[3].2);
        assert_ne!(observations[0].1, observations[2].1);
        assert_eq!(publisher.attempts.load(Ordering::SeqCst), 2);
    }

    async fn assert_event_closes_active_connection(kind: SubscriptionKind, expected: CloseSignal) {
        let actors = ActorRegistry::new(
            Arc::new(ActorStore),
            ActorLimits::for_test(),
            Metrics::new().expect("metrics"),
            CancellationToken::new(),
        );
        let document_id = DocumentId::new();
        let mut connection = actors
            .connect(
                &operation_context(Duration::from_secs(1)),
                document_id,
                ActorSession {
                    actor: PublicUser {
                        id: 1,
                        username: "event-test-user".to_owned(),
                        avatar: String::new(),
                    },
                    access: Access::Viewer,
                    permission_revision: 1,
                    expires_at: time::OffsetDateTime::now_utc() + time::Duration::hours(1),
                },
            )
            .await
            .expect("active actor connection");
        assert!(matches!(
            connection.recv().await,
            ConnectionEvent::Binary(_)
        ));

        let payload = match kind {
            SubscriptionKind::Permission => format!(
                r#"{{"document_id":"{document_id}","permission_revision":2,"deleted":false}}"#
            ),
            SubscriptionKind::Invalidation => format!(r#"{{"document_id":"{document_id}"}}"#),
            SubscriptionKind::Update => unreachable!("update is not an invalidation event"),
        };
        handle_event(kind, payload.as_bytes(), &actors)
            .await
            .expect("route parsed event to actor");

        let close = tokio::time::timeout(Duration::from_secs(1), connection.recv())
            .await
            .expect("actor close event");
        assert_eq!(close, ConnectionEvent::Close(expected));
        assert_eq!(
            expected.code,
            match kind {
                SubscriptionKind::Permission => 4403,
                SubscriptionKind::Invalidation => 4409,
                SubscriptionKind::Update => unreachable!("update is not an invalidation event"),
            }
        );
        actors
            .shutdown(Duration::from_secs(1))
            .await
            .expect("actor registry shutdown");
    }

    #[test]
    fn jetstream_contracts_separate_permission_retention_from_document_capacity() {
        let config = nats_config();
        let contracts = StreamContracts::from_config(&config).expect("stream contracts");
        let documents = contracts.documents.config(&config.stream);
        let permissions = contracts.permissions.config(&config.permission_stream);

        assert_eq!(
            documents.subjects,
            vec![
                NATS_INVALIDATION_SUBJECT.to_owned(),
                NATS_UPDATE_SUBJECT.to_owned(),
            ]
        );
        assert_eq!(documents.max_bytes, DOCUMENT_STREAM_MAX_BYTES);
        assert_eq!(
            permissions.subjects,
            vec![NATS_PERMISSION_SUBJECT.to_owned()]
        );
        assert_eq!(permissions.max_bytes, PERMISSION_STREAM_MAX_BYTES);
        assert!(
            documents
                .subjects
                .iter()
                .all(|subject| !permissions.subjects.contains(subject))
        );
        assert_eq!(documents.duplicate_window, STREAM_DUPLICATE_WINDOW);
        assert_eq!(permissions.duplicate_window, STREAM_DUPLICATE_WINDOW);

        assert_stream_contract_rejects_drift(&contracts.documents, &config.stream, documents);
        assert_stream_contract_rejects_drift(
            &contracts.permissions,
            &config.permission_stream,
            permissions,
        );

        for (index, mut drifted) in [config.clone(), config.clone(), config]
            .into_iter()
            .enumerate()
        {
            match index {
                0 => drifted.update_subject = "drifted.documents.updated".to_owned(),
                1 => {
                    drifted.invalidation_subject = "drifted.documents.invalidated".to_owned();
                }
                2 => drifted.permission_subject = "drifted.permissions.changed".to_owned(),
                _ => unreachable!("fixed subject index"),
            }
            assert!(StreamContracts::from_config(&drifted).is_err());
        }

        let mut same_stream = nats_config();
        same_stream
            .permission_stream
            .clone_from(&same_stream.stream);
        assert!(StreamContracts::from_config(&same_stream).is_err());
    }

    #[test]
    fn permission_consumer_replays_all_time_retained_history_without_changing_other_consumers() {
        assert!(STREAM_MAX_AGE >= PERMISSION_REPLAY_MINIMUM);

        for (role, subject) in [
            ("updates", NATS_UPDATE_SUBJECT),
            ("invalidations", NATS_INVALIDATION_SUBJECT),
        ] {
            let expected = consumer_config(
                &format!("consumer-{role}"),
                role,
                subject,
                Duration::from_secs(5),
                ConsumerStartPolicy::New,
            );
            assert_eq!(expected.deliver_policy, DeliverPolicy::New);
            let mut actual = actual_consumer_config(&expected);
            validate_consumer_config(&expected, &actual).expect("new-event consumer contract");
            actual.deliver_policy = DeliverPolicy::All;
            assert!(validate_consumer_config(&expected, &actual).is_err());
        }

        let expected = consumer_config(
            "consumer-permissions",
            "permissions",
            NATS_PERMISSION_SUBJECT,
            Duration::from_secs(5),
            ConsumerStartPolicy::AllRetained,
        );
        assert_eq!(expected.deliver_policy, DeliverPolicy::All);
        let mut actual = actual_consumer_config(&expected);
        validate_consumer_config(&expected, &actual).expect("permission replay contract");
        actual.deliver_policy = DeliverPolicy::New;
        assert!(validate_consumer_config(&expected, &actual).is_err());
        actual.deliver_policy = DeliverPolicy::ByStartSequence { start_sequence: 1 };
        assert!(validate_consumer_config(&expected, &actual).is_err());
    }

    #[test]
    fn permission_replay_uses_stream_sequence_and_handles_retention_shrink() {
        let watermark = ReplayWatermark {
            target_stream_sequence: 12,
        };
        assert!(!watermark.is_complete(ReplayProgress {
            ack_floor_stream_sequence: 11,
            num_pending: 1,
            num_ack_pending: 0,
        }));
        assert!(!watermark.is_complete(ReplayProgress {
            ack_floor_stream_sequence: 11,
            num_pending: 0,
            num_ack_pending: 1,
        }));
        assert!(watermark.is_complete(ReplayProgress {
            ack_floor_stream_sequence: 12,
            num_pending: 3,
            num_ack_pending: 1,
        }));
        assert!(watermark.is_complete(ReplayProgress {
            ack_floor_stream_sequence: 4,
            num_pending: 0,
            num_ack_pending: 0,
        }));
    }

    fn assert_stream_contract_rejects_drift(
        contract: &StreamContract,
        name: &str,
        expected: stream::Config,
    ) {
        contract
            .validate(name, &expected)
            .expect("declared stream config");

        let mut actual = expected.clone();
        actual.max_age = Duration::ZERO;
        assert!(contract.validate(name, &actual).is_err());
        let mut actual = expected.clone();
        actual.duplicate_window = Duration::from_secs(1);
        assert!(contract.validate(name, &actual).is_err());
        let mut actual = expected.clone();
        actual.max_bytes = if expected.max_bytes == -1 { 1 } else { -1 };
        assert!(contract.validate(name, &actual).is_err());
        let mut actual = expected.clone();
        actual.max_message_size = -1;
        assert!(contract.validate(name, &actual).is_err());
        let mut actual = expected.clone();
        actual.retention = RetentionPolicy::Interest;
        assert!(contract.validate(name, &actual).is_err());
        let mut actual = expected.clone();
        actual.storage = StorageType::Memory;
        assert!(contract.validate(name, &actual).is_err());
        let mut actual = expected.clone();
        actual.discard = DiscardPolicy::New;
        assert!(contract.validate(name, &actual).is_err());
        let mut actual = expected;
        actual.subjects.push("unexpected.event".to_owned());
        assert!(contract.validate(name, &actual).is_err());
    }

    fn observe(
        observations: &StdMutex<Vec<Observation>>,
        operation: &'static str,
        context: &RequestContext,
    ) {
        observations.lock().expect("observation lock").push((
            operation,
            Arc::clone(&context.request_id),
            context.deadline,
        ));
    }

    fn unused_operation<T>() -> Result<T> {
        Err(ServiceError::internal(anyhow::anyhow!(
            "unexpected mock operation"
        )))
    }

    fn nats_config() -> NatsConfig {
        NatsConfig {
            servers: vec!["nats://127.0.0.1:4222".to_owned()],
            name: "knowledge-core.collaboration.test".to_owned(),
            stream: "KNOWLEDGE_CORE_TEST".to_owned(),
            permission_stream: "KNOWLEDGE_CORE_PERMISSIONS_TEST".to_owned(),
            update_subject: NATS_UPDATE_SUBJECT.to_owned(),
            invalidation_subject: NATS_INVALIDATION_SUBJECT.to_owned(),
            permission_subject: NATS_PERMISSION_SUBJECT.to_owned(),
            connect_timeout: Duration::from_secs(1),
            operation_timeout: Duration::from_secs(1),
            token: None,
            username: None,
            password: None,
            tls: TlsConfig::default(),
        }
    }

    fn actual_consumer_config(expected: &pull::Config) -> consumer::Config {
        consumer::Config {
            durable_name: expected.durable_name.clone(),
            deliver_policy: expected.deliver_policy,
            ack_policy: expected.ack_policy,
            ack_wait: expected.ack_wait,
            filter_subject: expected.filter_subject.clone(),
            max_ack_pending: expected.max_ack_pending,
            max_deliver: expected.max_deliver,
            ..Default::default()
        }
    }
}
