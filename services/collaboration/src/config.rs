use std::{env, net::SocketAddr, path::PathBuf, time::Duration};

use url::Url;

use crate::{
    actor::ActorLimits,
    endpoint::validate_socket_endpoint,
    error::{Result, ServiceError},
    remote_config::{RemoteConfig, validate_log_level},
};

const DEFAULT_ETCD_PREFIX: &str = "/knowledge-core/development/registry";
pub(crate) const MAX_TICKET_TTL_MS: u64 = 60_000;
pub const NATS_UPDATE_SUBJECT: &str = "collaboration.documents.updated";
pub const NATS_INVALIDATION_SUBJECT: &str = "collaboration.documents.invalidated";
pub const NATS_PERMISSION_SUBJECT: &str = "knowledge.permissions.changed";

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Environment {
    Development,
    Production,
    Test,
}

#[derive(Clone)]
pub struct Config {
    pub environment: Environment,
    pub instance_id: String,
    pub shutdown_timeout: Duration,
    pub public: PublicConfig,
    pub rpc: RpcConfig,
    pub admin: AdminConfig,
    pub telemetry: TelemetryConfig,
    pub postgres: PostgresConfig,
    pub redis: RedisConfig,
    pub nats: NatsConfig,
    pub etcd: EtcdConfig,
    pub knowledge: KnowledgeConfig,
    pub ticket: TicketConfig,
    pub actor: ActorConfig,
    pub workers: WorkerConfig,
    pub(crate) remote: Option<RemoteConfig>,
}

#[derive(Clone)]
pub struct PublicConfig {
    pub address: SocketAddr,
    pub allowed_origins: Vec<String>,
    pub max_frame_bytes: usize,
    pub max_update_bytes: usize,
    pub max_document_bytes: usize,
    pub max_awareness_bytes: usize,
    pub max_connections: usize,
    pub max_connections_per_document: usize,
    pub handshakes_per_second: u32,
    pub handshake_burst: u32,
    pub handshake_timeout: Duration,
}

#[derive(Clone)]
pub struct RpcConfig {
    pub address: SocketAddr,
    pub advertised_address: String,
    pub service_name: String,
    pub request_timeout: Duration,
    pub tls: TlsConfig,
}

#[derive(Clone)]
pub struct AdminConfig {
    pub address: SocketAddr,
    pub request_timeout: Duration,
}

#[derive(Clone)]
pub struct TelemetryConfig {
    pub log_level: String,
    pub health_check_requests: bool,
    pub otlp_endpoint: Option<Url>,
    pub export_timeout: Duration,
    pub shutdown_timeout: Duration,
}

#[derive(Clone)]
pub struct PostgresConfig {
    pub url: String,
    pub max_connections: u32,
    pub connect_timeout: Duration,
    pub acquire_timeout: Duration,
    pub operation_timeout: Duration,
    pub tls: TlsConfig,
}

#[derive(Clone)]
pub struct RedisConfig {
    pub url: Url,
    pub prefix: String,
    pub operation_timeout: Duration,
}

#[derive(Clone)]
pub struct NatsConfig {
    pub servers: Vec<String>,
    pub name: String,
    pub stream: String,
    pub permission_stream: String,
    pub update_subject: String,
    pub invalidation_subject: String,
    pub permission_subject: String,
    pub connect_timeout: Duration,
    pub operation_timeout: Duration,
    pub token: Option<String>,
    pub username: Option<String>,
    pub password: Option<String>,
    pub tls: TlsConfig,
}

impl NatsConfig {
    pub(crate) fn validate_protocol_contract(&self) -> Result<()> {
        for (name, stream) in [
            ("COLLABORATION_NATS_STREAM", self.stream.as_str()),
            (
                "COLLABORATION_NATS_PERMISSION_STREAM",
                self.permission_stream.as_str(),
            ),
        ] {
            if stream.is_empty() || stream.trim() != stream {
                return Err(ServiceError::invalid_input(format!(
                    "{name} must be non-empty and trimmed"
                )));
            }
        }
        if self.stream == self.permission_stream {
            return Err(ServiceError::invalid_input(
                "COLLABORATION_NATS_STREAM and COLLABORATION_NATS_PERMISSION_STREAM must be different",
            ));
        }
        validate_protocol_subject(
            "COLLABORATION_NATS_UPDATE_SUBJECT",
            &self.update_subject,
            NATS_UPDATE_SUBJECT,
        )?;
        validate_protocol_subject(
            "COLLABORATION_NATS_INVALIDATION_SUBJECT",
            &self.invalidation_subject,
            NATS_INVALIDATION_SUBJECT,
        )?;
        validate_protocol_subject(
            "COLLABORATION_NATS_PERMISSION_SUBJECT",
            &self.permission_subject,
            NATS_PERMISSION_SUBJECT,
        )
    }
}

#[derive(Clone)]
pub struct EtcdConfig {
    pub endpoints: Vec<String>,
    pub prefix: String,
    pub username: Option<String>,
    pub password: Option<String>,
    pub connect_timeout: Duration,
    pub request_timeout: Duration,
    pub lease_ttl: Duration,
    pub tls: TlsConfig,
}

#[derive(Clone)]
pub struct KnowledgeConfig {
    pub service_name: String,
    pub request_timeout: Duration,
    pub tls: TlsConfig,
}

#[derive(Clone)]
pub struct TicketConfig {
    pub ttl: Duration,
    pub subprotocol: String,
    pub fragment: String,
}

#[derive(Clone)]
pub struct ActorConfig {
    pub command_capacity: usize,
    pub outbound_capacity: usize,
    pub idle_timeout: Duration,
    pub command_timeout: Duration,
    pub awareness_messages_per_second: u32,
    pub updates_per_second: u32,
}

#[derive(Clone)]
pub struct WorkerConfig {
    pub poll_interval: Duration,
    pub operation_timeout: Duration,
    pub projection_lease: Duration,
    pub snapshot_update_threshold: i64,
    pub snapshot_byte_threshold: i64,
    pub automatic_version_interval: Duration,
    pub outbox_batch_size: i64,
}

#[derive(Clone, Default)]
pub struct TlsConfig {
    pub enabled: bool,
    pub ca_file: Option<PathBuf>,
    pub cert_file: Option<PathBuf>,
    pub key_file: Option<PathBuf>,
    pub server_name: Option<String>,
}

impl Config {
    /// Loads static environment configuration and the mandatory initial Nacos
    /// document when remote configuration is enabled.
    ///
    /// # Errors
    ///
    /// Returns an error when either source is invalid or Nacos cannot provide a
    /// valid encrypted document before the startup deadline.
    pub async fn load() -> Result<Self> {
        let mut config = Self::from_environment()?;
        if let Some(remote) = RemoteConfig::load("collaboration").await? {
            let remote_document = remote.document();
            config.apply_remote(&remote_document)?;
            config.remote = Some(remote);
        }
        Ok(config)
    }

    #[allow(clippy::too_many_lines)]
    fn apply_remote(&mut self, document: &crate::remote_config::DynamicDocument) -> Result<()> {
        if env::var_os("COLLABORATION_LOG_LEVEL").is_none() {
            self.telemetry.log_level.clone_from(&document.log_level);
        }
        if env::var_os("COLLABORATION_LOG_HEALTH_CHECK_REQUESTS").is_none() {
            self.telemetry.health_check_requests = document.health_check_requests;
        }
        let Some(overrides) = &document.config else {
            return Ok(());
        };
        if let Some(log) = &overrides.log
            && env::var_os("COLLABORATION_LOG_LEVEL").is_none()
        {
            self.telemetry.log_level.clone_from(&log.level);
            self.telemetry.health_check_requests = log.health_check_requests;
        }
        if env::var_os("COLLABORATION_SHUTDOWN_TIMEOUT_MS").is_none()
            && let Some(value) = overrides.shutdown_timeout_ms
        {
            self.shutdown_timeout = Duration::from_millis(value);
        }
        if let Some(value) = &overrides.public {
            if env::var_os("COLLABORATION_ALLOWED_ORIGINS").is_none()
                && let Some(origins) = &value.allowed_origins
            {
                self.public.allowed_origins.clone_from(origins);
            }
            macro_rules! apply {
                ($field:ident, $env:literal) => {
                    if env::var_os($env).is_none()
                        && let Some(value) = value.$field
                    {
                        self.public.$field = value;
                    }
                };
            }
            apply!(max_frame_bytes, "COLLABORATION_MAX_FRAME_BYTES");
            apply!(max_update_bytes, "COLLABORATION_MAX_UPDATE_BYTES");
            apply!(max_document_bytes, "COLLABORATION_MAX_DOCUMENT_BYTES");
            apply!(max_awareness_bytes, "COLLABORATION_MAX_AWARENESS_BYTES");
            apply!(max_connections, "COLLABORATION_MAX_CONNECTIONS");
            apply!(
                max_connections_per_document,
                "COLLABORATION_MAX_CONNECTIONS_PER_DOCUMENT"
            );
            apply!(handshakes_per_second, "COLLABORATION_HANDSHAKES_PER_SECOND");
            apply!(handshake_burst, "COLLABORATION_HANDSHAKE_BURST");
            if env::var_os("COLLABORATION_HANDSHAKE_TIMEOUT_MS").is_none()
                && let Some(value) = value.handshake_timeout_ms
            {
                self.public.handshake_timeout = Duration::from_millis(value);
            }
        }
        if let Some(value) = &overrides.ticket
            && env::var_os("COLLABORATION_TICKET_TTL_MS").is_none()
            && let Some(value) = value.ttl_ms
        {
            self.ticket.ttl = Duration::from_millis(value);
        }
        if let Some(value) = &overrides.actor {
            macro_rules! apply {
                ($field:ident, $env:literal) => {
                    if env::var_os($env).is_none()
                        && let Some(value) = value.$field
                    {
                        self.actor.$field = value;
                    }
                };
            }
            apply!(command_capacity, "COLLABORATION_ACTOR_COMMAND_CAPACITY");
            apply!(outbound_capacity, "COLLABORATION_OUTBOUND_CAPACITY");
            apply!(
                awareness_messages_per_second,
                "COLLABORATION_AWARENESS_RATE"
            );
            apply!(updates_per_second, "COLLABORATION_UPDATE_RATE");
            if env::var_os("COLLABORATION_ACTOR_IDLE_TIMEOUT_MS").is_none()
                && let Some(value) = value.idle_timeout_ms
            {
                self.actor.idle_timeout = Duration::from_millis(value);
            }
            if env::var_os("COLLABORATION_ACTOR_COMMAND_TIMEOUT_MS").is_none()
                && let Some(value) = value.command_timeout_ms
            {
                self.actor.command_timeout = Duration::from_millis(value);
            }
        }
        if let Some(value) = &overrides.workers {
            macro_rules! duration {
                ($field:ident, $source:ident, $env:literal) => {
                    if env::var_os($env).is_none()
                        && let Some(value) = value.$source
                    {
                        self.workers.$field = Duration::from_millis(value);
                    }
                };
            }
            duration!(
                poll_interval,
                poll_interval_ms,
                "COLLABORATION_WORKER_POLL_MS"
            );
            duration!(
                operation_timeout,
                operation_timeout_ms,
                "COLLABORATION_WORKER_TIMEOUT_MS"
            );
            duration!(
                projection_lease,
                projection_lease_ms,
                "COLLABORATION_PROJECTION_LEASE_MS"
            );
            duration!(
                automatic_version_interval,
                automatic_version_interval_ms,
                "COLLABORATION_AUTOMATIC_VERSION_INTERVAL_MS"
            );
            if env::var_os("COLLABORATION_SNAPSHOT_UPDATE_THRESHOLD").is_none()
                && let Some(v) = value.snapshot_update_threshold
            {
                self.workers.snapshot_update_threshold = v;
            }
            if env::var_os("COLLABORATION_SNAPSHOT_BYTE_THRESHOLD").is_none()
                && let Some(v) = value.snapshot_byte_threshold
            {
                self.workers.snapshot_byte_threshold = v;
            }
            if env::var_os("COLLABORATION_OUTBOX_BATCH_SIZE").is_none()
                && let Some(v) = value.outbox_batch_size
            {
                self.workers.outbox_batch_size = v;
            }
        }
        if let Some(value) = &overrides.rpc {
            if env::var_os("COLLABORATION_RPC_ADDRESS").is_none()
                && let Some(raw) = &value.address
            {
                self.rpc.address = raw.parse().map_err(|error| {
                    ServiceError::invalid_input("Nacos rpc.address must be an IP socket address")
                        .with_source(error)
                })?;
            }
            if env::var_os("COLLABORATION_RPC_ADVERTISED_ADDRESS").is_none()
                && let Some(raw) = &value.advertised_address
            {
                validate_socket_endpoint(raw).map_err(|error| {
                    ServiceError::invalid_input("Nacos rpc.advertised_address must be host:port")
                        .with_source(error)
                })?;
                self.rpc.advertised_address.clone_from(raw);
            }
            if env::var_os("COLLABORATION_RPC_SERVICE_NAME").is_none()
                && let Some(raw) = &value.service_name
            {
                self.rpc.service_name = required("Nacos rpc.service_name", raw)?;
            }
            if env::var_os("COLLABORATION_RPC_REQUEST_TIMEOUT_MS").is_none()
                && let Some(v) = value.request_timeout_ms
            {
                self.rpc.request_timeout = Duration::from_millis(v);
            }
        }
        if let Some(value) = &overrides.admin {
            if env::var_os("COLLABORATION_ADMIN_ADDRESS").is_none()
                && let Some(raw) = &value.address
            {
                self.admin.address = raw.parse().map_err(|error| {
                    ServiceError::invalid_input("Nacos admin.address must be an IP socket address")
                        .with_source(error)
                })?;
            }
            if env::var_os("COLLABORATION_ADMIN_REQUEST_TIMEOUT_MS").is_none()
                && let Some(v) = value.request_timeout_ms
            {
                self.admin.request_timeout = Duration::from_millis(v);
            }
        }
        if let Some(value) = &overrides.telemetry {
            if env::var_os("COLLABORATION_OTLP_ENDPOINT").is_none()
                && let Some(raw) = &value.otlp_endpoint
            {
                self.telemetry.otlp_endpoint = Some(parse_url(
                    "Nacos telemetry.otlp_endpoint",
                    raw,
                    &["http", "https"],
                    false,
                )?);
            }
            if env::var_os("COLLABORATION_OTLP_EXPORT_TIMEOUT_MS").is_none()
                && let Some(v) = value.export_timeout_ms
            {
                self.telemetry.export_timeout = Duration::from_millis(v);
            }
            if env::var_os("COLLABORATION_TELEMETRY_SHUTDOWN_TIMEOUT_MS").is_none()
                && let Some(v) = value.shutdown_timeout_ms
            {
                self.telemetry.shutdown_timeout = Duration::from_millis(v);
            }
        }
        if let Some(value) = &overrides.postgres {
            if env::var_os("COLLABORATION_POSTGRES_MAX_CONNECTIONS").is_none()
                && let Some(v) = value.max_connections
            {
                self.postgres.max_connections = v;
            }
            if env::var_os("COLLABORATION_POSTGRES_CONNECT_TIMEOUT_MS").is_none()
                && let Some(v) = value.connect_timeout_ms
            {
                self.postgres.connect_timeout = Duration::from_millis(v);
            }
            if env::var_os("COLLABORATION_POSTGRES_ACQUIRE_TIMEOUT_MS").is_none()
                && let Some(v) = value.acquire_timeout_ms
            {
                self.postgres.acquire_timeout = Duration::from_millis(v);
            }
            if env::var_os("COLLABORATION_POSTGRES_OPERATION_TIMEOUT_MS").is_none()
                && let Some(v) = value.operation_timeout_ms
            {
                self.postgres.operation_timeout = Duration::from_millis(v);
            }
        }
        if let Some(value) = &overrides.redis {
            if env::var_os("COLLABORATION_REDIS_PREFIX").is_none()
                && let Some(raw) = &value.prefix
            {
                self.redis.prefix = required("Nacos redis.prefix", raw)?;
            }
            if env::var_os("COLLABORATION_REDIS_OPERATION_TIMEOUT_MS").is_none()
                && let Some(v) = value.operation_timeout_ms
            {
                self.redis.operation_timeout = Duration::from_millis(v);
            }
        }
        if let Some(value) = &overrides.nats {
            if env::var_os("COLLABORATION_NATS_SERVERS").is_none()
                && let Some(servers) = &value.servers
            {
                for server in servers {
                    parse_url("Nacos nats.servers", server, &["nats", "tls"], false)?;
                }
                self.nats.servers.clone_from(servers);
            }
            macro_rules! text {
                ($field:ident, $env:literal) => {
                    if env::var_os($env).is_none()
                        && let Some(raw) = &value.$field
                    {
                        self.nats.$field =
                            required(concat!("Nacos nats.", stringify!($field)), raw)?;
                    }
                };
            }
            text!(name, "COLLABORATION_NATS_NAME");
            text!(stream, "COLLABORATION_NATS_STREAM");
            text!(permission_stream, "COLLABORATION_NATS_PERMISSION_STREAM");
            text!(update_subject, "COLLABORATION_NATS_UPDATE_SUBJECT");
            text!(
                invalidation_subject,
                "COLLABORATION_NATS_INVALIDATION_SUBJECT"
            );
            text!(permission_subject, "COLLABORATION_NATS_PERMISSION_SUBJECT");
            if env::var_os("COLLABORATION_NATS_CONNECT_TIMEOUT_MS").is_none()
                && let Some(v) = value.connect_timeout_ms
            {
                self.nats.connect_timeout = Duration::from_millis(v);
            }
            if env::var_os("COLLABORATION_NATS_OPERATION_TIMEOUT_MS").is_none()
                && let Some(v) = value.operation_timeout_ms
            {
                self.nats.operation_timeout = Duration::from_millis(v);
            }
        }
        if let Some(value) = &overrides.etcd {
            if env::var_os("COLLABORATION_ETCD_ENDPOINTS").is_none()
                && let Some(endpoints) = &value.endpoints
            {
                for endpoint in endpoints {
                    parse_url("Nacos etcd.endpoints", endpoint, &["http", "https"], false)?;
                }
                self.etcd.endpoints.clone_from(endpoints);
            }
            if env::var_os("COLLABORATION_ETCD_PREFIX").is_none()
                && let Some(raw) = &value.prefix
            {
                self.etcd.prefix = required("Nacos etcd.prefix", raw)?;
            }
            if env::var_os("COLLABORATION_ETCD_CONNECT_TIMEOUT_MS").is_none()
                && let Some(v) = value.connect_timeout_ms
            {
                self.etcd.connect_timeout = Duration::from_millis(v);
            }
            if env::var_os("COLLABORATION_ETCD_REQUEST_TIMEOUT_MS").is_none()
                && let Some(v) = value.request_timeout_ms
            {
                self.etcd.request_timeout = Duration::from_millis(v);
            }
            if env::var_os("COLLABORATION_ETCD_LEASE_TTL_MS").is_none()
                && let Some(v) = value.lease_ttl_ms
            {
                self.etcd.lease_ttl = Duration::from_millis(v);
            }
        }
        if let Some(value) = &overrides.knowledge {
            if env::var_os("COLLABORATION_KNOWLEDGE_SERVICE_NAME").is_none()
                && let Some(raw) = &value.service_name
            {
                self.knowledge.service_name = required("Nacos knowledge.service_name", raw)?;
            }
            if env::var_os("COLLABORATION_KNOWLEDGE_REQUEST_TIMEOUT_MS").is_none()
                && let Some(v) = value.request_timeout_ms
            {
                self.knowledge.request_timeout = Duration::from_millis(v);
            }
        }
        validate_log_level(&self.telemetry.log_level)?;
        ActorLimits::from_config(&self.actor, &self.public)?;
        self.nats.validate_protocol_contract()?;
        if self.ticket.ttl.is_zero() || self.ticket.ttl > Duration::from_millis(MAX_TICKET_TTL_MS) {
            return Err(ServiceError::invalid_input("ticket TTL is invalid"));
        }
        Ok(())
    }

    /// Loads and strictly validates the complete Collaboration process configuration.
    ///
    /// # Errors
    ///
    /// Returns an invalid-input error when any environment value is missing, malformed, unsafe
    /// for the selected environment, or outside its documented boundary.
    // Keeping every environment key in this constructor makes the accepted configuration surface
    // directly auditable and prevents hidden secondary loaders.
    #[allow(clippy::too_many_lines)]
    pub fn from_environment() -> Result<Self> {
        let environment = environment()?;
        let public = public_config(environment)?;
        let telemetry = telemetry_config(environment)?;
        let rpc_tls = tls_config("COLLABORATION_RPC_TLS")?;
        let postgres_tls = tls_config("COLLABORATION_POSTGRES_TLS")?;
        let nats_tls = tls_config("COLLABORATION_NATS_TLS")?;
        let etcd_tls = tls_config("COLLABORATION_ETCD_TLS")?;
        let knowledge_tls = tls_config("COLLABORATION_KNOWLEDGE_TLS")?;
        let redis_url = parse_url(
            "COLLABORATION_REDIS_URL",
            &value("COLLABORATION_REDIS_URL", "redis://127.0.0.1:6379/1"),
            &["redis", "rediss"],
            true,
        )?;
        let nats_servers = comma_list(
            "COLLABORATION_NATS_SERVERS",
            &value("COLLABORATION_NATS_SERVERS", "nats://127.0.0.1:4222"),
        )?;
        for server in &nats_servers {
            parse_url(
                "COLLABORATION_NATS_SERVERS",
                server,
                &["nats", "tls"],
                false,
            )?;
        }
        let etcd_endpoints = comma_list(
            "COLLABORATION_ETCD_ENDPOINTS",
            &value("COLLABORATION_ETCD_ENDPOINTS", "http://127.0.0.1:2379"),
        )?;
        for endpoint in &etcd_endpoints {
            parse_url(
                "COLLABORATION_ETCD_ENDPOINTS",
                endpoint,
                &["http", "https"],
                false,
            )?;
        }
        let nats_token = optional("COLLABORATION_NATS_TOKEN");
        let nats_username = optional("COLLABORATION_NATS_USERNAME");
        let nats_password = optional("COLLABORATION_NATS_PASSWORD");
        if nats_token.is_some() && (nats_username.is_some() || nats_password.is_some()) {
            return Err(ServiceError::invalid_input(
                "NATS token cannot be combined with username/password",
            ));
        }
        if nats_username.is_some() != nats_password.is_some() {
            return Err(ServiceError::invalid_input(
                "NATS username and password must be configured together",
            ));
        }
        let etcd_username = optional("COLLABORATION_ETCD_USERNAME");
        let etcd_password = optional("COLLABORATION_ETCD_PASSWORD");
        if etcd_username.is_some() != etcd_password.is_some() {
            return Err(ServiceError::invalid_input(
                "Etcd username and password must be configured together",
            ));
        }
        if environment == Environment::Production {
            validate_production(
                &redis_url,
                &nats_servers,
                &etcd_endpoints,
                &rpc_tls,
                &postgres_tls,
                &nats_tls,
                &etcd_tls,
                &knowledge_tls,
            )?;
        }
        Ok(Self {
            environment,
            remote: None,
            instance_id: required(
                "COLLABORATION_INSTANCE_ID",
                &value("COLLABORATION_INSTANCE_ID", "collaboration-local"),
            )?,
            shutdown_timeout: duration(
                "COLLABORATION_SHUTDOWN_TIMEOUT_MS",
                30_000,
                1_000,
                120_000,
            )?,
            public,
            rpc: RpcConfig {
                address: address("COLLABORATION_RPC_ADDRESS", "0.0.0.0:8883")?,
                advertised_address: advertised_address(
                    "COLLABORATION_RPC_ADVERTISED_ADDRESS",
                    "127.0.0.1:8883",
                    environment,
                )?,
                service_name: required(
                    "COLLABORATION_RPC_SERVICE_NAME",
                    &value(
                        "COLLABORATION_RPC_SERVICE_NAME",
                        "knowledge-core.collaboration",
                    ),
                )?,
                request_timeout: duration(
                    "COLLABORATION_RPC_REQUEST_TIMEOUT_MS",
                    5_000,
                    100,
                    60_000,
                )?,
                tls: rpc_tls,
            },
            admin: AdminConfig {
                address: address("COLLABORATION_ADMIN_ADDRESS", "0.0.0.0:8084")?,
                request_timeout: duration(
                    "COLLABORATION_ADMIN_REQUEST_TIMEOUT_MS",
                    5_000,
                    100,
                    30_000,
                )?,
            },
            telemetry,
            postgres: PostgresConfig {
                url: required(
                    "COLLABORATION_POSTGRES_URL",
                    &value(
                        "COLLABORATION_POSTGRES_URL",
                        "postgres://knowledge_core@127.0.0.1:5432/knowledge_core",
                    ),
                )?,
                max_connections: integer_as::<u32>(
                    "COLLABORATION_POSTGRES_MAX_CONNECTIONS",
                    20,
                    1,
                    200,
                )?,
                connect_timeout: duration(
                    "COLLABORATION_POSTGRES_CONNECT_TIMEOUT_MS",
                    5_000,
                    100,
                    60_000,
                )?,
                acquire_timeout: duration(
                    "COLLABORATION_POSTGRES_ACQUIRE_TIMEOUT_MS",
                    5_000,
                    100,
                    60_000,
                )?,
                operation_timeout: duration(
                    "COLLABORATION_POSTGRES_OPERATION_TIMEOUT_MS",
                    30_000,
                    1_000,
                    300_000,
                )?,
                tls: postgres_tls,
            },
            redis: RedisConfig {
                url: redis_url,
                prefix: required(
                    "COLLABORATION_REDIS_PREFIX",
                    &value("COLLABORATION_REDIS_PREFIX", "knowledge-core:collaboration"),
                )?,
                operation_timeout: duration(
                    "COLLABORATION_REDIS_OPERATION_TIMEOUT_MS",
                    3_000,
                    100,
                    30_000,
                )?,
            },
            nats: NatsConfig {
                servers: nats_servers,
                name: required(
                    "COLLABORATION_NATS_NAME",
                    &value("COLLABORATION_NATS_NAME", "knowledge-core.collaboration"),
                )?,
                stream: required(
                    "COLLABORATION_NATS_STREAM",
                    &value("COLLABORATION_NATS_STREAM", "KNOWLEDGE_CORE_EVENTS"),
                )?,
                permission_stream: required(
                    "COLLABORATION_NATS_PERMISSION_STREAM",
                    &value(
                        "COLLABORATION_NATS_PERMISSION_STREAM",
                        "KNOWLEDGE_CORE_PERMISSIONS",
                    ),
                )?,
                update_subject: protocol_subject(
                    "COLLABORATION_NATS_UPDATE_SUBJECT",
                    NATS_UPDATE_SUBJECT,
                )?,
                invalidation_subject: protocol_subject(
                    "COLLABORATION_NATS_INVALIDATION_SUBJECT",
                    NATS_INVALIDATION_SUBJECT,
                )?,
                permission_subject: protocol_subject(
                    "COLLABORATION_NATS_PERMISSION_SUBJECT",
                    NATS_PERMISSION_SUBJECT,
                )?,
                connect_timeout: duration(
                    "COLLABORATION_NATS_CONNECT_TIMEOUT_MS",
                    5_000,
                    100,
                    60_000,
                )?,
                operation_timeout: duration(
                    "COLLABORATION_NATS_OPERATION_TIMEOUT_MS",
                    5_000,
                    100,
                    60_000,
                )?,
                token: nats_token,
                username: nats_username,
                password: nats_password,
                tls: nats_tls,
            },
            etcd: EtcdConfig {
                endpoints: etcd_endpoints,
                prefix: required(
                    "COLLABORATION_ETCD_PREFIX",
                    &value("COLLABORATION_ETCD_PREFIX", DEFAULT_ETCD_PREFIX),
                )?,
                username: etcd_username,
                password: etcd_password,
                connect_timeout: duration(
                    "COLLABORATION_ETCD_CONNECT_TIMEOUT_MS",
                    5_000,
                    100,
                    60_000,
                )?,
                request_timeout: duration(
                    "COLLABORATION_ETCD_REQUEST_TIMEOUT_MS",
                    5_000,
                    100,
                    60_000,
                )?,
                lease_ttl: duration("COLLABORATION_ETCD_LEASE_TTL_MS", 60_000, 5_000, 300_000)?,
                tls: etcd_tls,
            },
            knowledge: KnowledgeConfig {
                service_name: required(
                    "COLLABORATION_KNOWLEDGE_SERVICE_NAME",
                    &value(
                        "COLLABORATION_KNOWLEDGE_SERVICE_NAME",
                        "knowledge-core.knowledge",
                    ),
                )?,
                request_timeout: duration(
                    "COLLABORATION_KNOWLEDGE_REQUEST_TIMEOUT_MS",
                    5_000,
                    100,
                    60_000,
                )?,
                tls: knowledge_tls,
            },
            ticket: TicketConfig {
                ttl: duration(
                    "COLLABORATION_TICKET_TTL_MS",
                    30_000,
                    1_000,
                    MAX_TICKET_TTL_MS,
                )?,
                subprotocol: required(
                    "COLLABORATION_WEBSOCKET_SUBPROTOCOL",
                    &value(
                        "COLLABORATION_WEBSOCKET_SUBPROTOCOL",
                        "knowledge-core-yjs-v1",
                    ),
                )?,
                fragment: required(
                    "COLLABORATION_FRAGMENT",
                    &value("COLLABORATION_FRAGMENT", "default"),
                )?,
            },
            actor: ActorConfig {
                command_capacity: integer_as::<usize>(
                    "COLLABORATION_ACTOR_COMMAND_CAPACITY",
                    256,
                    8,
                    65_536,
                )?,
                outbound_capacity: integer_as::<usize>(
                    "COLLABORATION_OUTBOUND_CAPACITY",
                    128,
                    8,
                    65_536,
                )?,
                idle_timeout: duration(
                    "COLLABORATION_ACTOR_IDLE_TIMEOUT_MS",
                    60_000,
                    1_000,
                    3_600_000,
                )?,
                command_timeout: duration(
                    "COLLABORATION_ACTOR_COMMAND_TIMEOUT_MS",
                    30_000,
                    1_000,
                    300_000,
                )?,
                awareness_messages_per_second: integer_as::<u32>(
                    "COLLABORATION_AWARENESS_RATE",
                    20,
                    1,
                    1_000,
                )?,
                updates_per_second: integer_as::<u32>("COLLABORATION_UPDATE_RATE", 50, 1, 10_000)?,
            },
            workers: WorkerConfig {
                poll_interval: duration("COLLABORATION_WORKER_POLL_MS", 1_000, 50, 60_000)?,
                operation_timeout: duration(
                    "COLLABORATION_WORKER_TIMEOUT_MS",
                    30_000,
                    1_000,
                    300_000,
                )?,
                projection_lease: duration(
                    "COLLABORATION_PROJECTION_LEASE_MS",
                    30_000,
                    1_000,
                    300_000,
                )?,
                snapshot_update_threshold: integer_as::<i64>(
                    "COLLABORATION_SNAPSHOT_UPDATE_THRESHOLD",
                    500,
                    1,
                    100_000,
                )?,
                snapshot_byte_threshold: integer_as::<i64>(
                    "COLLABORATION_SNAPSHOT_BYTE_THRESHOLD",
                    8 << 20,
                    1_024,
                    1 << 30,
                )?,
                automatic_version_interval: duration(
                    "COLLABORATION_AUTOMATIC_VERSION_INTERVAL_MS",
                    30 * 60_000,
                    60_000,
                    30 * 24 * 60 * 60_000,
                )?,
                outbox_batch_size: integer_as::<i64>(
                    "COLLABORATION_OUTBOX_BATCH_SIZE",
                    50,
                    1,
                    1_000,
                )?,
            },
        })
    }
}

fn telemetry_config(environment: Environment) -> Result<TelemetryConfig> {
    let log_level = value("COLLABORATION_LOG_LEVEL", "info");
    validate_log_level(&log_level)?;
    let otlp_endpoint = optional("COLLABORATION_OTLP_ENDPOINT")
        .map(|value| {
            parse_url(
                "COLLABORATION_OTLP_ENDPOINT",
                &value,
                &["http", "https"],
                false,
            )
        })
        .transpose()?;
    if environment == Environment::Production
        && otlp_endpoint
            .as_ref()
            .is_none_or(|endpoint| endpoint.scheme() != "https")
    {
        return Err(ServiceError::invalid_input(
            "production OTLP endpoint is required and must use https",
        ));
    }
    Ok(TelemetryConfig {
        log_level,
        health_check_requests: boolean("COLLABORATION_LOG_HEALTH_CHECK_REQUESTS", true)?,
        otlp_endpoint,
        export_timeout: duration("COLLABORATION_OTLP_EXPORT_TIMEOUT_MS", 5_000, 100, 60_000)?,
        shutdown_timeout: duration(
            "COLLABORATION_TELEMETRY_SHUTDOWN_TIMEOUT_MS",
            5_000,
            100,
            30_000,
        )?,
    })
}

fn environment() -> Result<Environment> {
    match value("COLLABORATION_ENVIRONMENT", "development").as_str() {
        "development" => Ok(Environment::Development),
        "production" => Ok(Environment::Production),
        "test" => Ok(Environment::Test),
        _ => Err(ServiceError::invalid_input(
            "COLLABORATION_ENVIRONMENT must be development, production, or test",
        )),
    }
}

fn public_config(environment: Environment) -> Result<PublicConfig> {
    let origins = comma_list(
        "COLLABORATION_ALLOWED_ORIGINS",
        &value("COLLABORATION_ALLOWED_ORIGINS", "http://localhost:3000"),
    )?;
    let allowed_origins = origins
        .into_iter()
        .map(|origin| exact_origin(&origin))
        .collect::<Result<Vec<_>>>()?;
    if environment == Environment::Production && allowed_origins.is_empty() {
        return Err(ServiceError::invalid_input(
            "production allowed origins must not be empty",
        ));
    }
    Ok(PublicConfig {
        address: address("COLLABORATION_PUBLIC_ADDRESS", "0.0.0.0:8091")?,
        allowed_origins,
        max_frame_bytes: integer_as::<usize>(
            "COLLABORATION_MAX_FRAME_BYTES",
            (1 << 20) + 16_384,
            1_024,
            32 << 20,
        )?,
        max_update_bytes: integer_as::<usize>(
            "COLLABORATION_MAX_UPDATE_BYTES",
            1 << 20,
            1_024,
            16 << 20,
        )?,
        max_document_bytes: integer_as::<usize>(
            "COLLABORATION_MAX_DOCUMENT_BYTES",
            16 << 20,
            1_024,
            1 << 30,
        )?,
        max_awareness_bytes: integer_as::<usize>(
            "COLLABORATION_MAX_AWARENESS_BYTES",
            64 << 10,
            256,
            1 << 20,
        )?,
        max_connections: integer_as::<usize>(
            "COLLABORATION_MAX_CONNECTIONS",
            20_000,
            1,
            1_000_000,
        )?,
        max_connections_per_document: integer_as::<usize>(
            "COLLABORATION_MAX_CONNECTIONS_PER_DOCUMENT",
            200,
            1,
            10_000,
        )?,
        handshakes_per_second: integer_as::<u32>(
            "COLLABORATION_HANDSHAKES_PER_SECOND",
            200,
            1,
            100_000,
        )?,
        handshake_burst: integer_as::<u32>("COLLABORATION_HANDSHAKE_BURST", 400, 1, 200_000)?,
        handshake_timeout: duration("COLLABORATION_HANDSHAKE_TIMEOUT_MS", 5_000, 100, 60_000)?,
    })
}

#[allow(clippy::too_many_arguments)]
fn validate_production(
    redis_url: &Url,
    nats_servers: &[String],
    etcd_endpoints: &[String],
    rpc_tls: &TlsConfig,
    postgres_tls: &TlsConfig,
    nats_tls: &TlsConfig,
    etcd_tls: &TlsConfig,
    knowledge_tls: &TlsConfig,
) -> Result<()> {
    if redis_url.scheme() != "rediss" {
        return Err(ServiceError::invalid_input(
            "production Redis URL must use rediss",
        ));
    }
    if nats_servers
        .iter()
        .any(|value| !value.starts_with("tls://"))
        || !nats_tls.enabled
    {
        return Err(ServiceError::invalid_input(
            "production NATS connections require TLS",
        ));
    }
    if etcd_endpoints
        .iter()
        .any(|value| !value.starts_with("https://"))
        || !etcd_tls.enabled
    {
        return Err(ServiceError::invalid_input(
            "production Etcd connections require TLS",
        ));
    }
    for (name, tls) in [
        ("RPC", rpc_tls),
        ("PostgreSQL", postgres_tls),
        ("Knowledge RPC", knowledge_tls),
    ] {
        if !tls.enabled || tls.ca_file.is_none() {
            return Err(ServiceError::invalid_input(format!(
                "production {name} requires verified TLS"
            )));
        }
    }
    if rpc_tls.cert_file.is_none()
        || rpc_tls.key_file.is_none()
        || knowledge_tls.cert_file.is_none()
        || knowledge_tls.key_file.is_none()
    {
        return Err(ServiceError::invalid_input(
            "production RPC traffic requires mutual TLS",
        ));
    }
    Ok(())
}

fn tls_config(prefix: &str) -> Result<TlsConfig> {
    let enabled = boolean(&format!("{prefix}_ENABLED"), false)?;
    let ca_file = optional(&format!("{prefix}_CA_FILE")).map(PathBuf::from);
    let cert_file = optional(&format!("{prefix}_CERT_FILE")).map(PathBuf::from);
    let key_file = optional(&format!("{prefix}_KEY_FILE")).map(PathBuf::from);
    let server_name = optional(&format!("{prefix}_SERVER_NAME"));
    if !enabled
        && (ca_file.is_some() || cert_file.is_some() || key_file.is_some() || server_name.is_some())
    {
        return Err(ServiceError::invalid_input(format!(
            "{prefix} settings cannot be set while TLS is disabled"
        )));
    }
    if cert_file.is_some() != key_file.is_some() {
        return Err(ServiceError::invalid_input(format!(
            "{prefix} certificate and key must be configured together"
        )));
    }
    Ok(TlsConfig {
        enabled,
        ca_file,
        cert_file,
        key_file,
        server_name,
    })
}

fn value(name: &str, fallback: &str) -> String {
    env::var(name).unwrap_or_else(|_| fallback.to_owned())
}

fn optional(name: &str) -> Option<String> {
    env::var(name)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}

fn required(name: &str, value: &str) -> Result<String> {
    let value = value.trim();
    if value.is_empty() {
        return Err(ServiceError::invalid_input(format!("{name} is required")));
    }
    Ok(value.to_owned())
}

fn protocol_subject(name: &str, expected: &'static str) -> Result<String> {
    let configured = value(name, expected);
    validate_protocol_subject(name, &configured, expected)?;
    Ok(expected.to_owned())
}

fn validate_protocol_subject(name: &str, configured: &str, expected: &str) -> Result<()> {
    if configured != expected {
        return Err(ServiceError::invalid_input(format!(
            "{name} is fixed by the cross-service NATS protocol and must equal {expected}"
        )));
    }
    Ok(())
}

fn address(name: &str, fallback: &str) -> Result<SocketAddr> {
    let raw = value(name, fallback);
    raw.parse().map_err(|error| {
        ServiceError::invalid_input(format!("{name} must be an IP socket address"))
            .with_source(error)
    })
}

fn advertised_address(name: &str, fallback: &str, environment: Environment) -> Result<String> {
    let raw = value(name, fallback);
    validate_socket_endpoint(&raw).map_err(|error| {
        ServiceError::invalid_input(format!("{name} must be host:port")).with_source(error)
    })?;
    let parsed = Url::parse(&format!("tcp://{raw}")).map_err(|error| {
        ServiceError::invalid_input(format!("{name} must be host:port")).with_source(error)
    })?;
    if !parsed.username().is_empty()
        || parsed.password().is_some()
        || !parsed.path().is_empty()
        || parsed.query().is_some()
        || parsed.fragment().is_some()
    {
        return Err(ServiceError::invalid_input(format!(
            "{name} must be host:port"
        )));
    }
    let host = parsed
        .host()
        .ok_or_else(|| ServiceError::invalid_input(format!("{name} must include a host")))?;
    match host {
        url::Host::Ipv4(address) => {
            if address.is_unspecified()
                || (environment == Environment::Production && address.is_loopback())
            {
                return Err(ServiceError::invalid_input(format!(
                    "{name} is not advertisable"
                )));
            }
        }
        url::Host::Ipv6(address) => {
            if address.is_unspecified()
                || (environment == Environment::Production && address.is_loopback())
            {
                return Err(ServiceError::invalid_input(format!(
                    "{name} is not advertisable"
                )));
            }
        }
        url::Host::Domain(domain) => {
            if environment == Environment::Production && domain.eq_ignore_ascii_case("localhost") {
                return Err(ServiceError::invalid_input(format!(
                    "{name} is not advertisable"
                )));
            }
        }
    }
    Ok(raw)
}

fn parse_url(name: &str, value: &str, schemes: &[&str], allow_credentials: bool) -> Result<Url> {
    let parsed = Url::parse(value).map_err(|error| {
        ServiceError::invalid_input(format!("{name} must be an absolute URL")).with_source(error)
    })?;
    if !schemes.contains(&parsed.scheme())
        || parsed.host_str().is_none()
        || (!allow_credentials && (!parsed.username().is_empty() || parsed.password().is_some()))
    {
        return Err(ServiceError::invalid_input(format!(
            "{name} has an unsupported URL"
        )));
    }
    Ok(parsed)
}

fn exact_origin(value: &str) -> Result<String> {
    let parsed = parse_url(
        "COLLABORATION_ALLOWED_ORIGINS",
        value,
        &["http", "https"],
        false,
    )?;
    if parsed.path() != "/" || parsed.query().is_some() || parsed.fragment().is_some() {
        return Err(ServiceError::invalid_input(
            "COLLABORATION_ALLOWED_ORIGINS must contain exact origins",
        ));
    }
    Ok(parsed.origin().ascii_serialization())
}

fn comma_list(name: &str, value: &str) -> Result<Vec<String>> {
    if value.trim().is_empty() {
        return Ok(Vec::new());
    }
    let entries = value
        .split(',')
        .map(str::trim)
        .map(ToOwned::to_owned)
        .collect::<Vec<_>>();
    if entries.iter().any(String::is_empty) {
        return Err(ServiceError::invalid_input(format!(
            "{name} contains an empty value"
        )));
    }
    Ok(entries)
}

fn duration(name: &str, fallback_ms: u64, minimum_ms: u64, maximum_ms: u64) -> Result<Duration> {
    Ok(Duration::from_millis(integer(
        name,
        fallback_ms,
        minimum_ms,
        maximum_ms,
    )?))
}

fn integer(name: &str, fallback: u64, minimum: u64, maximum: u64) -> Result<u64> {
    let raw = value(name, &fallback.to_string());
    if raw.is_empty() || !raw.bytes().all(|value| value.is_ascii_digit()) {
        return Err(ServiceError::invalid_input(format!(
            "{name} must be an integer"
        )));
    }
    let parsed = raw.parse::<u64>().map_err(|error| {
        ServiceError::invalid_input(format!("{name} is outside the supported range"))
            .with_source(error)
    })?;
    if !(minimum..=maximum).contains(&parsed) {
        return Err(ServiceError::invalid_input(format!(
            "{name} must be between {minimum} and {maximum}"
        )));
    }
    Ok(parsed)
}

fn integer_as<T>(name: &str, fallback: u64, minimum: u64, maximum: u64) -> Result<T>
where
    T: TryFrom<u64>,
    T::Error: Into<anyhow::Error>,
{
    T::try_from(integer(name, fallback, minimum, maximum)?).map_err(|error| {
        ServiceError::invalid_input(format!("{name} is unsupported on this platform"))
            .with_source(error)
    })
}

fn boolean(name: &str, fallback: bool) -> Result<bool> {
    match env::var(name) {
        Err(_) => Ok(fallback),
        Ok(value) if value == "true" => Ok(true),
        Ok(value) if value == "false" => Ok(false),
        Ok(_) => Err(ServiceError::invalid_input(format!(
            "{name} must be true or false"
        ))),
    }
}

#[cfg(test)]
mod tests {
    use super::{
        DEFAULT_ETCD_PREFIX, Environment, NATS_INVALIDATION_SUBJECT, NATS_PERMISSION_SUBJECT,
        NATS_UPDATE_SUBJECT, advertised_address, validate_protocol_subject,
    };

    #[test]
    fn default_etcd_prefix_matches_go_registry_contract() {
        assert_eq!(DEFAULT_ETCD_PREFIX, "/knowledge-core/development/registry");
    }

    #[test]
    fn nats_protocol_subjects_are_exact_and_reject_configuration_drift() {
        for (name, expected) in [
            ("COLLABORATION_NATS_UPDATE_SUBJECT", NATS_UPDATE_SUBJECT),
            (
                "COLLABORATION_NATS_INVALIDATION_SUBJECT",
                NATS_INVALIDATION_SUBJECT,
            ),
            (
                "COLLABORATION_NATS_PERMISSION_SUBJECT",
                NATS_PERMISSION_SUBJECT,
            ),
        ] {
            validate_protocol_subject(name, expected, expected).expect("fixed protocol subject");
            assert!(validate_protocol_subject(name, "drifted.subject", expected).is_err());
            assert!(validate_protocol_subject(name, &format!("{expected} "), expected).is_err());
        }
    }

    #[test]
    fn advertised_rpc_address_accepts_strict_ip_and_hostname_endpoints() {
        for address in ["127.0.0.1:8883", "collaboration:8883", "[::1]:8883"] {
            assert_eq!(
                advertised_address(
                    "KNOWLEDGE_CORE_TEST_UNUSED_ADVERTISED_ADDRESS",
                    address,
                    Environment::Development,
                )
                .expect("valid advertised address"),
                address
            );
        }
        for address in [
            "collaboration",
            "collaboration:0",
            "collaboration:8883/path",
            "user@collaboration:8883",
        ] {
            assert!(
                advertised_address(
                    "KNOWLEDGE_CORE_TEST_UNUSED_ADVERTISED_ADDRESS",
                    address,
                    Environment::Development,
                )
                .is_err(),
                "{address}"
            );
        }
    }

    #[test]
    fn production_advertised_rpc_address_rejects_localhost() {
        assert!(
            advertised_address(
                "KNOWLEDGE_CORE_TEST_UNUSED_ADVERTISED_ADDRESS",
                "localhost:8883",
                Environment::Production,
            )
            .is_err()
        );
    }
}
