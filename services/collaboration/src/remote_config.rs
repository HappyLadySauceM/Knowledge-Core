use std::{
    env, fs,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::Duration,
};

use aes_gcm::{
    Aes256Gcm, Nonce,
    aead::{Aead as _, KeyInit as _, Payload},
};
use arc_swap::ArcSwap;
use base64::{Engine as _, engine::general_purpose::STANDARD};
use nacos_sdk::api::{
    config::{ConfigChangeListener, ConfigResponse, ConfigService, ConfigServiceBuilder},
    props::ClientProps,
};
use serde::Deserialize;
use tokio::{task::JoinHandle, time};
use tokio_util::sync::CancellationToken;
use url::Url;
use zeroize::Zeroizing;

use crate::{
    actor::{ActorLimits, ActorRegistry},
    config::{ActorConfig, PublicConfig, RoutingConfig, TicketConfig, WorkerConfig},
    error::{Result, ServiceError},
    telemetry::{LogController, Metrics},
    ticket::TicketService,
    websocket::WebSocketServer,
};

const ENVELOPE_SCHEMA: &str = "knowledge-core.io/config-envelope/v1";
const DYNAMIC_API_VERSION: &str = "knowledge-core.io/v1alpha1";
const DYNAMIC_KIND: &str = "DynamicConfig";
const APPLICATION_API_VERSION: &str = "knowledge-core.io/v1beta1";
const APPLICATION_KIND: &str = "ApplicationConfig";
const KEY_SIZE: usize = 32;
const MAXIMUM_CONTENT: usize = 1 << 20;
const ENV_PREFIX: &str = "KNOWLEDGE_CORE_NACOS_";
const SDK_CA_ENV: &str = "NACOS_CLIENT_TLS_CA_CERT";
const NATIVE_TLS_CA_ENV: &str = "SSL_CERT_FILE";
const SDK_TLS_ENABLED_AUTH_PARAM: &str = "nacos_tls_enabled";

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct DynamicDocument {
    pub(crate) revision: u64,
    pub(crate) log_level: String,
    pub(crate) health_check_requests: bool,
    pub(crate) config: Option<ApplicationOverrides>,
}

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct ApplicationOverrides {
    pub(crate) log: Option<ApplicationLog>,
    pub(crate) shutdown_timeout_ms: Option<u64>,
    pub(crate) public: Option<PublicOverrides>,
    pub(crate) rpc: Option<RpcOverrides>,
    pub(crate) admin: Option<AdminOverrides>,
    pub(crate) telemetry: Option<TelemetryOverrides>,
    pub(crate) postgres: Option<PostgresOverrides>,
    pub(crate) redis: Option<RedisOverrides>,
    pub(crate) nats: Option<NatsOverrides>,
    pub(crate) knowledge: Option<KnowledgeOverrides>,
    pub(crate) ticket: Option<TicketOverrides>,
    pub(crate) actor: Option<ActorOverrides>,
    pub(crate) workers: Option<WorkerOverrides>,
    pub(crate) routing: Option<RoutingOverrides>,
}

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct RpcOverrides {
    pub(crate) address: Option<String>,
    pub(crate) service_name: Option<String>,
    pub(crate) request_timeout_ms: Option<u64>,
}
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct AdminOverrides {
    pub(crate) address: Option<String>,
    pub(crate) request_timeout_ms: Option<u64>,
}
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct TelemetryOverrides {
    pub(crate) otlp_endpoint: Option<String>,
    pub(crate) export_timeout_ms: Option<u64>,
    pub(crate) shutdown_timeout_ms: Option<u64>,
}
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct PostgresOverrides {
    pub(crate) max_connections: Option<u32>,
    pub(crate) connect_timeout_ms: Option<u64>,
    pub(crate) acquire_timeout_ms: Option<u64>,
    pub(crate) operation_timeout_ms: Option<u64>,
}
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct RedisOverrides {
    pub(crate) prefix: Option<String>,
    pub(crate) operation_timeout_ms: Option<u64>,
}
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct NatsOverrides {
    pub(crate) servers: Option<Vec<String>>,
    pub(crate) name: Option<String>,
    pub(crate) stream: Option<String>,
    pub(crate) permission_stream: Option<String>,
    pub(crate) update_subject: Option<String>,
    pub(crate) invalidation_subject: Option<String>,
    pub(crate) permission_subject: Option<String>,
    pub(crate) connect_timeout_ms: Option<u64>,
    pub(crate) operation_timeout_ms: Option<u64>,
}
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct KnowledgeOverrides {
    pub(crate) service_name: Option<String>,
    pub(crate) address: Option<String>,
    pub(crate) request_timeout_ms: Option<u64>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct ApplicationLog {
    pub(crate) level: String,
    #[serde(default = "default_health_check_requests")]
    pub(crate) health_check_requests: bool,
}

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct PublicOverrides {
    pub(crate) allowed_origins: Option<Vec<String>>,
    pub(crate) max_frame_bytes: Option<usize>,
    pub(crate) max_update_bytes: Option<usize>,
    pub(crate) max_document_bytes: Option<usize>,
    pub(crate) max_awareness_bytes: Option<usize>,
    pub(crate) max_connections: Option<usize>,
    pub(crate) max_connections_per_document: Option<usize>,
    pub(crate) handshakes_per_second: Option<u32>,
    pub(crate) handshake_burst: Option<u32>,
    pub(crate) handshake_timeout_ms: Option<u64>,
}

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct TicketOverrides {
    pub(crate) ttl_ms: Option<u64>,
}

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct ActorOverrides {
    pub(crate) command_capacity: Option<usize>,
    pub(crate) outbound_capacity: Option<usize>,
    pub(crate) idle_timeout_ms: Option<u64>,
    pub(crate) command_timeout_ms: Option<u64>,
    pub(crate) awareness_messages_per_second: Option<u32>,
    pub(crate) updates_per_second: Option<u32>,
}

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct WorkerOverrides {
    pub(crate) poll_interval_ms: Option<u64>,
    pub(crate) operation_timeout_ms: Option<u64>,
    pub(crate) projection_lease_ms: Option<u64>,
    pub(crate) snapshot_update_threshold: Option<i64>,
    pub(crate) snapshot_byte_threshold: Option<i64>,
    pub(crate) automatic_version_interval_ms: Option<u64>,
    pub(crate) outbox_batch_size: Option<i64>,
}

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct RoutingOverrides {
    pub(crate) instance_count: Option<u32>,
}

#[derive(Clone)]
pub(crate) struct RuntimeTargets {
    pub(crate) log: LogController,
    pub(crate) startup_log_level: String,
    pub(crate) tickets: TicketService,
    pub(crate) actors: ActorRegistry,
    pub(crate) public: Arc<WebSocketServer>,
    pub(crate) startup_public: PublicConfig,
    pub(crate) startup_ticket: TicketConfig,
    pub(crate) startup_actor: ActorConfig,
    pub(crate) startup_workers: WorkerConfig,
    pub(crate) startup_routing: RoutingConfig,
}

#[derive(Clone)]
pub(crate) struct RemoteConfig {
    bootstrap: Bootstrap,
    service: ConfigService,
    current: Arc<ArcSwap<DynamicDocument>>,
}

pub(crate) struct RemoteRuntime {
    service: ConfigService,
    binding: Binding,
    listener: Arc<dyn ConfigChangeListener>,
    cancellation: CancellationToken,
    poll_task: tokio::sync::Mutex<Option<JoinHandle<()>>>,
}

#[derive(Clone)]
struct Bootstrap {
    servers: String,
    tls_enabled: bool,
    binding: Binding,
    username: String,
    password: String,
    key_id: String,
    key: Arc<Zeroizing<Vec<u8>>>,
    timeout: Duration,
    poll_interval: Duration,
}

#[derive(Clone)]
struct Binding {
    namespace: String,
    group: String,
    data_id: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Envelope {
    schema: String,
    key_id: String,
    wrapped_key: EncryptedValue,
    payload: EncryptedValue,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EncryptedValue {
    nonce: String,
    ciphertext: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DynamicWireDocument {
    api_version: String,
    kind: String,
    revision: u64,
    #[serde(default)]
    log: DynamicLog,
    service: Option<String>,
    config: Option<ApplicationOverrides>,
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct DynamicLog {
    level: String,
    #[serde(default = "default_health_check_requests")]
    health_check_requests: bool,
}

fn default_health_check_requests() -> bool {
    true
}

impl RemoteConfig {
    pub(crate) async fn load(service_name: &str) -> Result<Option<Self>> {
        let Some(bootstrap) = Bootstrap::from_environment(service_name)? else {
            return Ok(None);
        };
        let properties = ClientProps::new()
            .server_addr(bootstrap.servers.clone())
            .namespace(bootstrap.binding.namespace.clone())
            .app_name(service_name)
            .auth_username(bootstrap.username.clone())
            .auth_password(bootstrap.password.clone())
            .auth_ext(
                SDK_TLS_ENABLED_AUTH_PARAM,
                bootstrap.tls_enabled.to_string(),
            )
            .load_cache_at_start(false)
            .env_first(false);
        let service = time::timeout(
            bootstrap.timeout,
            ConfigServiceBuilder::new(properties)
                .enable_auth_plugin_http()
                .build(),
        )
        .await
        .map_err(|_| unavailable("Nacos client initialization timed out"))?
        .map_err(|error| unavailable_with("initialize Nacos client", error))?;
        let document = fetch_document(&service, &bootstrap).await?;
        Ok(Some(Self {
            bootstrap,
            service,
            current: Arc::new(ArcSwap::from_pointee(document)),
        }))
    }

    pub(crate) fn document(&self) -> Arc<DynamicDocument> {
        self.current.load_full()
    }

    pub(crate) async fn start(
        &self,
        targets: RuntimeTargets,
        metrics: Metrics,
        cancellation: CancellationToken,
    ) -> Result<RemoteRuntime> {
        let document = fetch_document(&self.service, &self.bootstrap).await?;
        apply_document(&self.current, &Mutex::new(()), document, &targets, &metrics)?;
        let listener: Arc<dyn ConfigChangeListener> = Arc::new(Listener {
            binding: self.bootstrap.binding.clone(),
            key_id: self.bootstrap.key_id.clone(),
            key: Arc::clone(&self.bootstrap.key),
            current: Arc::clone(&self.current),
            update_lock: Mutex::new(()),
            targets: targets.clone(),
            metrics: metrics.clone(),
        });
        time::timeout(
            self.bootstrap.timeout,
            self.service.add_listener(
                self.bootstrap.binding.data_id.clone(),
                self.bootstrap.binding.group.clone(),
                Arc::clone(&listener),
            ),
        )
        .await
        .map_err(|_| unavailable("Nacos listener registration timed out"))?
        .map_err(|error| unavailable_with("register Nacos configuration listener", error))?;

        metrics.config_success();
        let poll_task = tokio::spawn(poll(
            self.service.clone(),
            self.bootstrap.clone(),
            Arc::clone(&self.current),
            targets,
            metrics,
            cancellation.child_token(),
        ));
        tracing::info!(
            component = "config.nacos",
            event = "listener_started",
            data_id = %self.bootstrap.binding.data_id,
            "dynamic configuration listener started"
        );
        Ok(RemoteRuntime {
            service: self.service.clone(),
            binding: self.bootstrap.binding.clone(),
            listener,
            cancellation,
            poll_task: tokio::sync::Mutex::new(Some(poll_task)),
        })
    }
}

impl RemoteRuntime {
    pub(crate) async fn shutdown(&self, maximum_wait: Duration) -> Result<()> {
        self.cancellation.cancel();
        if let Some(mut task) = self.poll_task.lock().await.take()
            && time::timeout(maximum_wait, &mut task).await.is_err()
        {
            task.abort();
            let _ = task.await;
            return Err(ServiceError::internal(anyhow::anyhow!(
                "Nacos configuration poller did not stop before the shutdown deadline"
            )));
        }
        time::timeout(
            maximum_wait,
            self.service.remove_listener(
                self.binding.data_id.clone(),
                self.binding.group.clone(),
                Arc::clone(&self.listener),
            ),
        )
        .await
        .map_err(|_| {
            ServiceError::internal(anyhow::anyhow!(
                "Nacos configuration listener removal timed out"
            ))
        })?
        .map_err(|error| unavailable_with("remove Nacos configuration listener", error))
    }

    pub(crate) fn stop(&self) {
        self.cancellation.cancel();
    }
}

struct Listener {
    binding: Binding,
    key_id: String,
    key: Arc<Zeroizing<Vec<u8>>>,
    current: Arc<ArcSwap<DynamicDocument>>,
    update_lock: Mutex<()>,
    targets: RuntimeTargets,
    metrics: Metrics,
}

impl ConfigChangeListener for Listener {
    fn notify(&self, response: ConfigResponse) {
        if response.namespace() != &self.binding.namespace
            || response.group() != &self.binding.group
            || response.data_id() != &self.binding.data_id
        {
            self.reject("configuration callback binding does not match the requested document");
            return;
        }
        let result = decrypt(
            response.content().as_bytes(),
            self.key.as_slice(),
            &self.key_id,
            &self.binding,
        )
        .and_then(|plaintext| decode_dynamic_document(&plaintext))
        .and_then(|document| {
            apply_document(
                &self.current,
                &self.update_lock,
                document,
                &self.targets,
                &self.metrics,
            )
        });
        if let Err(error) = result {
            self.metrics.config_rejected();
            tracing::error!(
                component = "config.nacos",
                event = "reload_rejected",
                error_key = error.key(),
                "dynamic configuration update rejected"
            );
        }
    }
}

impl Listener {
    fn reject(&self, reason: &'static str) {
        self.metrics.config_rejected();
        tracing::error!(
            component = "config.nacos",
            event = "reload_rejected",
            reason,
            "dynamic configuration update rejected"
        );
    }
}

async fn poll(
    service: ConfigService,
    bootstrap: Bootstrap,
    current: Arc<ArcSwap<DynamicDocument>>,
    targets: RuntimeTargets,
    metrics: Metrics,
    cancellation: CancellationToken,
) {
    let mut interval = time::interval(bootstrap.poll_interval);
    interval.set_missed_tick_behavior(time::MissedTickBehavior::Skip);
    let update_lock = Mutex::new(());
    interval.tick().await;
    loop {
        tokio::select! {
            biased;
            () = cancellation.cancelled() => return,
            _ = interval.tick() => {
                match fetch_document(&service, &bootstrap).await {
                    Ok(document) => {
                        if let Err(error) = apply_document(
                            &current,
                            &update_lock,
                            document,
                            &targets,
                            &metrics,
                        ) {
                            metrics.config_rejected();
                            tracing::error!(
                                component = "config.nacos",
                                event = "reload_rejected",
                                error_key = error.key(),
                                "dynamic configuration update rejected"
                            );
                        }
                    }
                    Err(error) => {
                        metrics.config_failure();
                        tracing::error!(
                            component = "config.nacos",
                            event = "dependency_error",
                            error_key = error.key(),
                            "dynamic configuration health check failed; retaining last-good configuration"
                        );
                    }
                }
            }
        }
    }
}

async fn fetch_document(service: &ConfigService, bootstrap: &Bootstrap) -> Result<DynamicDocument> {
    let response = time::timeout(
        bootstrap.timeout,
        service.get_config(
            bootstrap.binding.data_id.clone(),
            bootstrap.binding.group.clone(),
        ),
    )
    .await
    .map_err(|_| unavailable("Nacos configuration fetch timed out"))?
    .map_err(|error| unavailable_with("fetch Nacos configuration", error))?;
    let plaintext = decrypt(
        response.content().as_bytes(),
        bootstrap.key.as_slice(),
        &bootstrap.key_id,
        &bootstrap.binding,
    )?;
    decode_dynamic_document(&plaintext)
}

fn apply_document(
    current: &ArcSwap<DynamicDocument>,
    update_lock: &Mutex<()>,
    document: DynamicDocument,
    targets: &RuntimeTargets,
    metrics: &Metrics,
) -> Result<()> {
    let _guard = update_lock
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let previous = current.load_full();
    if document.revision < previous.revision {
        return Err(ServiceError::invalid_input(format!(
            "dynamic configuration revision cannot move backward from {} to {}",
            previous.revision, document.revision
        )));
    }
    if document.revision == previous.revision {
        if document != *previous {
            return Err(ServiceError::invalid_input(format!(
                "dynamic configuration revision {} conflicts with the last-good document",
                document.revision
            )));
        }
        metrics.config_success();
        return Ok(());
    }
    if env::var_os("COLLABORATION_LOG_LEVEL").is_none() {
        targets.log.set_level(&document.log_level)?;
    } else {
        targets.log.set_level(&targets.startup_log_level)?;
    }
    let restart_required = document
        .config
        .as_ref()
        .is_some_and(|config| restart_required(config, targets));
    if let Some(config) = &document.config {
        apply_runtime_overrides(config, targets)?;
    }
    let revision = document.revision;
    current.store(Arc::new(document));
    metrics.config_applied(restart_required);
    tracing::info!(
        component = "config.nacos",
        event = "reload",
        revision,
        "dynamic configuration applied"
    );
    Ok(())
}

fn apply_runtime_overrides(config: &ApplicationOverrides, targets: &RuntimeTargets) -> Result<()> {
    let mut public = targets.startup_public.clone();
    let mut ticket = targets.startup_ticket.clone();
    let mut actor = targets.startup_actor.clone();
    if let Some(value) = &config.public {
        if env::var_os("COLLABORATION_ALLOWED_ORIGINS").is_none()
            && let Some(origins) = &value.allowed_origins
        {
            public.allowed_origins.clone_from(origins);
        }
        if env::var_os("COLLABORATION_MAX_FRAME_BYTES").is_none()
            && let Some(value) = value.max_frame_bytes
        {
            public.max_frame_bytes = value;
        }
        if env::var_os("COLLABORATION_MAX_UPDATE_BYTES").is_none()
            && let Some(value) = value.max_update_bytes
        {
            public.max_update_bytes = value;
        }
        if env::var_os("COLLABORATION_MAX_DOCUMENT_BYTES").is_none()
            && let Some(value) = value.max_document_bytes
        {
            public.max_document_bytes = value;
        }
        if env::var_os("COLLABORATION_MAX_AWARENESS_BYTES").is_none()
            && let Some(value) = value.max_awareness_bytes
        {
            public.max_awareness_bytes = value;
        }
        if env::var_os("COLLABORATION_MAX_CONNECTIONS_PER_DOCUMENT").is_none()
            && let Some(value) = value.max_connections_per_document
        {
            public.max_connections_per_document = value;
        }
        if env::var_os("COLLABORATION_HANDSHAKES_PER_SECOND").is_none()
            && let Some(value) = value.handshakes_per_second
        {
            public.handshakes_per_second = value;
        }
        if env::var_os("COLLABORATION_HANDSHAKE_BURST").is_none()
            && let Some(value) = value.handshake_burst
        {
            public.handshake_burst = value;
        }
        if env::var_os("COLLABORATION_HANDSHAKE_TIMEOUT_MS").is_none()
            && let Some(value) = value.handshake_timeout_ms
        {
            public.handshake_timeout = Duration::from_millis(value);
        }
    }
    if env::var_os("COLLABORATION_TICKET_TTL_MS").is_none()
        && let Some(value) = &config.ticket
        && let Some(ttl) = value.ttl_ms
    {
        ticket.ttl = Duration::from_millis(ttl);
    }
    if let Some(value) = &config.actor {
        if env::var_os("COLLABORATION_ACTOR_COMMAND_CAPACITY").is_none()
            && let Some(value) = value.command_capacity
        {
            actor.command_capacity = value;
        }
        if env::var_os("COLLABORATION_OUTBOUND_CAPACITY").is_none()
            && let Some(value) = value.outbound_capacity
        {
            actor.outbound_capacity = value;
        }
        if env::var_os("COLLABORATION_ACTOR_IDLE_TIMEOUT_MS").is_none()
            && let Some(value) = value.idle_timeout_ms
        {
            actor.idle_timeout = Duration::from_millis(value);
        }
        if env::var_os("COLLABORATION_ACTOR_COMMAND_TIMEOUT_MS").is_none()
            && let Some(value) = value.command_timeout_ms
        {
            actor.command_timeout = Duration::from_millis(value);
        }
        if env::var_os("COLLABORATION_AWARENESS_RATE").is_none()
            && let Some(value) = value.awareness_messages_per_second
        {
            actor.awareness_messages_per_second = value;
        }
        if env::var_os("COLLABORATION_UPDATE_RATE").is_none()
            && let Some(value) = value.updates_per_second
        {
            actor.updates_per_second = value;
        }
    }
    let limits = ActorLimits::from_config(&actor, &public)?;
    targets.public.apply_dynamic(&public)?;
    targets.tickets.set_ttl(ticket.ttl)?;
    targets.actors.set_limits(limits);
    Ok(())
}

fn restart_required(config: &ApplicationOverrides, targets: &RuntimeTargets) -> bool {
    config.shutdown_timeout_ms.is_some()
        || config.rpc.is_some()
        || config.admin.is_some()
        || config.telemetry.is_some()
        || config.postgres.is_some()
        || config.redis.is_some()
        || config.nats.is_some()
        || config.knowledge.is_some()
        || config
            .public
            .as_ref()
            .and_then(|value| value.max_connections)
            .is_some_and(|value| value != targets.startup_public.max_connections)
        || config.workers.as_ref().is_some_and(|value| {
            value
                .poll_interval_ms
                .is_some_and(|v| Duration::from_millis(v) != targets.startup_workers.poll_interval)
                || value.operation_timeout_ms.is_some_and(|v| {
                    Duration::from_millis(v) != targets.startup_workers.operation_timeout
                })
                || value.projection_lease_ms.is_some_and(|v| {
                    Duration::from_millis(v) != targets.startup_workers.projection_lease
                })
                || value
                    .snapshot_update_threshold
                    .is_some_and(|v| v != targets.startup_workers.snapshot_update_threshold)
                || value
                    .snapshot_byte_threshold
                    .is_some_and(|v| v != targets.startup_workers.snapshot_byte_threshold)
                || value.automatic_version_interval_ms.is_some_and(|v| {
                    Duration::from_millis(v) != targets.startup_workers.automatic_version_interval
                })
                || value
                    .outbox_batch_size
                    .is_some_and(|v| v != targets.startup_workers.outbox_batch_size)
        })
        || config.routing.as_ref().is_some_and(|value| {
            value
                .instance_count
                .is_some_and(|count| count != targets.startup_routing.instance_count)
        })
}

fn decrypt(
    encoded: &[u8],
    key: &[u8],
    expected_key_id: &str,
    binding: &Binding,
) -> Result<Vec<u8>> {
    if encoded.is_empty() || encoded.len() > MAXIMUM_CONTENT * 2 {
        return Err(invalid("configuration envelope size is invalid"));
    }
    if key.len() != KEY_SIZE {
        return Err(invalid("configuration key must contain 32 bytes"));
    }
    let mut deserializer = serde_json::Deserializer::from_slice(encoded);
    let envelope = Envelope::deserialize(&mut deserializer)
        .map_err(|error| invalid_with("decode configuration envelope", error))?;
    deserializer
        .end()
        .map_err(|error| invalid_with("decode configuration envelope trailing content", error))?;
    if envelope.schema != ENVELOPE_SCHEMA {
        return Err(invalid("configuration envelope schema is unsupported"));
    }
    if envelope.key_id.is_empty() || envelope.key_id != expected_key_id {
        return Err(invalid(
            "configuration key identifier does not match the configured key",
        ));
    }
    let data_key = open(
        key,
        &envelope.wrapped_key,
        wrap_aad(&envelope.key_id).as_bytes(),
    )?;
    if data_key.len() != KEY_SIZE {
        return Err(invalid("configuration data key has an invalid size"));
    }
    let plaintext = open(
        &data_key,
        &envelope.payload,
        payload_aad(&envelope.key_id, binding).as_bytes(),
    )?;
    if plaintext.is_empty() || plaintext.len() > MAXIMUM_CONTENT {
        return Err(invalid("configuration plaintext size is invalid"));
    }
    Ok(plaintext)
}

fn open(key: &[u8], value: &EncryptedValue, additional_data: &[u8]) -> Result<Vec<u8>> {
    let cipher = Aes256Gcm::new_from_slice(key)
        .map_err(|error| invalid_with("create AES-256-GCM cipher", error))?;
    let nonce = STANDARD
        .decode(&value.nonce)
        .map_err(|error| invalid_with("decode configuration nonce", error))?;
    let nonce: [u8; 12] = nonce
        .try_into()
        .map_err(|_| invalid("configuration nonce has an invalid size"))?;
    let ciphertext = STANDARD
        .decode(&value.ciphertext)
        .map_err(|error| invalid_with("decode configuration ciphertext", error))?;
    cipher
        .decrypt(
            &Nonce::from(nonce),
            Payload {
                msg: &ciphertext,
                aad: additional_data,
            },
        )
        .map_err(|_| invalid("configuration authentication failed"))
}

fn decode_dynamic_document(contents: &[u8]) -> Result<DynamicDocument> {
    if contents.is_empty() || contents.len() > MAXIMUM_CONTENT {
        return Err(invalid("dynamic configuration document size is invalid"));
    }
    let mut documents = serde_yaml::Deserializer::from_slice(contents);
    let first = documents
        .next()
        .ok_or_else(|| invalid("dynamic configuration document is required"))?;
    let document = DynamicWireDocument::deserialize(first)
        .map_err(|error| invalid_with("decode dynamic configuration", error))?;
    if documents.next().is_some() {
        return Err(invalid(
            "multiple dynamic configuration documents are not allowed",
        ));
    }
    let legacy = document.api_version == DYNAMIC_API_VERSION && document.kind == DYNAMIC_KIND;
    let application =
        document.api_version == APPLICATION_API_VERSION && document.kind == APPLICATION_KIND;
    if !legacy && !application {
        return Err(invalid(
            "dynamic configuration api_version or kind is unsupported",
        ));
    }
    if document.revision == 0 {
        return Err(invalid("dynamic configuration revision must be positive"));
    }
    let (log_level, health_check_requests) = if legacy {
        (document.log.level, document.log.health_check_requests)
    } else {
        let config = document
            .config
            .as_ref()
            .ok_or_else(|| invalid("application configuration config is required"))?;
        let log = config
            .log
            .as_ref()
            .ok_or_else(|| invalid("application configuration log is required"))?;
        validate_log_level(&log.level)?;
        (log.level.clone(), log.health_check_requests)
    };
    validate_log_level(&log_level)?;
    if application && document.service.as_deref() != Some("collaboration") {
        return Err(invalid(
            "application configuration service must be collaboration",
        ));
    }
    if let Some(config) = &document.config {
        validate_application_overrides(config)?;
    }
    Ok(DynamicDocument {
        revision: document.revision,
        log_level,
        health_check_requests,
        config: document.config,
    })
}

fn validate_application_overrides(config: &ApplicationOverrides) -> Result<()> {
    if let Some(value) = config.shutdown_timeout_ms
        && value == 0
    {
        return Err(invalid("shutdown timeout must be positive"));
    }
    if let Some(value) = &config.public {
        if let Some(origins) = &value.allowed_origins {
            for origin in origins {
                let parsed = Url::parse(origin)
                    .map_err(|error| invalid_with("parse allowed origin", error))?;
                if !matches!(parsed.scheme(), "http" | "https")
                    || parsed.host_str().is_none()
                    || parsed.username() != ""
                    || parsed.password().is_some()
                    || parsed.path() != "/"
                    || parsed.query().is_some()
                    || parsed.fragment().is_some()
                {
                    return Err(invalid(
                        "allowed origins must be exact HTTP origins without credentials or paths",
                    ));
                }
            }
        }
        for value in [
            value.max_frame_bytes,
            value.max_update_bytes,
            value.max_document_bytes,
            value.max_awareness_bytes,
            value.max_connections,
            value.max_connections_per_document,
        ] {
            if value == Some(0) {
                return Err(invalid("public limits must be positive"));
            }
        }
        if value.handshakes_per_second == Some(0)
            || value.handshake_burst == Some(0)
            || value.handshake_timeout_ms == Some(0)
        {
            return Err(invalid("WebSocket handshake limits must be positive"));
        }
    }
    if let Some(value) = &config.ticket
        && value
            .ttl_ms
            .is_some_and(|ttl| ttl == 0 || ttl > crate::config::MAX_TICKET_TTL_MS)
    {
        return Err(invalid(
            "ticket TTL must be between one millisecond and 60 seconds",
        ));
    }
    if let Some(value) = &config.actor
        && (value.command_capacity == Some(0)
            || value.outbound_capacity == Some(0)
            || value.idle_timeout_ms == Some(0)
            || value.command_timeout_ms == Some(0)
            || value.awareness_messages_per_second == Some(0)
            || value.updates_per_second == Some(0))
    {
        return Err(invalid("actor limits must be positive"));
    }
    if let Some(value) = &config.workers
        && (value.poll_interval_ms == Some(0)
            || value.operation_timeout_ms == Some(0)
            || value.projection_lease_ms == Some(0)
            || value.automatic_version_interval_ms == Some(0)
            || value.snapshot_update_threshold.is_some_and(|v| v <= 0)
            || value.snapshot_byte_threshold.is_some_and(|v| v <= 0)
            || value.outbox_batch_size.is_some_and(|v| v <= 0))
    {
        return Err(invalid("worker limits must be positive"));
    }
    if let Some(value) = &config.routing
        && value
            .instance_count
            .is_some_and(|count| count == 0 || count > crate::routing::MAXIMUM_INSTANCE_COUNT)
    {
        return Err(invalid("routing instance_count must be between 1 and 32"));
    }
    Ok(())
}

pub(crate) fn validate_log_level(level: &str) -> Result<()> {
    if level.trim() != level {
        return Err(invalid(
            "dynamic configuration log level must not contain surrounding whitespace",
        ));
    }
    match level {
        "debug" | "info" | "warn" | "error" => Ok(()),
        _ => Err(invalid(
            "dynamic configuration log level must be debug, info, warn, or error",
        )),
    }
}

impl Bootstrap {
    fn from_environment(service_name: &str) -> Result<Option<Self>> {
        if !boolean(&format!("{ENV_PREFIX}ENABLED"), false)? {
            return Ok(None);
        }
        let namespace = required(&format!("{ENV_PREFIX}NAMESPACE"))?;
        let group = optional_value(&format!("{ENV_PREFIX}GROUP"))
            .unwrap_or_else(|| "KNOWLEDGE_CORE".to_owned());
        let data_id = optional_value(&format!("{ENV_PREFIX}DATA_ID"))
            .unwrap_or_else(|| format!("{service_name}.dynamic.yaml"));
        let binding = Binding {
            namespace,
            group,
            data_id,
        };
        binding.validate()?;
        let tls_enabled = boolean(&format!("{ENV_PREFIX}TLS_ENABLED"), true)?;
        let servers = parse_servers(&required(&format!("{ENV_PREFIX}SERVERS"))?, tls_enabled)?;
        let username = required(&format!("{ENV_PREFIX}USERNAME"))?;
        let password = required(&format!("{ENV_PREFIX}PASSWORD"))?;
        let runtime_dir = required(&format!("{ENV_PREFIX}RUNTIME_DIR"))?;
        let home_dir = required("HOME")?;
        if tls_enabled {
            let ca_file = required(&format!("{ENV_PREFIX}CA_FILE"))?;
            let sdk_ca_file = required(SDK_CA_ENV)?;
            let native_tls_ca_file = required(NATIVE_TLS_CA_ENV)?;
            prepare_sdk_environment(
                Path::new(&ca_file),
                Path::new(&sdk_ca_file),
                Path::new(&native_tls_ca_file),
                Path::new(&runtime_dir),
                Path::new(&home_dir),
                &binding.namespace,
            )?;
        } else {
            if optional_value(&format!("{ENV_PREFIX}CA_FILE")).is_some()
                || optional_value(SDK_CA_ENV).is_some()
                || optional_value(NATIVE_TLS_CA_ENV).is_some()
            {
                return Err(invalid(
                    "Nacos CA environment variables must be unset when TLS is disabled",
                ));
            }
            prepare_sdk_runtime(
                Path::new(&runtime_dir),
                Path::new(&home_dir),
                &binding.namespace,
            )?;
        }
        let key_id = required(&format!("{ENV_PREFIX}KEY_ID"))?;
        if key_id.contains(['\r', '\n', '|']) {
            return Err(invalid(
                "Nacos key identifier contains unsupported characters",
            ));
        }
        let key = STANDARD
            .decode(required(&format!("{ENV_PREFIX}KEK"))?)
            .map_err(|error| invalid_with("decode Nacos AES-256 key", error))?;
        if key.len() != KEY_SIZE {
            return Err(invalid("Nacos AES-256 key must contain 32 bytes"));
        }
        Ok(Some(Self {
            servers,
            tls_enabled,
            binding,
            username,
            password,
            key_id,
            key: Arc::new(Zeroizing::new(key)),
            timeout: duration(
                &format!("{ENV_PREFIX}TIMEOUT"),
                "5s",
                Duration::from_secs(1),
                Duration::from_secs(30),
            )?,
            poll_interval: duration(
                &format!("{ENV_PREFIX}POLL_INTERVAL"),
                "30s",
                Duration::from_secs(5),
                Duration::from_mins(5),
            )?,
        }))
    }
}

impl Binding {
    fn validate(&self) -> Result<()> {
        for (name, value) in [
            ("namespace", &self.namespace),
            ("group", &self.group),
            ("data ID", &self.data_id),
        ] {
            if value.trim().is_empty() || value.trim() != value {
                return Err(invalid(format!(
                    "Nacos {name} must be non-empty and trimmed"
                )));
            }
            if value.contains(['\r', '\n', '|', '/', '\\']) || value == "." || value == ".." {
                return Err(invalid(format!(
                    "Nacos {name} contains unsupported characters"
                )));
            }
        }
        Ok(())
    }
}

fn prepare_sdk_environment(
    ca_file: &Path,
    sdk_ca_file: &Path,
    native_tls_ca_file: &Path,
    runtime_dir: &Path,
    home_dir: &Path,
    namespace: &str,
) -> Result<()> {
    let cache_directory = sdk_cache_directory(
        ca_file,
        sdk_ca_file,
        native_tls_ca_file,
        runtime_dir,
        home_dir,
        namespace,
    )?;
    let metadata =
        fs::metadata(ca_file).map_err(|error| invalid_with("inspect Nacos CA file", error))?;
    if !metadata.is_file() {
        return Err(invalid("Nacos CA path must identify a regular file"));
    }
    fs::File::open(ca_file).map_err(|error| invalid_with("open Nacos CA file", error))?;
    fs::create_dir_all(cache_directory)
        .map_err(|error| invalid_with("prepare Nacos client cache directory", error))
}

fn prepare_sdk_runtime(runtime_dir: &Path, home_dir: &Path, namespace: &str) -> Result<()> {
    if !runtime_dir.is_absolute()
        || !home_dir.is_absolute()
        || runtime_dir != home_dir.join("nacos")
    {
        return Err(invalid(
            "Nacos runtime directory must be absolute and equal HOME/nacos",
        ));
    }
    fs::create_dir_all(runtime_dir.join("config").join(namespace))
        .map_err(|error| invalid_with("prepare Nacos client cache directory", error))
}

fn sdk_cache_directory(
    ca_file: &Path,
    sdk_ca_file: &Path,
    native_tls_ca_file: &Path,
    runtime_dir: &Path,
    home_dir: &Path,
    namespace: &str,
) -> Result<PathBuf> {
    if !ca_file.is_absolute() || ca_file != sdk_ca_file || ca_file != native_tls_ca_file {
        return Err(invalid(
            "Nacos CA file must be absolute and match NACOS_CLIENT_TLS_CA_CERT and SSL_CERT_FILE",
        ));
    }
    if !runtime_dir.is_absolute()
        || !home_dir.is_absolute()
        || runtime_dir != home_dir.join("nacos")
    {
        return Err(invalid(
            "Nacos runtime directory must be absolute and equal HOME/nacos",
        ));
    }
    Ok(runtime_dir.join("config").join(namespace))
}

fn parse_servers(raw: &str, tls_enabled: bool) -> Result<String> {
    let mut servers = Vec::new();
    let expected_scheme = if tls_enabled { "https" } else { "http" };
    for value in raw.split(',') {
        let value = value.trim();
        let parsed =
            Url::parse(value).map_err(|error| invalid_with("parse Nacos server URL", error))?;
        if parsed.scheme() != expected_scheme
            || parsed.host_str().is_none()
            || parsed.port().is_none()
            || parsed.username() != ""
            || parsed.password().is_some()
            || parsed.path() != "/"
            || parsed.query().is_some()
            || parsed.fragment().is_some()
        {
            return Err(invalid(format!(
                "Nacos servers must be absolute {expected_scheme}://host:port URLs without credentials or paths"
            )));
        }
        let host = parsed
            .host()
            .ok_or_else(|| invalid("Nacos server host is required"))?;
        let port = parsed
            .port()
            .ok_or_else(|| invalid("Nacos server port is required"))?;
        let address = match host {
            url::Host::Ipv6(value) => format!("[{value}]:{port}"),
            _ => format!("{host}:{port}"),
        };
        if servers.contains(&address) {
            return Err(invalid("Nacos server addresses must be unique"));
        }
        servers.push(address);
    }
    if servers.is_empty() {
        return Err(invalid("at least one Nacos server is required"));
    }
    Ok(servers.join(","))
}

fn required(name: &str) -> Result<String> {
    optional_value(name).ok_or_else(|| {
        invalid(format!(
            "{name} is required when Nacos configuration is enabled"
        ))
    })
}

fn optional_value(name: &str) -> Option<String> {
    env::var(name)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}

fn boolean(name: &str, fallback: bool) -> Result<bool> {
    match env::var(name) {
        Err(_) => Ok(fallback),
        Ok(value) if value == "true" => Ok(true),
        Ok(value) if value == "false" => Ok(false),
        Ok(_) => Err(invalid(format!("{name} must be true or false"))),
    }
}

fn duration(name: &str, fallback: &str, minimum: Duration, maximum: Duration) -> Result<Duration> {
    let raw = optional_value(name).unwrap_or_else(|| fallback.to_owned());
    let value = parse_duration(&raw).map_err(|error| invalid(format!("parse {name}: {error}")))?;
    if !(minimum..=maximum).contains(&value) {
        return Err(invalid(format!(
            "{name} must be an integer duration using ms, s, or m between {} and {} milliseconds",
            minimum.as_millis(),
            maximum.as_millis()
        )));
    }
    Ok(value)
}

fn parse_duration(raw: &str) -> std::result::Result<Duration, &'static str> {
    let (number, multiplier) = if let Some(number) = raw.strip_suffix("ms") {
        (number, 1_u64)
    } else if let Some(number) = raw.strip_suffix('s') {
        (number, 1_000)
    } else if let Some(number) = raw.strip_suffix('m') {
        (number, 60_000)
    } else {
        return Err("duration unit must be ms, s, or m");
    };
    let value = number
        .parse::<u64>()
        .map_err(|_| "duration value must be a positive integer")?;
    let milliseconds = value
        .checked_mul(multiplier)
        .filter(|value| *value > 0)
        .ok_or("duration value must be a positive integer")?;
    Ok(Duration::from_millis(milliseconds))
}

fn wrap_aad(key_id: &str) -> String {
    format!("{ENVELOPE_SCHEMA}|keywrap|{key_id}")
}

fn payload_aad(key_id: &str, binding: &Binding) -> String {
    format!(
        "{ENVELOPE_SCHEMA}|payload|{key_id}|{}|{}|{}",
        binding.namespace, binding.group, binding.data_id
    )
}

fn invalid(detail: impl Into<String>) -> ServiceError {
    ServiceError::invalid_input(detail.into())
}

fn invalid_with(detail: impl Into<String>, error: impl Into<anyhow::Error>) -> ServiceError {
    invalid(detail).with_source(error)
}

fn unavailable(detail: impl Into<String>) -> ServiceError {
    ServiceError::unavailable(anyhow::anyhow!(detail.into()))
}

fn unavailable_with(detail: &'static str, error: impl std::fmt::Display) -> ServiceError {
    ServiceError::unavailable(anyhow::anyhow!("{detail}: {error}"))
}

#[cfg(test)]
mod tests {
    use super::{
        Binding, decode_dynamic_document, decrypt, parse_duration, parse_servers, payload_aad,
        sdk_cache_directory, validate_log_level,
    };

    use std::{path::PathBuf, time::Duration};

    #[test]
    fn dynamic_document_is_strict_and_versioned() {
        let document = decode_dynamic_document(
            b"api_version: knowledge-core.io/v1alpha1\nkind: DynamicConfig\nrevision: 4\nlog:\n  level: warn\n",
        )
        .expect("valid document");
        assert_eq!(document.revision, 4);
        assert_eq!(document.log_level, "warn");
        assert!(
            decode_dynamic_document(
                b"api_version: knowledge-core.io/v1alpha1\nkind: DynamicConfig\nrevision: 4\nlog:\n  level: info\nunknown: true\n",
            )
            .is_err()
        );
        assert!(validate_log_level("verbose").is_err());
    }

    #[test]
    fn application_document_is_strict_and_service_bound() {
        let document = decode_dynamic_document(b"api_version: knowledge-core.io/v1beta1\nkind: ApplicationConfig\nservice: collaboration\nrevision: 5\nconfig:\n  log:\n    level: info\n    health_check_requests: false\n  public:\n    allowed_origins: [https://example.com]\n    handshakes_per_second: 50\n  ticket:\n    ttl_ms: 20000\n").expect("valid application document");
        assert_eq!(document.revision, 5);
        assert!(!document.health_check_requests);
        assert!(document.config.is_some());
        assert!(decode_dynamic_document(b"api_version: knowledge-core.io/v1beta1\nkind: ApplicationConfig\nservice: gateway\nrevision: 5\nconfig:\n  log:\n    level: info\n").is_err());
        assert!(decode_dynamic_document(b"api_version: knowledge-core.io/v1beta1\nkind: ApplicationConfig\nservice: collaboration\nrevision: 5\nconfig:\n  log:\n    level: info\n  postgres:\n    url: postgres://secret\n").is_err());
    }

    #[test]
    fn server_addresses_are_normalized_for_the_native_client() {
        assert_eq!(
            parse_servers("https://nacos:8848,https://[::1]:8848", true).expect("servers"),
            "nacos:8848,[::1]:8848"
        );
        for invalid in [
            "nacos:8848",
            "http://nacos:8848",
            "http://nacos",
            "http://user@nacos:8848",
            "http://nacos:8848/path",
        ] {
            assert!(parse_servers(invalid, true).is_err(), "{invalid}");
        }
        assert_eq!(
            parse_servers("http://nacos:8848,http://[::1]:8848", false).expect("servers"),
            "nacos:8848,[::1]:8848"
        );
        assert!(parse_servers("https://nacos:8848", false).is_err());
    }

    #[test]
    fn sdk_transport_and_cache_paths_share_one_explicit_contract() {
        let root = std::env::temp_dir().join("knowledge-core-nacos-sdk-paths");
        let ca_file = root.join("internal-ca.crt");
        let home_dir = root.join("home");
        let runtime_dir = home_dir.join("nacos");
        assert_eq!(
            sdk_cache_directory(&ca_file, &ca_file, &ca_file, &runtime_dir, &home_dir, "dev",)
                .expect("valid SDK paths"),
            runtime_dir.join("config").join("dev")
        );
        assert!(
            sdk_cache_directory(
                &ca_file,
                &root.join("other-ca.crt"),
                &ca_file,
                &runtime_dir,
                &home_dir,
                "dev",
            )
            .is_err()
        );
        assert!(
            sdk_cache_directory(
                &ca_file,
                &ca_file,
                &ca_file,
                &PathBuf::from("relative"),
                &home_dir,
                "dev",
            )
            .is_err()
        );
    }

    #[test]
    fn binding_rejects_values_that_can_escape_the_sdk_cache_path() {
        for namespace in [".", "..", "dev/other", "dev\\other"] {
            assert!(
                Binding {
                    namespace: namespace.to_owned(),
                    group: "KNOWLEDGE_CORE".to_owned(),
                    data_id: "collaboration.dynamic.yaml".to_owned(),
                }
                .validate()
                .is_err(),
                "{namespace}"
            );
        }
    }

    #[test]
    fn payload_aad_matches_the_go_contract() {
        let binding = Binding {
            namespace: "test".to_owned(),
            group: "KNOWLEDGE_CORE".to_owned(),
            data_id: "collaboration.dynamic.yaml".to_owned(),
        };
        assert_eq!(
            payload_aad("config-1", &binding),
            "knowledge-core.io/config-envelope/v1|payload|config-1|test|KNOWLEDGE_CORE|collaboration.dynamic.yaml"
        );
    }

    #[test]
    fn duration_values_use_the_portable_go_contract() {
        for (raw, expected) in [
            ("1500ms", Duration::from_millis(1_500)),
            ("5s", Duration::from_secs(5)),
            ("2m", Duration::from_mins(2)),
        ] {
            assert_eq!(parse_duration(raw).expect("valid duration"), expected);
        }
        for invalid in ["", "5000", "1.5s", "0s", "-1s", " 5s", "5h"] {
            assert!(parse_duration(invalid).is_err(), "{invalid}");
        }
    }

    #[test]
    fn decrypts_envelope_created_by_go_configctl() {
        let binding = Binding {
            namespace: "test".to_owned(),
            group: "KNOWLEDGE_CORE".to_owned(),
            data_id: "collaboration.dynamic.yaml".to_owned(),
        };
        let plaintext = decrypt(
            include_bytes!("../tests/fixtures/config-envelope.json"),
            &[0x42; 32],
            "test-key",
            &binding,
        )
        .expect("Go envelope decrypts");
        let document = decode_dynamic_document(&plaintext).expect("dynamic document");
        assert_eq!(document.revision, 1);
        assert_eq!(document.log_level, "info");
    }
}
