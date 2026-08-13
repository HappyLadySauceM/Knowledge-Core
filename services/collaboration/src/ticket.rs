use std::{
    fmt,
    sync::{
        Arc,
        atomic::{AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};

use async_trait::async_trait;
use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use rand::{TryRngCore, rngs::OsRng};
use redis::aio::ConnectionManager;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use time::OffsetDateTime;
use tokio::time::timeout;

use crate::{
    config::{RedisConfig, TicketConfig},
    domain::{Access, Authorization, DocumentId, PublicUser, RequestContext},
    error::{ErrorCode, Result, ServiceError},
};

const TICKET_BYTES: usize = 32;
const CLAIMS_VERSION: u8 = 1;
const MAX_GENERATION_ATTEMPTS: usize = 4;

#[derive(Clone)]
pub struct OpaqueTicket(Arc<str>);

impl OpaqueTicket {
    pub fn expose(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for OpaqueTicket {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("[REDACTED]")
    }
}

#[derive(Clone, Debug)]
pub struct IssuedTicket {
    pub ticket: OpaqueTicket,
    pub expires_at: OffsetDateTime,
    pub session_expires_at: OffsetDateTime,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct TicketClaims {
    version: u8,
    pub document_id: DocumentId,
    pub actor: PublicUser,
    pub access: Access,
    pub permission_revision: i64,
    pub session_expires_at: OffsetDateTime,
}

impl TicketClaims {
    fn from_authorization(authorization: &Authorization) -> Result<Self> {
        authorization.actor.validate()?;
        if authorization.permission_revision <= 0 {
            return Err(ServiceError::invalid_input(
                "permission revision must be positive",
            ));
        }
        Ok(Self {
            version: CLAIMS_VERSION,
            document_id: authorization.document_id,
            actor: authorization.actor.clone(),
            access: authorization.access,
            permission_revision: authorization.permission_revision,
            session_expires_at: authorization.token_expires_at,
        })
    }

    fn validate(&self, expected_document: DocumentId, now: OffsetDateTime) -> Result<()> {
        self.actor.validate()?;
        if self.version != CLAIMS_VERSION
            || self.document_id != expected_document
            || self.permission_revision <= 0
            || self.session_expires_at <= now
        {
            return Err(ServiceError::unauthenticated());
        }
        Ok(())
    }
}

#[async_trait]
pub trait TicketBackend: Send + Sync {
    async fn put(
        &self,
        context: &RequestContext,
        digest: [u8; 32],
        value: Vec<u8>,
        ttl: Duration,
    ) -> Result<bool>;

    async fn take(&self, context: &RequestContext, digest: [u8; 32]) -> Result<Option<Vec<u8>>>;

    async fn ping(&self) -> Result<()>;
}

#[derive(Clone)]
pub struct TicketService {
    backend: Arc<dyn TicketBackend>,
    ttl_ms: Arc<AtomicU64>,
}

impl TicketService {
    /// Creates a ticket service with the configured one-time ticket lifetime.
    ///
    /// # Errors
    ///
    /// Returns an invalid-input error when the ticket lifetime is zero or exceeds one minute.
    pub fn new(backend: Arc<dyn TicketBackend>, config: &TicketConfig) -> Result<Self> {
        if config.ttl.is_zero() || config.ttl > Duration::from_mins(1) {
            return Err(ServiceError::invalid_input(
                "ticket TTL must be between one millisecond and 60 seconds",
            ));
        }
        Ok(Self {
            backend,
            ttl_ms: Arc::new(AtomicU64::new(
                u64::try_from(config.ttl.as_millis()).map_err(|_| {
                    ServiceError::invalid_input("ticket TTL exceeds the supported range")
                })?,
            )),
        })
    }

    /// Stores and returns a short-lived, opaque, single-use session ticket.
    ///
    /// # Errors
    ///
    /// Returns an error when authorization is invalid, expired, cannot be encoded, or the ticket
    /// backend cannot reserve a unique ticket before the request deadline.
    pub async fn issue(
        &self,
        context: &RequestContext,
        authorization: &Authorization,
    ) -> Result<IssuedTicket> {
        let now = OffsetDateTime::now_utc();
        let configured_expiry = now
            .checked_add(duration_to_time(Duration::from_millis(
                self.ttl_ms.load(Ordering::Acquire),
            ))?)
            .ok_or_else(|| ServiceError::internal(anyhow::anyhow!("ticket expiry overflow")))?;
        let expires_at = configured_expiry.min(authorization.token_expires_at);
        if expires_at <= now {
            return Err(ServiceError::unauthenticated());
        }
        let ttl = Duration::try_from(expires_at - now).map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("convert ticket TTL"))
        })?;
        let claims = TicketClaims::from_authorization(authorization)?;
        let value = serde_json::to_vec(&claims).map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("encode ticket claims"))
        })?;

        for _ in 0..MAX_GENERATION_ATTEMPTS {
            let mut bytes = [0_u8; TICKET_BYTES];
            OsRng.try_fill_bytes(&mut bytes).map_err(|error| {
                ServiceError::internal(anyhow::anyhow!(error).context("generate session ticket"))
            })?;
            let encoded = URL_SAFE_NO_PAD.encode(bytes);
            if self
                .backend
                .put(context, ticket_digest(&bytes), value.clone(), ttl)
                .await?
            {
                return Ok(IssuedTicket {
                    ticket: OpaqueTicket(Arc::from(encoded)),
                    expires_at,
                    session_expires_at: authorization.token_expires_at,
                });
            }
        }
        Err(ServiceError::internal(anyhow::anyhow!(
            "could not allocate a unique session ticket"
        )))
    }

    /// Atomically consumes and validates a ticket for the expected document.
    ///
    /// # Errors
    ///
    /// Returns an unauthenticated error for malformed, missing, expired, or mismatched tickets,
    /// and a dependency error when the backend cannot complete before the request deadline.
    pub async fn consume(
        &self,
        context: &RequestContext,
        ticket: &str,
        expected_document: DocumentId,
    ) -> Result<TicketClaims> {
        let bytes = decode_ticket(ticket)?;
        let Some(value) = self.backend.take(context, ticket_digest(&bytes)).await? else {
            return Err(ServiceError::unauthenticated());
        };
        let claims: TicketClaims =
            serde_json::from_slice(&value).map_err(|_| ServiceError::unauthenticated())?;
        claims.validate(expected_document, OffsetDateTime::now_utc())?;
        Ok(claims)
    }

    /// Checks the ticket backend using its configured operation timeout.
    ///
    /// # Errors
    ///
    /// Returns an error when the backend is unavailable or returns an invalid response.
    pub async fn ping(&self) -> Result<()> {
        self.backend.ping().await
    }

    pub(crate) fn set_ttl(&self, ttl: Duration) -> Result<()> {
        if ttl.is_zero() || ttl > Duration::from_mins(1) {
            return Err(ServiceError::invalid_input(
                "ticket TTL must be between one millisecond and 60 seconds",
            ));
        }
        let ttl_ms = u64::try_from(ttl.as_millis())
            .map_err(|_| ServiceError::invalid_input("ticket TTL exceeds the supported range"))?;
        self.ttl_ms.store(ttl_ms, Ordering::Release);
        Ok(())
    }
}

#[derive(Clone)]
pub struct RedisTicketBackend {
    connection: ConnectionManager,
    key_prefix: Arc<str>,
    operation_timeout: Duration,
}

impl RedisTicketBackend {
    /// Opens and verifies the Redis connection used for one-time tickets.
    ///
    /// # Errors
    ///
    /// Returns an error when the Redis URL is invalid or the connection cannot be established and
    /// verified within the configured timeout.
    pub async fn open(config: &RedisConfig) -> Result<Self> {
        let client = redis::Client::open(config.url.as_str()).map_err(|error| {
            ServiceError::invalid_input("COLLABORATION_REDIS_URL is invalid").with_source(error)
        })?;
        let connection = timeout(config.operation_timeout, ConnectionManager::new(client))
            .await
            .map_err(|_| dependency_timeout("connect Redis"))?
            .map_err(|error| dependency_error(error, "connect Redis"))?;
        let backend = Self {
            connection,
            key_prefix: Arc::from(format!("{}:ticket:", config.prefix)),
            operation_timeout: config.operation_timeout,
        };
        backend.ping().await?;
        Ok(backend)
    }

    fn key(&self, digest: &[u8; 32]) -> String {
        let mut key = String::with_capacity(self.key_prefix.len() + digest.len() * 2);
        key.push_str(&self.key_prefix);
        for byte in digest {
            use std::fmt::Write as _;
            write!(&mut key, "{byte:02x}").expect("writing to a String cannot fail");
        }
        key
    }

    async fn request_command<T>(
        &self,
        context: &RequestContext,
        command: &mut redis::Cmd,
        operation: &'static str,
    ) -> Result<T>
    where
        T: redis::FromRedisValue,
    {
        let budget = operation_budget(context, self.operation_timeout, operation)?;
        self.command(command, operation, budget).await
    }

    async fn command<T>(
        &self,
        command: &mut redis::Cmd,
        operation: &'static str,
        budget: OperationBudget,
    ) -> Result<T>
    where
        T: redis::FromRedisValue,
    {
        let mut connection = self.connection.clone();
        match timeout(budget.maximum_wait, command.query_async(&mut connection)).await {
            Ok(result) => result.map_err(|error| dependency_error(error, operation)),
            Err(error) if budget.request_deadline_limited => Err(request_deadline_error(
                operation,
                anyhow::Error::new(error).context("Redis operation exceeded request deadline"),
            )),
            Err(_) => Err(dependency_timeout(operation)),
        }
    }
}

#[derive(Clone, Copy, Debug)]
struct OperationBudget {
    maximum_wait: Duration,
    request_deadline_limited: bool,
}

#[async_trait]
impl TicketBackend for RedisTicketBackend {
    async fn put(
        &self,
        context: &RequestContext,
        digest: [u8; 32],
        value: Vec<u8>,
        ttl: Duration,
    ) -> Result<bool> {
        let milliseconds = u64::try_from(ttl.as_millis()).map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("convert Redis ticket TTL"))
        })?;
        if milliseconds == 0 {
            return Err(ServiceError::unauthenticated());
        }
        let response: Option<String> = self
            .request_command(
                context,
                redis::cmd("SET")
                    .arg(self.key(&digest))
                    .arg(value)
                    .arg("PX")
                    .arg(milliseconds)
                    .arg("NX"),
                "store Redis ticket",
            )
            .await?;
        Ok(response.is_some())
    }

    async fn take(&self, context: &RequestContext, digest: [u8; 32]) -> Result<Option<Vec<u8>>> {
        self.request_command(
            context,
            redis::cmd("GETDEL").arg(self.key(&digest)),
            "consume Redis ticket",
        )
        .await
    }

    async fn ping(&self) -> Result<()> {
        let response: String = self
            .command(
                &mut redis::cmd("PING"),
                "ping Redis",
                OperationBudget {
                    maximum_wait: self.operation_timeout,
                    request_deadline_limited: false,
                },
            )
            .await?;
        if response == "PONG" {
            Ok(())
        } else {
            Err(ServiceError::unavailable(anyhow::anyhow!(
                "Redis returned an unexpected ping response"
            )))
        }
    }
}

fn decode_ticket(value: &str) -> Result<[u8; TICKET_BYTES]> {
    if value.len() != 43 || value.trim() != value {
        return Err(ServiceError::unauthenticated());
    }
    let decoded = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| ServiceError::unauthenticated())?;
    let bytes: [u8; TICKET_BYTES] = decoded
        .try_into()
        .map_err(|_| ServiceError::unauthenticated())?;
    if URL_SAFE_NO_PAD.encode(bytes) != value {
        return Err(ServiceError::unauthenticated());
    }
    Ok(bytes)
}

fn ticket_digest(ticket: &[u8; TICKET_BYTES]) -> [u8; 32] {
    Sha256::digest(ticket).into()
}

fn duration_to_time(value: Duration) -> Result<time::Duration> {
    time::Duration::try_from(value).map_err(|error| {
        ServiceError::internal(anyhow::anyhow!(error).context("convert ticket duration"))
    })
}

fn operation_budget(
    context: &RequestContext,
    operation_timeout: Duration,
    operation: &'static str,
) -> Result<OperationBudget> {
    let Some(deadline) = context.deadline else {
        return Ok(OperationBudget {
            maximum_wait: operation_timeout,
            request_deadline_limited: false,
        });
    };
    let remaining = deadline
        .checked_duration_since(Instant::now())
        .filter(|remaining| !remaining.is_zero())
        .ok_or_else(|| {
            request_deadline_error(
                operation,
                anyhow::anyhow!("request deadline elapsed before Redis operation"),
            )
        })?;
    Ok(OperationBudget {
        maximum_wait: remaining.min(operation_timeout),
        request_deadline_limited: remaining <= operation_timeout,
    })
}

fn dependency_timeout(operation: &'static str) -> ServiceError {
    ServiceError::unavailable(anyhow::anyhow!("{operation} timed out"))
}

fn request_deadline_error(operation: &'static str, source: anyhow::Error) -> ServiceError {
    ServiceError::new(
        ErrorCode::Unavailable,
        "collaboration.deadline_exceeded",
        "request deadline exceeded",
    )
    .with_source(source.context(operation))
}

fn dependency_error(error: redis::RedisError, operation: &'static str) -> ServiceError {
    ServiceError::unavailable(anyhow::anyhow!(error).context(operation))
}

#[cfg(test)]
mod tests {
    use std::{
        collections::HashMap,
        sync::Arc,
        time::{Duration, Instant},
    };

    use async_trait::async_trait;
    use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
    use time::OffsetDateTime;
    use tokio::sync::Mutex;

    use super::{TicketBackend, TicketService, decode_ticket, operation_budget, ticket_digest};
    use crate::{
        config::TicketConfig,
        domain::{Access, Authorization, DocumentId, PublicUser, RequestContext},
        error::{ErrorCode, Result},
    };

    #[derive(Default)]
    struct MemoryBackend {
        values: Mutex<HashMap<[u8; 32], Vec<u8>>>,
    }

    #[async_trait]
    impl TicketBackend for MemoryBackend {
        async fn put(
            &self,
            _context: &RequestContext,
            digest: [u8; 32],
            value: Vec<u8>,
            _ttl: Duration,
        ) -> Result<bool> {
            let mut values = self.values.lock().await;
            if values.contains_key(&digest) {
                return Ok(false);
            }
            values.insert(digest, value);
            Ok(true)
        }

        async fn take(
            &self,
            _context: &RequestContext,
            digest: [u8; 32],
        ) -> Result<Option<Vec<u8>>> {
            Ok(self.values.lock().await.remove(&digest))
        }

        async fn ping(&self) -> Result<()> {
            Ok(())
        }
    }

    #[test]
    fn ticket_encoding_is_canonical_and_hashes_raw_entropy() {
        let raw = [7_u8; 32];
        let encoded = URL_SAFE_NO_PAD.encode(raw);
        assert_eq!(decode_ticket(&encoded).expect("canonical ticket"), raw);
        assert_eq!(ticket_digest(&raw).len(), 32);
        assert!(decode_ticket(&(encoded + "=")).is_err());
    }

    #[tokio::test]
    async fn concurrent_consumption_has_exactly_one_winner() {
        let backend = Arc::new(MemoryBackend::default());
        let service = TicketService::new(
            backend,
            &TicketConfig {
                ttl: Duration::from_secs(30),
                subprotocol: "knowledge-core-yjs-v1".to_owned(),
                fragment: "default".to_owned(),
            },
        )
        .expect("valid service");
        let document_id = DocumentId::new();
        let issue_context = request_context("issue-ticket");
        let issued = service
            .issue(
                &issue_context,
                &Authorization {
                    document_id,
                    actor: PublicUser {
                        id: 1,
                        username: "editor".to_owned(),
                        avatar: String::new(),
                    },
                    access: Access::Editor,
                    permission_revision: 1,
                    token_expires_at: OffsetDateTime::now_utc() + time::Duration::minutes(5),
                },
            )
            .await
            .expect("issue ticket");
        let ticket = Arc::<str>::from(issued.ticket.expose());
        let first = {
            let service = service.clone();
            let ticket = Arc::clone(&ticket);
            tokio::spawn(async move {
                service
                    .consume(&request_context("consume-ticket-1"), &ticket, document_id)
                    .await
            })
        };
        let second = {
            let service = service.clone();
            tokio::spawn(async move {
                service
                    .consume(&request_context("consume-ticket-2"), &ticket, document_id)
                    .await
            })
        };
        let results = [
            first.await.expect("first join"),
            second.await.expect("second join"),
        ];
        assert_eq!(results.iter().filter(|result| result.is_ok()).count(), 1);
    }

    #[test]
    fn redis_budget_rejects_an_expired_request_before_a_command() {
        let mut context = RequestContext::new("expired-ticket-request");
        context.deadline = Some(Instant::now());

        let error = operation_budget(&context, Duration::from_secs(5), "consume Redis ticket")
            .expect_err("expired request");
        assert_eq!(error.code(), ErrorCode::Unavailable);
        assert_eq!(error.key(), "collaboration.deadline_exceeded");
        assert_eq!(error.detail(), "request deadline exceeded");
    }

    #[test]
    fn redis_budget_uses_the_shorter_request_deadline() {
        let mut context = RequestContext::new("short-ticket-request");
        context.deadline = Instant::now().checked_add(Duration::from_millis(50));

        let budget = operation_budget(&context, Duration::from_secs(5), "store Redis ticket")
            .expect("request budget");
        assert!(budget.request_deadline_limited);
        assert!(!budget.maximum_wait.is_zero());
        assert!(budget.maximum_wait <= Duration::from_millis(50));
    }

    fn request_context(request_id: &'static str) -> RequestContext {
        let mut context = RequestContext::new(request_id);
        context.deadline = Instant::now().checked_add(Duration::from_secs(1));
        context
    }
}
