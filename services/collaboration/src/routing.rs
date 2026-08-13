use std::{
    sync::Arc,
    time::{Duration, Instant},
};

#[cfg(test)]
use std::{
    collections::HashMap,
    sync::{
        Mutex,
        atomic::{AtomicU32, Ordering},
    },
};

use async_trait::async_trait;
use redis::aio::ConnectionManager;
use sha2::{Digest, Sha256};
use tokio::time::timeout;
use tokio_util::sync::CancellationToken;

use crate::{
    config::RedisConfig,
    domain::{DocumentId, RequestContext},
    error::{ErrorCode, Result, ServiceError},
};

/// Default number of Collaboration instances when unset.
/// 未配置时的 Collaboration 实例数。
pub const DEFAULT_INSTANCE_COUNT: u32 = 1;
/// Maximum number of Collaboration instances in one hash ring.
/// 单个 Hash 环允许的最大 Collaboration 实例数。
pub const MAXIMUM_INSTANCE_COUNT: u32 = 32;
const ROUTE_TTL: Duration = Duration::from_secs(90);
const LOAD_TTL: Duration = Duration::from_secs(3);
const LOAD_HEARTBEAT: Duration = Duration::from_secs(1);
const ASSIGN_ATTEMPTS: usize = 8;

/// Selects the preferred instance ordinal for a document.
/// 为文档选择首选实例序号。
///
/// # Errors
///
/// Returns an invalid-input error when `instance_count` is zero.
pub fn hash_bucket(document_id: DocumentId, instance_count: u32) -> Result<u32> {
    if instance_count == 0 {
        return Err(ServiceError::invalid_input(
            "COLLABORATION_INSTANCE_COUNT must be greater than zero",
        ));
    }
    let digest = Sha256::digest(document_id.as_uuid().as_bytes());
    let mut bytes = [0_u8; 8];
    bytes.copy_from_slice(&digest[..8]);
    #[allow(clippy::cast_possible_truncation)]
    Ok((u64::from_be_bytes(bytes) % u64::from(instance_count)) as u32)
}

/// Parses this process ordinal from a stable instance identity.
/// 从稳定实例身份解析本进程序号。
///
/// # Errors
///
/// Returns an invalid-input error when the identity cannot be mapped into `0..instance_count`.
pub fn parse_instance_ordinal(instance_id: &str, instance_count: u32) -> Result<u32> {
    validate_instance_count(instance_count)?;
    if instance_id.trim() != instance_id || instance_id.is_empty() {
        return Err(ServiceError::invalid_input(
            "COLLABORATION_INSTANCE_ID must be non-empty and trimmed",
        ));
    }
    if instance_count == 1 {
        return Ok(0);
    }
    let Some((_, ordinal_str)) = instance_id.rsplit_once('-') else {
        return Err(ServiceError::invalid_input(
            "COLLABORATION_INSTANCE_ID must end with -<ordinal> when instance_count is greater than one",
        ));
    };
    let ordinal = parse_canonical_u32(ordinal_str).ok_or_else(|| {
        ServiceError::invalid_input(
            "COLLABORATION_INSTANCE_ID ordinal suffix must be a canonical integer",
        )
    })?;
    if ordinal >= instance_count {
        return Err(ServiceError::invalid_input(
            "COLLABORATION_INSTANCE_ID ordinal must be less than COLLABORATION_INSTANCE_COUNT",
        ));
    }
    Ok(ordinal)
}

/// Builds the public WebSocket path for a placed document.
/// 构造已放置文档的公开 WebSocket 路径。
#[must_use]
pub fn websocket_document_path(ordinal: u32, document_id: DocumentId) -> String {
    format!("/v1/instances/{ordinal}/documents/{document_id}")
}

fn validate_instance_count(instance_count: u32) -> Result<()> {
    if instance_count == 0 || instance_count > MAXIMUM_INSTANCE_COUNT {
        return Err(ServiceError::invalid_input(format!(
            "COLLABORATION_INSTANCE_COUNT must be between 1 and {MAXIMUM_INSTANCE_COUNT}"
        )));
    }
    Ok(())
}

fn parse_canonical_u32(value: &str) -> Option<u32> {
    let parsed = value.parse::<u32>().ok()?;
    (parsed.to_string() == value).then_some(parsed)
}

#[async_trait]
pub trait RoutingStore: Send + Sync {
    async fn get_route(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<Option<u32>>;

    async fn set_route_nx(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        ordinal: u32,
        ttl: Duration,
    ) -> Result<bool>;

    async fn put_route(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        ordinal: u32,
        ttl: Duration,
    ) -> Result<()>;

    async fn delete_route(&self, context: &RequestContext, document_id: DocumentId) -> Result<()>;

    async fn get_load(&self, context: &RequestContext, ordinal: u32) -> Result<Option<u32>>;

    async fn put_load(&self, ordinal: u32, connections: u32, ttl: Duration) -> Result<()>;
}

#[derive(Clone)]
pub struct RoutingService {
    store: Arc<dyn RoutingStore>,
    instance_count: u32,
    local_ordinal: u32,
    max_connections: u32,
    route_ttl: Duration,
    load_ttl: Duration,
}

impl RoutingService {
    /// Creates a routing service for one Collaboration instance.
    /// 为单个 Collaboration 实例创建路由服务。
    ///
    /// # Errors
    ///
    /// Returns an invalid-input error when the instance topology is inconsistent.
    pub fn new(
        store: Arc<dyn RoutingStore>,
        instance_count: u32,
        local_ordinal: u32,
        max_connections: u32,
    ) -> Result<Self> {
        validate_instance_count(instance_count)?;
        if local_ordinal >= instance_count || max_connections == 0 {
            return Err(ServiceError::invalid_input(
                "collaboration routing topology is invalid",
            ));
        }
        Ok(Self {
            store,
            instance_count,
            local_ordinal,
            max_connections,
            route_ttl: ROUTE_TTL,
            load_ttl: LOAD_TTL,
        })
    }

    #[must_use]
    pub const fn instance_count(&self) -> u32 {
        self.instance_count
    }

    #[must_use]
    pub const fn local_ordinal(&self) -> u32 {
        self.local_ordinal
    }

    #[must_use]
    pub const fn accepts_legacy_document_path(&self) -> bool {
        self.instance_count == 1
    }

    /// Places a document onto one Ready instance that is under capacity.
    /// 将文档放到一台未满载且 Ready 的实例上。
    ///
    /// # Errors
    ///
    /// Returns an unavailable error when every instance is full, not ready, or the store fails.
    pub async fn assign(&self, context: &RequestContext, document_id: DocumentId) -> Result<u32> {
        for _ in 0..ASSIGN_ATTEMPTS {
            if let Some(ordinal) = self.follow_or_clear_route(context, document_id).await? {
                return Ok(ordinal);
            }
            match self.place_new_route(context, document_id).await? {
                PlaceResult::Placed(ordinal) => {
                    tracing::debug!(
                        document.id = %document_id,
                        instance.ordinal = ordinal,
                        "assigned collaboration instance"
                    );
                    return Ok(ordinal);
                }
                PlaceResult::LostRace => {}
                PlaceResult::NoneReady => {
                    return Err(ServiceError::unavailable(anyhow::anyhow!(
                        "every collaboration instance is full or not ready"
                    )));
                }
            }
        }
        Err(ServiceError::unavailable(anyhow::anyhow!(
            "could not assign a collaboration instance"
        )))
    }

    /// Refreshes the sticky mapping TTL after a successful handshake.
    /// 握手成功后刷新粘性映射 TTL。
    ///
    /// # Errors
    ///
    /// Returns a store error when Redis cannot complete the write.
    pub async fn refresh_route(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<()> {
        self.store
            .put_route(context, document_id, self.local_ordinal, self.route_ttl)
            .await
    }

    /// Publishes this instance's active connection count.
    /// 发布本实例当前连接数。
    ///
    /// # Errors
    ///
    /// Returns a store error when Redis cannot complete the write.
    pub async fn publish_load(&self, connections: u32) -> Result<()> {
        self.store
            .put_load(self.local_ordinal, connections, self.load_ttl)
            .await
    }

    /// Counts connections held by the local handshake semaphore.
    /// 统计本机握手信号量已占用的连接数。
    #[must_use]
    pub fn active_connections(&self, connections: &tokio::sync::Semaphore) -> u32 {
        let max_connections = usize::try_from(self.max_connections).unwrap_or(usize::MAX);
        u32::try_from(max_connections.saturating_sub(connections.available_permits()))
            .unwrap_or(u32::MAX)
    }

    /// Publishes the local handshake semaphore occupancy as instance load.
    /// 将本机握手信号量占用发布为实例负载。
    ///
    /// # Errors
    ///
    /// Returns a store error when Redis cannot complete the write.
    pub async fn publish_semaphore_load(&self, connections: &tokio::sync::Semaphore) -> Result<()> {
        self.publish_load(self.active_connections(connections))
            .await
    }

    /// Starts a bounded heartbeat that refreshes this instance's load key.
    /// 启动有界心跳以刷新本实例的负载键。
    pub fn spawn_load_heartbeat(
        self: &Arc<Self>,
        connections: Arc<tokio::sync::Semaphore>,
        cancellation: CancellationToken,
    ) {
        let routing = Arc::clone(self);
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(LOAD_HEARTBEAT);
            interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
            loop {
                tokio::select! {
                    () = cancellation.cancelled() => break,
                    _ = interval.tick() => {
                        if let Err(error) = routing.publish_semaphore_load(&connections).await {
                            tracing::warn!(
                                error = %error,
                                instance.ordinal = routing.local_ordinal,
                                "failed to publish collaboration instance load"
                            );
                        }
                    }
                }
            }
        });
    }

    async fn follow_or_clear_route(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<Option<u32>> {
        let Some(ordinal) = self.store.get_route(context, document_id).await? else {
            return Ok(None);
        };
        if ordinal >= self.instance_count {
            self.store.delete_route(context, document_id).await?;
            return Ok(None);
        }
        match self.store.get_load(context, ordinal).await? {
            None => {
                self.store.delete_route(context, document_id).await?;
                Ok(None)
            }
            Some(load) if load >= self.max_connections => Err(ServiceError::unavailable(
                anyhow::anyhow!("sticky collaboration instance is at capacity"),
            )),
            Some(_) => {
                self.store
                    .put_route(context, document_id, ordinal, self.route_ttl)
                    .await?;
                Ok(Some(ordinal))
            }
        }
    }

    async fn place_new_route(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<PlaceResult> {
        let start = hash_bucket(document_id, self.instance_count)?;
        let mut overflowed = false;
        for offset in 0..self.instance_count {
            let ordinal = (start + offset) % self.instance_count;
            match self.store.get_load(context, ordinal).await? {
                Some(load) if load < self.max_connections => {
                    if offset > 0 {
                        overflowed = true;
                    }
                    if self
                        .store
                        .set_route_nx(context, document_id, ordinal, self.route_ttl)
                        .await?
                    {
                        if overflowed {
                            tracing::info!(
                                document.id = %document_id,
                                instance.ordinal = ordinal,
                                hash.ordinal = start,
                                "collaboration instance overflowed hash bucket"
                            );
                        }
                        return Ok(PlaceResult::Placed(ordinal));
                    }
                    return Ok(PlaceResult::LostRace);
                }
                _ => {}
            }
        }
        Ok(PlaceResult::NoneReady)
    }
}

enum PlaceResult {
    Placed(u32),
    LostRace,
    NoneReady,
}

pub struct RedisRoutingStore {
    connection: ConnectionManager,
    key_prefix: Arc<str>,
    operation_timeout: Duration,
}

impl RedisRoutingStore {
    /// Opens and verifies the Redis connection used for document placement.
    /// 打开并校验用于文档放置的 Redis 连接。
    ///
    /// # Errors
    ///
    /// Returns an error when the Redis URL is invalid or the connection cannot be verified.
    pub async fn open(config: &RedisConfig) -> Result<Self> {
        let client = redis::Client::open(config.url.as_str()).map_err(|error| {
            ServiceError::invalid_input("COLLABORATION_REDIS_URL is invalid").with_source(error)
        })?;
        let connection = timeout(config.operation_timeout, ConnectionManager::new(client))
            .await
            .map_err(|_| dependency_timeout("connect Redis routing"))?
            .map_err(|error| dependency_error(error, "connect Redis routing"))?;
        let store = Self {
            connection,
            key_prefix: Arc::from(config.prefix.as_str()),
            operation_timeout: config.operation_timeout,
        };
        store.ping().await?;
        Ok(store)
    }

    async fn ping(&self) -> Result<()> {
        let response: String = self
            .command(
                &mut redis::cmd("PING"),
                "ping Redis routing",
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
                "Redis returned an unexpected routing ping response"
            )))
        }
    }

    fn route_key(&self, document_id: DocumentId) -> String {
        format!("{}:route:{document_id}", self.key_prefix)
    }

    fn load_key(&self, ordinal: u32) -> String {
        format!("{}:load:{ordinal}", self.key_prefix)
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
                anyhow::Error::new(error).context("Redis routing exceeded request deadline"),
            )),
            Err(_) => Err(dependency_timeout(operation)),
        }
    }
}

#[async_trait]
impl RoutingStore for RedisRoutingStore {
    async fn get_route(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<Option<u32>> {
        let value: Option<String> = self
            .request_command(
                context,
                redis::cmd("GET").arg(self.route_key(document_id)),
                "read collaboration route",
            )
            .await?;
        parse_optional_ordinal(value, "collaboration route")
    }

    async fn set_route_nx(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        ordinal: u32,
        ttl: Duration,
    ) -> Result<bool> {
        let milliseconds = ttl_millis(ttl, "collaboration route NX TTL")?;
        let response: Option<String> = self
            .request_command(
                context,
                redis::cmd("SET")
                    .arg(self.route_key(document_id))
                    .arg(ordinal)
                    .arg("PX")
                    .arg(milliseconds)
                    .arg("NX"),
                "reserve collaboration route",
            )
            .await?;
        Ok(response.is_some())
    }

    async fn put_route(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        ordinal: u32,
        ttl: Duration,
    ) -> Result<()> {
        let milliseconds = ttl_millis(ttl, "collaboration route TTL")?;
        let _: () = self
            .request_command(
                context,
                redis::cmd("SET")
                    .arg(self.route_key(document_id))
                    .arg(ordinal)
                    .arg("PX")
                    .arg(milliseconds),
                "store collaboration route",
            )
            .await?;
        Ok(())
    }

    async fn delete_route(&self, context: &RequestContext, document_id: DocumentId) -> Result<()> {
        let _: () = self
            .request_command(
                context,
                redis::cmd("DEL").arg(self.route_key(document_id)),
                "delete collaboration route",
            )
            .await?;
        Ok(())
    }

    async fn get_load(&self, context: &RequestContext, ordinal: u32) -> Result<Option<u32>> {
        let value: Option<String> = self
            .request_command(
                context,
                redis::cmd("GET").arg(self.load_key(ordinal)),
                "read collaboration instance load",
            )
            .await?;
        parse_optional_ordinal(value, "collaboration instance load")
    }

    async fn put_load(&self, ordinal: u32, connections: u32, ttl: Duration) -> Result<()> {
        let milliseconds = ttl_millis(ttl, "collaboration load TTL")?;
        let _: () = self
            .command(
                redis::cmd("SET")
                    .arg(self.load_key(ordinal))
                    .arg(connections)
                    .arg("PX")
                    .arg(milliseconds),
                "store collaboration instance load",
                OperationBudget {
                    maximum_wait: self.operation_timeout,
                    request_deadline_limited: false,
                },
            )
            .await?;
        Ok(())
    }
}

fn parse_optional_ordinal(value: Option<String>, field: &'static str) -> Result<Option<u32>> {
    let Some(value) = value else {
        return Ok(None);
    };
    parse_canonical_u32(&value)
        .map(Some)
        .ok_or_else(|| ServiceError::unavailable(anyhow::anyhow!("{field} is invalid")))
}

fn ttl_millis(ttl: Duration, operation: &'static str) -> Result<u64> {
    let milliseconds = u64::try_from(ttl.as_millis())
        .map_err(|error| ServiceError::internal(anyhow::anyhow!(error).context(operation)))?;
    if milliseconds == 0 {
        return Err(ServiceError::internal(anyhow::anyhow!(
            "{operation} must be positive"
        )));
    }
    Ok(milliseconds)
}

#[derive(Clone, Copy, Debug)]
struct OperationBudget {
    maximum_wait: Duration,
    request_deadline_limited: bool,
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
                anyhow::anyhow!("request deadline elapsed before Redis routing"),
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
#[derive(Default)]
pub(crate) struct MemoryRoutingStore {
    state: Mutex<MemoryRoutingState>,
    nx_failures_remaining: AtomicU32,
}

#[cfg(test)]
#[derive(Default)]
struct MemoryRoutingState {
    routes: HashMap<DocumentId, u32>,
    loads: HashMap<u32, u32>,
}

#[cfg(test)]
impl MemoryRoutingStore {
    pub(crate) fn fail_next_nx(&self, count: u32) {
        self.nx_failures_remaining.store(count, Ordering::SeqCst);
    }

    pub(crate) fn seed_load(&self, ordinal: u32, connections: u32) {
        lock_memory(&self.state).loads.insert(ordinal, connections);
    }

    pub(crate) fn seed_route(&self, document_id: DocumentId, ordinal: u32) {
        lock_memory(&self.state).routes.insert(document_id, ordinal);
    }
}

#[cfg(test)]
fn lock_memory(state: &Mutex<MemoryRoutingState>) -> std::sync::MutexGuard<'_, MemoryRoutingState> {
    state
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

#[cfg(test)]
#[async_trait]
impl RoutingStore for MemoryRoutingStore {
    async fn get_route(
        &self,
        _context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<Option<u32>> {
        Ok(self
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .routes
            .get(&document_id)
            .copied())
    }

    async fn set_route_nx(
        &self,
        _context: &RequestContext,
        document_id: DocumentId,
        ordinal: u32,
        _ttl: Duration,
    ) -> Result<bool> {
        if self
            .nx_failures_remaining
            .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |value| {
                value.checked_sub(1)
            })
            .is_ok()
        {
            return Ok(false);
        }
        let mut state = lock_memory(&self.state);
        if state.routes.contains_key(&document_id) {
            return Ok(false);
        }
        state.routes.insert(document_id, ordinal);
        Ok(true)
    }

    async fn put_route(
        &self,
        _context: &RequestContext,
        document_id: DocumentId,
        ordinal: u32,
        _ttl: Duration,
    ) -> Result<()> {
        lock_memory(&self.state).routes.insert(document_id, ordinal);
        Ok(())
    }

    async fn delete_route(&self, _context: &RequestContext, document_id: DocumentId) -> Result<()> {
        lock_memory(&self.state).routes.remove(&document_id);
        Ok(())
    }

    async fn get_load(&self, _context: &RequestContext, ordinal: u32) -> Result<Option<u32>> {
        Ok(lock_memory(&self.state).loads.get(&ordinal).copied())
    }

    async fn put_load(&self, ordinal: u32, connections: u32, _ttl: Duration) -> Result<()> {
        lock_memory(&self.state).loads.insert(ordinal, connections);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use std::{sync::Arc, time::Duration};

    use super::{
        MemoryRoutingStore, RoutingService, RoutingStore as _, hash_bucket, parse_instance_ordinal,
        websocket_document_path,
    };
    use crate::{
        domain::{DocumentId, RequestContext},
        error::ErrorCode,
    };

    #[test]
    fn hash_bucket_is_stable_and_in_range() {
        let document_id =
            DocumentId::parse("0198f0e0-7b6d-7a11-8e21-1123456789ab").expect("uuidv7");
        let first = hash_bucket(document_id, 2).expect("hash");
        let second = hash_bucket(document_id, 2).expect("hash");
        assert_eq!(first, second);
        assert!(first < 2);
        assert!(hash_bucket(document_id, 0).is_err());
    }

    #[test]
    fn hash_bucket_spreads_distinct_documents() {
        let mut buckets = std::collections::BTreeSet::new();
        for _ in 0..32 {
            buckets.insert(hash_bucket(DocumentId::new(), 2).expect("hash"));
        }
        assert_eq!(buckets.len(), 2);
    }

    #[test]
    fn instance_ordinal_parses_statefulset_identity() {
        assert_eq!(
            parse_instance_ordinal("knowledge-core-collaboration-0", 2).expect("ordinal"),
            0
        );
        assert_eq!(
            parse_instance_ordinal("knowledge-core-collaboration-1", 2).expect("ordinal"),
            1
        );
        assert_eq!(
            parse_instance_ordinal("collaboration-compose", 1).expect("single instance"),
            0
        );
        assert!(parse_instance_ordinal("collaboration-compose", 2).is_err());
        assert!(parse_instance_ordinal("knowledge-core-collaboration-2", 2).is_err());
        assert!(parse_instance_ordinal("knowledge-core-collaboration-01", 2).is_err());
        assert!(parse_instance_ordinal("", 1).is_err());
    }

    #[test]
    fn websocket_path_includes_instance_ordinal() {
        let document_id =
            DocumentId::parse("0198f0e0-7b6d-7a11-8e21-1123456789ab").expect("uuidv7");
        assert_eq!(
            websocket_document_path(1, document_id),
            "/v1/instances/1/documents/0198f0e0-7b6d-7a11-8e21-1123456789ab"
        );
    }

    #[tokio::test]
    async fn assign_uses_hash_bucket_when_no_sticky_route_exists() {
        let store = Arc::new(MemoryRoutingStore::default());
        store
            .put_load(0, 0, Duration::from_secs(3))
            .await
            .expect("load 0");
        store
            .put_load(1, 0, Duration::from_secs(3))
            .await
            .expect("load 1");
        let routing = RoutingService::new(store, 2, 0, 8).expect("routing");
        let document_id =
            DocumentId::parse("0198f0e0-7b6d-7a11-8e21-1123456789ab").expect("uuidv7");
        let expected = hash_bucket(document_id, 2).expect("hash");
        let assigned = routing
            .assign(&request_context(), document_id)
            .await
            .expect("assign");
        assert_eq!(assigned, expected);
    }

    #[tokio::test]
    async fn assign_follows_sticky_route_when_instance_is_ready() {
        let store = Arc::new(MemoryRoutingStore::default());
        let document_id = DocumentId::new();
        store.seed_route(document_id, 1);
        store
            .put_load(0, 0, Duration::from_secs(3))
            .await
            .expect("load 0");
        store
            .put_load(1, 1, Duration::from_secs(3))
            .await
            .expect("load 1");
        let routing = RoutingService::new(store, 2, 0, 8).expect("routing");
        let assigned = routing
            .assign(&request_context(), document_id)
            .await
            .expect("assign");
        assert_eq!(assigned, 1);
    }

    #[tokio::test]
    async fn assign_rejects_sticky_route_when_target_is_at_capacity() {
        let store = Arc::new(MemoryRoutingStore::default());
        let document_id = DocumentId::new();
        store
            .put_route(&request_context(), document_id, 0, Duration::from_secs(90))
            .await
            .expect("route");
        store
            .put_load(0, 8, Duration::from_secs(3))
            .await
            .expect("load 0");
        store
            .put_load(1, 0, Duration::from_secs(3))
            .await
            .expect("load 1");
        let routing = RoutingService::new(store, 2, 0, 8).expect("routing");
        let error = routing
            .assign(&request_context(), document_id)
            .await
            .expect_err("full sticky instance");
        assert_eq!(error.code(), ErrorCode::Unavailable);
    }

    #[tokio::test]
    async fn assign_replaces_sticky_route_when_target_is_not_ready() {
        let store = Arc::new(MemoryRoutingStore::default());
        let document_id =
            DocumentId::parse("0198f0e0-7b6d-7a11-8e21-1123456789ab").expect("uuidv7");
        store
            .put_route(&request_context(), document_id, 0, Duration::from_secs(90))
            .await
            .expect("route");
        store
            .put_load(1, 0, Duration::from_secs(3))
            .await
            .expect("load 1");
        let routing = RoutingService::new(store.clone(), 2, 0, 8).expect("routing");
        let assigned = routing
            .assign(&request_context(), document_id)
            .await
            .expect("reassign");
        assert_eq!(assigned, 1);
        assert_eq!(
            store
                .get_route(&request_context(), document_id)
                .await
                .expect("route"),
            Some(1)
        );
    }

    #[tokio::test]
    async fn assign_overflows_to_the_next_ready_bucket_when_hash_target_is_full() {
        let store = Arc::new(MemoryRoutingStore::default());
        let document_id =
            DocumentId::parse("0198f0e0-7b6d-7a11-8e21-1123456789ab").expect("uuidv7");
        let hashed = hash_bucket(document_id, 2).expect("hash");
        let overflow = (hashed + 1) % 2;
        store
            .put_load(hashed, 8, Duration::from_secs(3))
            .await
            .expect("full hash bucket");
        store
            .put_load(overflow, 1, Duration::from_secs(3))
            .await
            .expect("overflow bucket");
        let routing = RoutingService::new(store, 2, 0, 8).expect("routing");
        let assigned = routing
            .assign(&request_context(), document_id)
            .await
            .expect("overflow");
        assert_eq!(assigned, overflow);
    }

    #[tokio::test]
    async fn assign_fails_when_every_instance_is_full() {
        let store = Arc::new(MemoryRoutingStore::default());
        store
            .put_load(0, 8, Duration::from_secs(3))
            .await
            .expect("load 0");
        store
            .put_load(1, 8, Duration::from_secs(3))
            .await
            .expect("load 1");
        let routing = RoutingService::new(store, 2, 0, 8).expect("routing");
        let error = routing
            .assign(&request_context(), DocumentId::new())
            .await
            .expect_err("all full");
        assert_eq!(error.code(), ErrorCode::Unavailable);
    }

    #[tokio::test]
    async fn assign_retries_after_a_lost_set_nx_race() {
        let store = Arc::new(MemoryRoutingStore::default());
        store
            .put_load(0, 0, Duration::from_secs(3))
            .await
            .expect("load 0");
        store
            .put_load(1, 0, Duration::from_secs(3))
            .await
            .expect("load 1");
        store.fail_next_nx(1);
        let routing = RoutingService::new(store, 2, 0, 8).expect("routing");
        let document_id = DocumentId::new();
        let assigned = routing
            .assign(&request_context(), document_id)
            .await
            .expect("retry after race");
        assert!(assigned < 2);
    }

    fn request_context() -> RequestContext {
        RequestContext::new("routing-test")
    }
}
