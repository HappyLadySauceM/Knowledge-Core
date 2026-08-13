use arc_swap::ArcSwap;
use std::{
    collections::{HashMap, HashSet},
    future::Future,
    sync::{
        Arc, Mutex as StdMutex,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use futures_util::future::{AbortHandle, Abortable};
use tokio::sync::{Mutex, mpsc, oneshot, watch};
use tokio_util::{sync::CancellationToken, task::TaskTracker};
use uuid::Uuid;
use yrs::{
    ReadTxn, Transact, Update,
    block::ClientID,
    encoding::read::Cursor,
    sync::{Awareness, Message as SyncProtocolMessage, MessageReader, SyncMessage},
    updates::{
        decoder::{Decode, DecoderV1},
        encoder::{Encode, Encoder, EncoderV1},
    },
};

use crate::{
    config::{ActorConfig, MAX_TICKET_TTL_MS, PublicConfig},
    domain::{Access, DocumentId, DocumentVersion, PublicUser, RequestContext},
    error::{ErrorCode, Result, ServiceError},
    richtext,
    storage::{
        DocumentStore, LoadedDocument, RestorationCandidate, RestoreVersion, StoredUpdate,
        UpdateLimits,
    },
    telemetry::Metrics,
    ticket::TicketClaims,
};

pub const CLOSE_INVALID_PROTOCOL: CloseSignal = CloseSignal::new(4400, "invalid-protocol");
pub const CLOSE_INVALID_UPDATE: CloseSignal = CloseSignal::new(4400, "invalid-update");
pub const CLOSE_SESSION_EXPIRED: CloseSignal = CloseSignal::new(4401, "session-expired");
pub const CLOSE_FORBIDDEN: CloseSignal = CloseSignal::new(4403, "forbidden");
pub const CLOSE_DOCUMENT_INVALIDATED: CloseSignal = CloseSignal::new(4409, "document-invalidated");
pub const CLOSE_SLOW_CONSUMER: CloseSignal = CloseSignal::new(4429, "slow-consumer");
pub const CLOSE_RATE_LIMITED: CloseSignal = CloseSignal::new(4429, "rate-limited");
pub const CLOSE_DEPENDENCY_UNAVAILABLE: CloseSignal =
    CloseSignal::new(4503, "dependency-unavailable");
pub const CLOSE_SERVICE_RESTART: CloseSignal = CloseSignal::new(1012, "service-restart");

const REMOTE_UPDATE_BATCH: i64 = 1_000;
const MAX_AWARENESS_CLIENTS_PER_CONNECTION: usize = 1;
const PERMISSION_REVISION_PRUNE_INTERVAL: Duration = Duration::from_mins(1);

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct CloseSignal {
    pub code: u16,
    pub reason: &'static str,
}

impl CloseSignal {
    pub const fn new(code: u16, reason: &'static str) -> Self {
        Self { code, reason }
    }
}

#[derive(Clone, Debug)]
pub struct ActorSession {
    pub actor: PublicUser,
    pub access: Access,
    pub permission_revision: i64,
    pub expires_at: time::OffsetDateTime,
}

impl From<TicketClaims> for ActorSession {
    fn from(claims: TicketClaims) -> Self {
        Self {
            actor: claims.actor,
            access: claims.access,
            permission_revision: claims.permission_revision,
            expires_at: claims.session_expires_at,
        }
    }
}

#[derive(Clone, Copy, Debug)]
pub struct ActorLimits {
    command_capacity: usize,
    outbound_capacity: usize,
    maximum_connections: usize,
    command_timeout: Duration,
    idle_timeout: Duration,
    permission_revision_retention: Duration,
    update: UpdateLimits,
    maximum_awareness_bytes: usize,
    updates_per_second: u32,
    awareness_messages_per_second: u32,
}

impl ActorLimits {
    /// Builds actor limits from the validated service configuration.
    ///
    /// # Errors
    ///
    /// Returns an invalid-input error when any required limit is zero.
    pub fn from_config(actor: &ActorConfig, public: &PublicConfig) -> Result<Self> {
        if actor.command_capacity == 0
            || actor.outbound_capacity == 0
            || public.max_connections_per_document == 0
            || actor.command_timeout.is_zero()
            || actor.idle_timeout.is_zero()
            || public.max_update_bytes == 0
            || public.max_document_bytes == 0
            || public.max_awareness_bytes == 0
            || actor.updates_per_second == 0
            || actor.awareness_messages_per_second == 0
        {
            return Err(ServiceError::invalid_input(
                "document actor limits must be greater than zero",
            ));
        }
        Ok(Self {
            command_capacity: actor.command_capacity,
            outbound_capacity: actor.outbound_capacity,
            maximum_connections: public.max_connections_per_document,
            command_timeout: actor.command_timeout,
            idle_timeout: actor.idle_timeout,
            permission_revision_retention: actor
                .idle_timeout
                .max(Duration::from_millis(MAX_TICKET_TTL_MS)),
            update: UpdateLimits {
                maximum_update_bytes: public.max_update_bytes,
                maximum_document_bytes: public.max_document_bytes,
            },
            maximum_awareness_bytes: public.max_awareness_bytes,
            updates_per_second: actor.updates_per_second,
            awareness_messages_per_second: actor.awareness_messages_per_second,
        })
    }

    #[cfg(test)]
    pub const fn for_test() -> Self {
        Self {
            command_capacity: 16,
            outbound_capacity: 8,
            maximum_connections: 8,
            command_timeout: Duration::from_secs(1),
            idle_timeout: Duration::from_millis(100),
            permission_revision_retention: Duration::from_millis(MAX_TICKET_TTL_MS),
            update: UpdateLimits {
                maximum_update_bytes: 1024 * 1024,
                maximum_document_bytes: 4 * 1024 * 1024,
            },
            maximum_awareness_bytes: 64 * 1024,
            updates_per_second: 50,
            awareness_messages_per_second: 20,
        }
    }
}

#[derive(Clone)]
pub struct ActorRegistry {
    inner: Arc<RegistryInner>,
}

struct RegistryInner {
    store: Arc<dyn DocumentStore>,
    limits: ArcSwap<ActorLimits>,
    metrics: Metrics,
    entries: Mutex<HashMap<DocumentId, RegistryEntry>>,
    permission_revisions: Mutex<PermissionRevisionCache>,
    tasks: ActorTasks,
    cancellation: CancellationToken,
    accepting: AtomicBool,
}

struct RegistryEntry {
    actor_id: Uuid,
    handle: ActorHandle,
}

struct PermissionRevisionCache {
    revisions: HashMap<DocumentId, PermissionRevisionWatermark>,
    retention: Duration,
    next_prune: Instant,
}

struct PermissionRevisionWatermark {
    revision: i64,
    expires_at: Instant,
}

impl PermissionRevisionCache {
    fn new(retention: Duration) -> Self {
        let now = Instant::now();
        Self {
            revisions: HashMap::new(),
            retention,
            next_prune: now + retention.min(PERMISSION_REVISION_PRUNE_INTERVAL),
        }
    }

    fn revision(&mut self, document_id: DocumentId, now: Instant) -> Option<i64> {
        self.prune_if_due(now);
        if self
            .revisions
            .get(&document_id)
            .is_some_and(|watermark| watermark.expires_at <= now)
        {
            self.revisions.remove(&document_id);
        }
        self.revisions
            .get(&document_id)
            .map(|watermark| watermark.revision)
    }

    fn record(&mut self, document_id: DocumentId, revision: i64, now: Instant) {
        self.prune_if_due(now);
        let watermark = self
            .revisions
            .entry(document_id)
            .or_insert(PermissionRevisionWatermark {
                revision,
                expires_at: now + self.retention,
            });
        watermark.revision = watermark.revision.max(revision);
        watermark.expires_at = now + self.retention;
    }

    fn prune_if_due(&mut self, now: Instant) {
        if now < self.next_prune {
            return;
        }
        self.revisions
            .retain(|_, watermark| watermark.expires_at > now);
        self.next_prune = now + self.retention.min(PERMISSION_REVISION_PRUNE_INTERVAL);
    }
}

#[derive(Default)]
struct ActorTasks {
    tracker: TaskTracker,
    abort_handles: Arc<StdMutex<HashMap<Uuid, AbortHandle>>>,
}

impl ActorTasks {
    fn spawn<F>(&self, actor_id: Uuid, future: F)
    where
        F: Future<Output = ()> + Send + 'static,
    {
        let (abort, registration) = AbortHandle::new_pair();
        lock_abort_handles(&self.abort_handles).insert(actor_id, abort);
        let handles = Arc::clone(&self.abort_handles);
        self.tracker.spawn(async move {
            let _ = Abortable::new(future, registration).await;
            lock_abort_handles(&handles).remove(&actor_id);
        });
    }

    async fn shutdown(&self, maximum_wait: Duration) -> Result<()> {
        self.tracker.close();
        if tokio::time::timeout(maximum_wait, self.tracker.wait())
            .await
            .is_ok()
        {
            return Ok(());
        }
        let handles = lock_abort_handles(&self.abort_handles)
            .values()
            .cloned()
            .collect::<Vec<_>>();
        for handle in handles {
            handle.abort();
        }
        self.tracker.wait().await;
        Err(ServiceError::internal(anyhow::anyhow!(
            "document actors did not stop before the shutdown deadline"
        )))
    }
}

impl ActorRegistry {
    pub fn new(
        store: Arc<dyn DocumentStore>,
        limits: ActorLimits,
        metrics: Metrics,
        cancellation: CancellationToken,
    ) -> Self {
        Self {
            inner: Arc::new(RegistryInner {
                store,
                limits: ArcSwap::from_pointee(limits),
                metrics,
                entries: Mutex::new(HashMap::new()),
                permission_revisions: Mutex::new(PermissionRevisionCache::new(
                    limits.permission_revision_retention,
                )),
                tasks: ActorTasks::default(),
                cancellation,
                accepting: AtomicBool::new(true),
            }),
        }
    }

    pub fn is_accepting(&self) -> bool {
        self.inner.accepting.load(Ordering::Acquire) && !self.inner.cancellation.is_cancelled()
    }

    /// Connects a session to the document actor, creating the actor when necessary.
    ///
    /// # Errors
    ///
    /// Returns a close signal when the registry is stopping, overloaded, or cannot initialize
    /// the actor before the request deadline.
    pub async fn connect(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        session: ActorSession,
    ) -> std::result::Result<ActorConnection, CloseSignal> {
        if !self.is_accepting() {
            return Err(CLOSE_DEPENDENCY_UNAVAILABLE);
        }
        if session.permission_revision <= 0 {
            return Err(CLOSE_FORBIDDEN);
        }
        let mut permission_revisions = self.inner.permission_revisions.lock().await;
        let known_revision = permission_revisions
            .revision(document_id, Instant::now())
            .unwrap_or(0);
        if session.permission_revision < known_revision {
            return Err(CLOSE_FORBIDDEN);
        }
        let handle = self.actor(context, document_id, known_revision).await?;
        drop(permission_revisions);
        handle.connect(context, session).await
    }

    /// Serializes a version restoration through the document actor.
    ///
    /// # Errors
    ///
    /// Returns an error when the actor is unavailable, the request precondition is stale, or the
    /// restoration cannot be durably committed.
    pub async fn restore_version(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        target: DocumentVersion,
        expected_sequence: i64,
        actor: PublicUser,
        idempotency_key: Option<String>,
    ) -> Result<DocumentVersion> {
        if !self.is_accepting() {
            return Err(close_as_service_error(CLOSE_DEPENDENCY_UNAVAILABLE));
        }
        if target.document_id != document_id || expected_sequence < 0 {
            return Err(ServiceError::invalid_input(
                "document restoration request is inconsistent",
            ));
        }
        actor.validate()?;
        let handle = self
            .actor(context, document_id, 0)
            .await
            .map_err(close_as_service_error)?;
        handle
            .restore_version(
                context.clone(),
                target,
                expected_sequence,
                actor,
                idempotency_key,
            )
            .await
    }

    /// Applies a committed remote sequence notification to an active actor.
    ///
    /// # Errors
    ///
    /// Returns an error when the actor cannot load or apply the committed state.
    pub async fn apply_remote(
        &self,
        document_id: DocumentId,
        generation: i64,
        sequence: i64,
    ) -> Result<()> {
        let handle = {
            let entries = self.inner.entries.lock().await;
            entries.get(&document_id).map(|entry| entry.handle.clone())
        };
        let Some(handle) = handle else {
            return Ok(());
        };
        handle.apply_remote(generation, sequence).await
    }

    /// Queues a critical invalidation for an active document actor.
    ///
    /// # Errors
    ///
    /// Returns an unavailable error when the actor queue remains full until the command deadline
    /// or the actor has already stopped. Callers must not acknowledge the source event on error.
    pub async fn invalidate(&self, document_id: DocumentId, close: CloseSignal) -> Result<()> {
        let handle = {
            let entries = self.inner.entries.lock().await;
            entries.get(&document_id).map(|entry| entry.handle.clone())
        };
        if let Some(handle) = handle {
            handle.invalidate(close).await?;
        }
        Ok(())
    }

    /// Records a permission revision and closes only active sessions authorized by an older one.
    ///
    /// # Errors
    ///
    /// Returns invalid input for a non-positive revision, or unavailable when an active actor
    /// cannot accept the critical invalidation before its command deadline.
    pub async fn invalidate_permissions(
        &self,
        document_id: DocumentId,
        permission_revision: i64,
    ) -> Result<()> {
        if permission_revision <= 0 {
            return Err(ServiceError::invalid_input(
                "permission revision must be positive",
            ));
        }
        let handle = {
            let mut permission_revisions = self.inner.permission_revisions.lock().await;
            permission_revisions.record(document_id, permission_revision, Instant::now());
            let entries = self.inner.entries.lock().await;
            entries.get(&document_id).map(|entry| entry.handle.clone())
        };
        if let Some(handle) = handle {
            handle.invalidate_permissions(permission_revision).await?;
        }
        Ok(())
    }

    /// Stops accepting connections and waits for every document actor to exit.
    ///
    /// # Errors
    ///
    /// Returns an error when actors require forced cancellation after the shutdown deadline.
    pub async fn shutdown(&self, maximum_wait: Duration) -> Result<()> {
        if !self.inner.accepting.swap(false, Ordering::AcqRel) {
            return Ok(());
        }
        self.inner.cancellation.cancel();
        // Serialize with actor registration so every task is tracked before the tracker closes.
        drop(self.inner.entries.lock().await);
        let result = self.inner.tasks.shutdown(maximum_wait).await;
        self.inner.entries.lock().await.clear();
        self.inner
            .permission_revisions
            .lock()
            .await
            .revisions
            .clear();
        result
    }

    pub async fn active_documents(&self) -> usize {
        self.inner.entries.lock().await.len()
    }

    async fn actor(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        initial_permission_revision: i64,
    ) -> std::result::Result<ActorHandle, CloseSignal> {
        let mut entries = self.inner.entries.lock().await;
        if !self.is_accepting() {
            return Err(CLOSE_DEPENDENCY_UNAVAILABLE);
        }
        if let Some(entry) = entries.get(&document_id)
            && !entry.handle.commands.is_closed()
        {
            return Ok(entry.handle.clone());
        }

        let actor_id = Uuid::now_v7();
        let limits = self.inner.limits.load_full();
        let (commands, receiver) = mpsc::channel(limits.command_capacity);
        let handle = ActorHandle {
            commands: commands.clone(),
            command_timeout: limits.command_timeout,
        };
        entries.insert(
            document_id,
            RegistryEntry {
                actor_id,
                handle: handle.clone(),
            },
        );
        self.inner.metrics.actor_started();

        let inner = Arc::clone(&self.inner);
        let initial_context = context.clone();
        self.inner.tasks.spawn(actor_id, async move {
            let result = run_actor(
                &initial_context,
                document_id,
                commands,
                receiver,
                Arc::clone(&inner.store),
                *inner.limits.load_full(),
                inner.metrics.clone(),
                initial_permission_revision,
                inner.cancellation.child_token(),
            )
            .await;
            if let Err(error) = result {
                tracing::error!(
                    component = "collaboration.actor",
                    error_key = error.key(),
                    "document actor stopped after an internal failure"
                );
            }
            let mut entries = inner.entries.lock().await;
            if entries
                .get(&document_id)
                .is_some_and(|entry| entry.actor_id == actor_id)
            {
                entries.remove(&document_id);
            }
            inner.metrics.actor_stopped();
        });
        Ok(handle)
    }

    pub(crate) fn set_limits(&self, limits: ActorLimits) {
        self.inner.limits.store(Arc::new(limits));
    }
}

#[derive(Clone)]
struct ActorHandle {
    commands: mpsc::Sender<ActorCommand>,
    command_timeout: Duration,
}

impl ActorHandle {
    async fn connect(
        &self,
        context: &RequestContext,
        session: ActorSession,
    ) -> std::result::Result<ActorConnection, CloseSignal> {
        let (response, receiver) = oneshot::channel();
        self.send_command(ActorCommand::Connect {
            context: context.clone(),
            session,
            response,
        })?;
        match tokio::time::timeout(self.command_timeout, receiver).await {
            Ok(Ok(result)) => result,
            Ok(Err(_)) | Err(_) => Err(CLOSE_DEPENDENCY_UNAVAILABLE),
        }
    }

    async fn apply_remote(&self, generation: i64, sequence: i64) -> Result<()> {
        let (response, receiver) = oneshot::channel();
        self.send_command(ActorCommand::ApplyRemote {
            generation,
            sequence,
            response,
        })
        .map_err(close_as_service_error)?;
        tokio::time::timeout(self.command_timeout, receiver)
            .await
            .map_err(|_| {
                ServiceError::unavailable(anyhow::anyhow!("document actor remote update timed out"))
            })?
            .map_err(|_| {
                ServiceError::unavailable(anyhow::anyhow!(
                    "document actor stopped before applying a remote update"
                ))
            })?
    }

    async fn restore_version(
        &self,
        context: RequestContext,
        target: DocumentVersion,
        expected_sequence: i64,
        actor: PublicUser,
        idempotency_key: Option<String>,
    ) -> Result<DocumentVersion> {
        let (response, receiver) = oneshot::channel();
        self.send_command(ActorCommand::Restore {
            context,
            target: Box::new(target),
            expected_sequence,
            actor,
            idempotency_key,
            response,
        })
        .map_err(close_as_service_error)?;
        tokio::time::timeout(self.command_timeout, receiver)
            .await
            .map_err(|_| {
                ServiceError::unavailable(anyhow::anyhow!("document actor restoration timed out"))
            })?
            .map_err(|_| {
                ServiceError::unavailable(anyhow::anyhow!(
                    "document actor stopped before restoring the version"
                ))
            })?
    }

    async fn invalidate(&self, close: CloseSignal) -> Result<()> {
        self.send_invalidation(ActorCommand::Invalidate { close })
            .await
    }

    async fn invalidate_permissions(&self, permission_revision: i64) -> Result<()> {
        self.send_invalidation(ActorCommand::InvalidatePermissions {
            permission_revision,
        })
        .await
    }

    async fn send_invalidation(&self, command: ActorCommand) -> Result<()> {
        match tokio::time::timeout(self.command_timeout, self.commands.send(command)).await {
            Ok(Ok(())) => Ok(()),
            Ok(Err(_)) => Err(ServiceError::unavailable(anyhow::anyhow!(
                "document actor stopped before accepting an invalidation"
            ))),
            Err(_) => Err(ServiceError::unavailable(anyhow::anyhow!(
                "document actor invalidation queue timed out"
            ))),
        }
    }

    fn send_command(&self, command: ActorCommand) -> std::result::Result<(), CloseSignal> {
        match self.commands.try_send(command) {
            Ok(()) => Ok(()),
            Err(mpsc::error::TrySendError::Full(_)) => Err(CLOSE_SLOW_CONSUMER),
            Err(mpsc::error::TrySendError::Closed(_)) => Err(CLOSE_DEPENDENCY_UNAVAILABLE),
        }
    }
}

pub struct ActorConnection {
    id: Uuid,
    document_id: DocumentId,
    commands: mpsc::Sender<ActorCommand>,
    command_timeout: Duration,
    request_context: RequestContext,
    outbound: mpsc::Receiver<Vec<u8>>,
    close: watch::Receiver<Option<CloseSignal>>,
    disconnected: CancellationToken,
}

impl ActorConnection {
    pub fn document_id(&self) -> DocumentId {
        self.document_id
    }

    /// Sends one binary protocol frame to the owning document actor.
    ///
    /// # Errors
    ///
    /// Returns a close signal when the actor is unavailable, overloaded, or rejects the frame.
    pub async fn send(&self, payload: Vec<u8>) -> std::result::Result<(), CloseSignal> {
        let (response, receiver) = oneshot::channel();
        let command = ActorCommand::Frame {
            connection_id: self.id,
            payload,
            context: self.request_context.with_deadline(self.command_timeout),
            response,
        };
        match self.commands.try_send(command) {
            Ok(()) => {}
            Err(mpsc::error::TrySendError::Full(_)) => return Err(CLOSE_RATE_LIMITED),
            Err(mpsc::error::TrySendError::Closed(_)) => {
                return Err(CLOSE_DEPENDENCY_UNAVAILABLE);
            }
        }
        match tokio::time::timeout(self.command_timeout, receiver).await {
            Ok(Ok(result)) => result,
            Ok(Err(_)) | Err(_) => Err(CLOSE_DEPENDENCY_UNAVAILABLE),
        }
    }

    pub async fn recv(&mut self) -> ConnectionEvent {
        loop {
            if let Some(close) = *self.close.borrow() {
                return ConnectionEvent::Close(close);
            }
            tokio::select! {
                biased;
                changed = self.close.changed() => {
                    if changed.is_err() {
                        return ConnectionEvent::Close(CLOSE_DEPENDENCY_UNAVAILABLE);
                    }
                }
                payload = self.outbound.recv() => {
                    return payload.map_or(
                        ConnectionEvent::Close(CLOSE_DEPENDENCY_UNAVAILABLE),
                        ConnectionEvent::Binary,
                    );
                }
            }
        }
    }
}

impl Drop for ActorConnection {
    fn drop(&mut self) {
        self.disconnected.cancel();
        let _ = self.commands.try_send(ActorCommand::Disconnect {
            connection_id: self.id,
        });
    }
}

#[derive(Debug, Eq, PartialEq)]
pub enum ConnectionEvent {
    Binary(Vec<u8>),
    Close(CloseSignal),
}

enum ActorCommand {
    Connect {
        context: RequestContext,
        session: ActorSession,
        response: oneshot::Sender<std::result::Result<ActorConnection, CloseSignal>>,
    },
    Frame {
        connection_id: Uuid,
        payload: Vec<u8>,
        context: RequestContext,
        response: oneshot::Sender<std::result::Result<(), CloseSignal>>,
    },
    ApplyRemote {
        generation: i64,
        sequence: i64,
        response: oneshot::Sender<Result<()>>,
    },
    Restore {
        context: RequestContext,
        target: Box<DocumentVersion>,
        expected_sequence: i64,
        actor: PublicUser,
        idempotency_key: Option<String>,
        response: oneshot::Sender<Result<DocumentVersion>>,
    },
    Invalidate {
        close: CloseSignal,
    },
    InvalidatePermissions {
        permission_revision: i64,
    },
    Disconnect {
        connection_id: Uuid,
    },
}

struct ConnectionState {
    session: ActorSession,
    outbound: mpsc::Sender<Vec<u8>>,
    close: watch::Sender<Option<CloseSignal>>,
    disconnected: CancellationToken,
    awareness_clients: HashSet<ClientID>,
    rate: ConnectionRate,
}

struct ConnectionRate {
    window_started: std::time::Instant,
    updates: u32,
    awareness: u32,
}

#[derive(Clone, Copy)]
enum RateKind {
    Update,
    Awareness,
}

impl ConnectionRate {
    fn new() -> Self {
        Self {
            window_started: std::time::Instant::now(),
            updates: 0,
            awareness: 0,
        }
    }

    fn allow_at(&mut self, kind: RateKind, limit: u32, now: std::time::Instant) -> bool {
        if now.duration_since(self.window_started) >= Duration::from_secs(1) {
            self.window_started = now;
            self.updates = 0;
            self.awareness = 0;
        }
        let count = match kind {
            RateKind::Update => &mut self.updates,
            RateKind::Awareness => &mut self.awareness,
        };
        if *count >= limit {
            return false;
        }
        *count += 1;
        true
    }
}

struct DocumentActor {
    document_id: DocumentId,
    store: Arc<dyn DocumentStore>,
    limits: ActorLimits,
    metrics: Metrics,
    awareness: Awareness,
    generation: i64,
    sequence: i64,
    permission_revision: i64,
    connections: HashMap<Uuid, ConnectionState>,
}

struct ActorRestoreOutcome {
    result: Result<RestoreVersion>,
    invalidate_actor: bool,
}

#[allow(clippy::too_many_arguments, clippy::too_many_lines)]
async fn run_actor(
    initial_context: &RequestContext,
    document_id: DocumentId,
    command_sender: mpsc::Sender<ActorCommand>,
    mut commands: mpsc::Receiver<ActorCommand>,
    store: Arc<dyn DocumentStore>,
    limits: ActorLimits,
    metrics: Metrics,
    initial_permission_revision: i64,
    cancellation: CancellationToken,
) -> Result<()> {
    store
        .initialize_document(initial_context, document_id)
        .await?;
    let loaded = store.load_document(initial_context, document_id).await?;
    let document = richtext::document_from_state(&loaded.state)?;
    let mut actor = DocumentActor {
        document_id,
        store,
        limits,
        metrics,
        awareness: Awareness::new(document),
        generation: loaded.generation,
        sequence: loaded.sequence,
        permission_revision: initial_permission_revision,
        connections: HashMap::new(),
    };
    let idle = tokio::time::sleep(limits.idle_timeout);
    tokio::pin!(idle);
    let mut maintenance = tokio::time::interval(Duration::from_secs(1));
    maintenance.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    let mut was_empty = true;
    let mut idle_armed = true;

    loop {
        let is_empty = actor.connections.is_empty();
        if is_empty && !was_empty {
            idle.as_mut()
                .reset(tokio::time::Instant::now() + limits.idle_timeout);
            idle_armed = true;
        } else if !is_empty {
            idle_armed = false;
        }
        was_empty = is_empty;
        tokio::select! {
            biased;
            () = cancellation.cancelled() => {
                actor.close_all(CLOSE_SERVICE_RESTART);
                break;
            }
            () = &mut idle, if idle_armed => break,
            _ = maintenance.tick() => actor.prune_connections(),
            command = commands.recv() => {
                let Some(command) = command else {
                    actor.close_all(CLOSE_SERVICE_RESTART);
                    break;
                };
                match command {
                    ActorCommand::Connect {
                        context,
                        session,
                        response,
                    } => {
                        let _ = response.send(actor.connect(
                            session,
                            command_sender.clone(),
                            context,
                        ));
                    }
                    ActorCommand::Frame {
                        connection_id,
                        payload,
                        context,
                        response,
                    } => {
                        let result = actor.handle_frame(connection_id, &payload, &context).await;
                        if let Err(close) = result {
                            actor.close_connection(connection_id, close);
                        }
                        let _ = response.send(result);
                    }
                    ActorCommand::ApplyRemote { generation, sequence, response } => {
                        let result = actor.apply_remote(generation, sequence).await;
                        let failed = result.is_err();
                        if failed {
                            actor.close_all(CLOSE_DEPENDENCY_UNAVAILABLE);
                        }
                        let _ = response.send(result);
                        if failed {
                            break;
                        }
                    }
                    ActorCommand::Restore {
                        context,
                        target,
                        expected_sequence,
                        actor: restoring_actor,
                        idempotency_key,
                        mut response,
                    } => {
                        let result = tokio::select! {
                            biased;
                            result = actor.restore_version(
                                &context,
                                &target,
                                expected_sequence,
                                &restoring_actor,
                                idempotency_key.as_deref(),
                            ) => Some(result),
                            () = response.closed() => None,
                        };
                        let Some(result) = result else {
                            continue;
                        };
                        let invalidate_actor = result.invalidate_actor;
                        if invalidate_actor {
                            actor.close_all(CLOSE_DOCUMENT_INVALIDATED);
                        }
                        let _ = response.send(result.result.map(|restored| restored.version));
                        if invalidate_actor {
                            break;
                        }
                    }
                    ActorCommand::Invalidate { close } => {
                        actor.close_all(close);
                        break;
                    }
                    ActorCommand::InvalidatePermissions { permission_revision } => {
                        actor.invalidate_permissions(permission_revision);
                    }
                    ActorCommand::Disconnect { connection_id } => {
                        actor.disconnect(connection_id);
                    }
                }
            }
        }
    }
    actor.metrics.connections_removed(actor.connections.len());
    Ok(())
}

impl DocumentActor {
    fn connect(
        &mut self,
        session: ActorSession,
        commands: mpsc::Sender<ActorCommand>,
        request_context: RequestContext,
    ) -> std::result::Result<ActorConnection, CloseSignal> {
        self.prune_connections();
        if session.expires_at <= time::OffsetDateTime::now_utc() {
            return Err(CLOSE_SESSION_EXPIRED);
        }
        if session.permission_revision <= 0
            || session.permission_revision < self.permission_revision
        {
            return Err(CLOSE_FORBIDDEN);
        }
        if self.connections.len() >= self.limits.maximum_connections {
            return Err(CLOSE_RATE_LIMITED);
        }
        let id = Uuid::now_v7();
        let (outbound, receiver) = mpsc::channel(self.limits.outbound_capacity);
        let (close, close_receiver) = watch::channel(None);
        let disconnected = CancellationToken::new();
        let initial = self
            .initial_message()
            .map_err(|_| CLOSE_DEPENDENCY_UNAVAILABLE)?;
        outbound
            .try_send(initial)
            .map_err(|_| CLOSE_DEPENDENCY_UNAVAILABLE)?;
        self.permission_revision = self.permission_revision.max(session.permission_revision);
        self.connections.insert(
            id,
            ConnectionState {
                session,
                outbound,
                close,
                disconnected: disconnected.clone(),
                awareness_clients: HashSet::new(),
                rate: ConnectionRate::new(),
            },
        );
        self.metrics.connection_opened();
        Ok(ActorConnection {
            id,
            document_id: self.document_id,
            commands,
            command_timeout: self.limits.command_timeout,
            request_context,
            outbound: receiver,
            close: close_receiver,
            disconnected,
        })
    }

    fn initial_message(&self) -> Result<Vec<u8>> {
        let transaction = self.awareness.doc().transact();
        let state_vector = transaction.state_vector();
        drop(transaction);
        let awareness = self.awareness.update().map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("encode awareness state"))
        })?;
        Ok(encode_messages([
            SyncProtocolMessage::Sync(SyncMessage::SyncStep1(state_vector)),
            SyncProtocolMessage::Awareness(awareness),
        ]))
    }

    async fn restore_version(
        &mut self,
        context: &RequestContext,
        target: &DocumentVersion,
        expected_sequence: i64,
        actor: &PublicUser,
        idempotency_key: Option<&str>,
    ) -> ActorRestoreOutcome {
        let loaded = match self.store.load_document(context, self.document_id).await {
            Ok(loaded) => loaded,
            Err(error) => {
                return ActorRestoreOutcome {
                    result: Err(error),
                    invalidate_actor: false,
                };
            }
        };
        let generation_changed = loaded.generation != self.generation;
        let result = async {
            if !generation_changed {
                self.synchronize_loaded_document(&loaded)?;
            }
            let document = richtext::document_from_state(&loaded.state)?;
            let current_projection = richtext::projection_from_document(&document)?;
            let target_projection = richtext::projection_from_state(&target.state)?;
            let update = if current_projection == target_projection {
                vec![0, 0]
            } else {
                richtext::restore_state(&document, &target.state)?.0
            };
            drop(document);
            self.store
                .commit_restoration(
                    context,
                    self.document_id,
                    RestorationCandidate {
                        target,
                        baseline_generation: loaded.generation,
                        baseline_sequence: loaded.sequence,
                        expected_sequence,
                        update: &update,
                        actor,
                        idempotency_key,
                        limits: self.limits.update,
                    },
                )
                .await
        }
        .await;
        let committed = result
            .as_ref()
            .is_ok_and(|restored| restored.committed.is_some());
        ActorRestoreOutcome {
            result,
            invalidate_actor: generation_changed || committed,
        }
    }

    async fn handle_frame(
        &mut self,
        connection_id: Uuid,
        payload: &[u8],
        context: &RequestContext,
    ) -> std::result::Result<(), CloseSignal> {
        let messages = decode_messages(payload)?;
        for message in messages {
            if self.session_expired(connection_id) {
                return Err(CLOSE_SESSION_EXPIRED);
            }
            match message {
                SyncProtocolMessage::Sync(SyncMessage::SyncStep1(state_vector)) => {
                    let update = self
                        .awareness
                        .doc()
                        .transact()
                        .encode_state_as_update_v1(&state_vector);
                    self.send_to(
                        connection_id,
                        encode_message(&SyncProtocolMessage::Sync(SyncMessage::SyncStep2(update))),
                    )?;
                }
                SyncProtocolMessage::Sync(
                    SyncMessage::SyncStep2(update) | SyncMessage::Update(update),
                ) => {
                    self.check_rate(connection_id, RateKind::Update)?;
                    self.apply_client_update(connection_id, &update, context)
                        .await?;
                }
                SyncProtocolMessage::Awareness(update) => {
                    self.check_rate(connection_id, RateKind::Awareness)?;
                    let encoded = update.encode_v1();
                    if encoded.len() > self.limits.maximum_awareness_bytes {
                        return Err(CLOSE_INVALID_PROTOCOL);
                    }
                    self.validate_awareness_ownership(connection_id, &update)?;
                    self.awareness
                        .apply_update(update.clone())
                        .map_err(|_| CLOSE_INVALID_PROTOCOL)?;
                    self.record_awareness_ownership(connection_id, &update);
                    self.broadcast(
                        &encode_message(&SyncProtocolMessage::Awareness(update)),
                        Some(connection_id),
                    );
                }
                SyncProtocolMessage::AwarenessQuery => {
                    let update = self
                        .awareness
                        .update()
                        .map_err(|_| CLOSE_DEPENDENCY_UNAVAILABLE)?;
                    self.send_to(
                        connection_id,
                        encode_message(&SyncProtocolMessage::Awareness(update)),
                    )?;
                }
                SyncProtocolMessage::Auth(_) | SyncProtocolMessage::Custom(_, _) => {
                    return Err(CLOSE_INVALID_PROTOCOL);
                }
            }
        }
        Ok(())
    }

    async fn apply_client_update(
        &mut self,
        connection_id: Uuid,
        update: &[u8],
        context: &RequestContext,
    ) -> std::result::Result<(), CloseSignal> {
        let Some(connection) = self.connections.get(&connection_id) else {
            return Err(CLOSE_DEPENDENCY_UNAVAILABLE);
        };
        if !connection.session.access.can_write() {
            return Err(CLOSE_FORBIDDEN);
        }
        if update.is_empty() || update.len() > self.limits.update.maximum_update_bytes {
            return Err(CLOSE_INVALID_UPDATE);
        }
        let actor = connection.session.actor.clone();
        let started = std::time::Instant::now();
        let committed = self
            .store
            .append_update(
                context,
                self.document_id,
                update,
                &actor,
                self.limits.update,
            )
            .await
            .map_err(|error| close_for_service_error(&error));
        self.metrics
            .observe_update(started.elapsed(), committed.as_ref().err().copied());
        let committed = committed?;
        if committed.generation != self.generation {
            self.close_all(CLOSE_DOCUMENT_INVALIDATED);
            return Err(CLOSE_DOCUMENT_INVALIDATED);
        }

        let committed_document = richtext::document_from_state(&committed.state)
            .map_err(|_| CLOSE_DEPENDENCY_UNAVAILABLE)?;
        let current_vector = self.awareness.doc().transact().state_vector();
        let catch_up = committed_document
            .transact()
            .encode_state_as_update_v1(&current_vector);
        if !is_empty_update(&catch_up) {
            let decoded = Update::decode_v1(&catch_up).map_err(|_| CLOSE_DEPENDENCY_UNAVAILABLE)?;
            self.awareness
                .doc_mut()
                .transact_mut()
                .apply_update(decoded)
                .map_err(|_| CLOSE_DEPENDENCY_UNAVAILABLE)?;
            self.broadcast(
                &encode_message(&SyncProtocolMessage::Sync(SyncMessage::Update(catch_up))),
                None,
            );
        }
        self.sequence = committed.sequence;
        Ok(())
    }

    async fn apply_remote(&mut self, generation: i64, target_sequence: i64) -> Result<()> {
        if target_sequence <= self.sequence && generation == self.generation {
            return Ok(());
        }
        if generation != self.generation || target_sequence < 0 {
            self.close_all(CLOSE_DOCUMENT_INVALIDATED);
            return Err(ServiceError::conflict("document generation changed"));
        }
        while self.sequence < target_sequence {
            let updates = self
                .store
                .updates_after(
                    &operation_context(self.limits.command_timeout),
                    self.document_id,
                    self.sequence,
                    REMOTE_UPDATE_BATCH,
                )
                .await?;
            if updates.is_empty()
                || updates
                    .first()
                    .is_none_or(|update| update.sequence != self.sequence + 1)
            {
                return self.reload_committed_state(target_sequence).await;
            }
            if self
                .apply_committed_batch(updates, target_sequence)
                .is_err()
            {
                return self.reload_committed_state(target_sequence).await;
            }
        }
        Ok(())
    }

    async fn reload_committed_state(&mut self, target_sequence: i64) -> Result<()> {
        let loaded = self
            .store
            .load_document(
                &operation_context(self.limits.command_timeout),
                self.document_id,
            )
            .await?;
        if loaded.generation != self.generation {
            self.close_all(CLOSE_DOCUMENT_INVALIDATED);
            return Err(ServiceError::conflict("document generation changed"));
        }
        if loaded.sequence < target_sequence {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "committed document snapshot is behind the announced sequence"
            )));
        }
        self.synchronize_loaded_document(&loaded)
    }

    fn synchronize_loaded_document(&mut self, loaded: &LoadedDocument) -> Result<()> {
        if loaded.generation != self.generation {
            return Err(ServiceError::conflict("document generation changed"));
        }
        if loaded.sequence < self.sequence {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "committed document snapshot is behind the actor sequence"
            )));
        }
        if loaded.sequence == self.sequence {
            return Ok(());
        }
        let committed = richtext::document_from_state(&loaded.state)?;
        let current_vector = self.awareness.doc().transact().state_vector();
        let catch_up = committed
            .transact()
            .encode_state_as_update_v1(&current_vector);
        if !is_empty_update(&catch_up) {
            let update = Update::decode_v1(&catch_up).map_err(|error| {
                ServiceError::internal(anyhow::anyhow!(error).context("decode snapshot catch-up"))
            })?;
            self.awareness
                .doc_mut()
                .transact_mut()
                .apply_update(update)
                .map_err(|error| {
                    ServiceError::internal(
                        anyhow::anyhow!(error).context("apply snapshot catch-up"),
                    )
                })?;
            self.broadcast(
                &encode_message(&SyncProtocolMessage::Sync(SyncMessage::Update(catch_up))),
                None,
            );
        }
        self.sequence = loaded.sequence;
        Ok(())
    }

    fn apply_committed_batch(
        &mut self,
        updates: Vec<StoredUpdate>,
        target_sequence: i64,
    ) -> Result<()> {
        for stored in updates {
            if stored.sequence > target_sequence {
                break;
            }
            if stored.generation != self.generation || stored.sequence != self.sequence + 1 {
                return Err(ServiceError::conflict(
                    "committed document update sequence is not contiguous",
                ));
            }
            let update = Update::decode_v1(&stored.update).map_err(|error| {
                ServiceError::internal(anyhow::anyhow!(error).context("decode committed update"))
            })?;
            self.awareness
                .doc_mut()
                .transact_mut()
                .apply_update(update)
                .map_err(|error| {
                    ServiceError::internal(anyhow::anyhow!(error).context("apply committed update"))
                })?;
            self.sequence = stored.sequence;
            self.broadcast(
                &encode_message(&SyncProtocolMessage::Sync(SyncMessage::Update(
                    stored.update,
                ))),
                None,
            );
        }
        Ok(())
    }

    fn send_to(
        &mut self,
        connection_id: Uuid,
        payload: Vec<u8>,
    ) -> std::result::Result<(), CloseSignal> {
        let Some(connection) = self.connections.get(&connection_id) else {
            return Err(CLOSE_DEPENDENCY_UNAVAILABLE);
        };
        if connection.outbound.try_send(payload).is_err() {
            self.close_connection(connection_id, CLOSE_SLOW_CONSUMER);
            return Err(CLOSE_SLOW_CONSUMER);
        }
        Ok(())
    }

    fn broadcast(&mut self, payload: &[u8], excluded: Option<Uuid>) {
        let mut slow = Vec::new();
        for (connection_id, connection) in &self.connections {
            if Some(*connection_id) == excluded || connection.disconnected.is_cancelled() {
                continue;
            }
            if connection.outbound.try_send(payload.to_owned()).is_err() {
                slow.push(*connection_id);
            }
        }
        for connection_id in slow {
            self.close_connection(connection_id, CLOSE_SLOW_CONSUMER);
        }
    }

    fn session_expired(&self, connection_id: Uuid) -> bool {
        self.connections
            .get(&connection_id)
            .is_none_or(|connection| {
                connection.session.expires_at <= time::OffsetDateTime::now_utc()
            })
    }

    fn check_rate(
        &mut self,
        connection_id: Uuid,
        kind: RateKind,
    ) -> std::result::Result<(), CloseSignal> {
        let limit = match kind {
            RateKind::Update => self.limits.updates_per_second,
            RateKind::Awareness => self.limits.awareness_messages_per_second,
        };
        let connection = self
            .connections
            .get_mut(&connection_id)
            .ok_or(CLOSE_DEPENDENCY_UNAVAILABLE)?;
        if connection
            .rate
            .allow_at(kind, limit, std::time::Instant::now())
        {
            Ok(())
        } else {
            Err(CLOSE_RATE_LIMITED)
        }
    }

    fn validate_awareness_ownership(
        &self,
        connection_id: Uuid,
        update: &yrs::sync::AwarenessUpdate,
    ) -> std::result::Result<(), CloseSignal> {
        let connection = self
            .connections
            .get(&connection_id)
            .ok_or(CLOSE_DEPENDENCY_UNAVAILABLE)?;
        let mut newly_owned = 0;
        for (client_id, entry) in &update.clients {
            if self.connections.iter().any(|(other_id, other)| {
                other_id != &connection_id && other.awareness_clients.contains(client_id)
            }) {
                return Err(CLOSE_FORBIDDEN);
            }
            if !connection.awareness_clients.contains(client_id) {
                if entry.json.as_ref() == "null" {
                    return Err(CLOSE_INVALID_PROTOCOL);
                }
                newly_owned += 1;
            }
        }
        if connection.awareness_clients.len() + newly_owned > MAX_AWARENESS_CLIENTS_PER_CONNECTION {
            Err(CLOSE_INVALID_PROTOCOL)
        } else {
            Ok(())
        }
    }

    fn record_awareness_ownership(
        &mut self,
        connection_id: Uuid,
        update: &yrs::sync::AwarenessUpdate,
    ) {
        if let Some(connection) = self.connections.get_mut(&connection_id) {
            for (client_id, entry) in &update.clients {
                if entry.json.as_ref() == "null" {
                    connection.awareness_clients.remove(client_id);
                } else {
                    connection.awareness_clients.insert(*client_id);
                }
            }
        }
    }

    fn prune_connections(&mut self) {
        let now = time::OffsetDateTime::now_utc();
        let mut closed = Vec::new();
        for (connection_id, connection) in &self.connections {
            if connection.disconnected.is_cancelled() {
                closed.push((*connection_id, None));
            } else if connection.session.expires_at <= now {
                closed.push((*connection_id, Some(CLOSE_SESSION_EXPIRED)));
            }
        }
        for (connection_id, close) in closed {
            if let Some(close) = close {
                self.close_connection(connection_id, close);
            } else if let Some(connection) = self.connections.remove(&connection_id) {
                self.remove_awareness(connection.awareness_clients);
                self.metrics.connection_closed("disconnected");
            }
        }
    }

    fn invalidate_permissions(&mut self, permission_revision: i64) {
        self.permission_revision = self.permission_revision.max(permission_revision);
        let stale_connections = self
            .connections
            .iter()
            .filter_map(|(connection_id, connection)| {
                (connection.session.permission_revision < permission_revision)
                    .then_some(*connection_id)
            })
            .collect::<Vec<_>>();
        for connection_id in stale_connections {
            self.close_connection(connection_id, CLOSE_FORBIDDEN);
        }
    }

    fn close_connection(&mut self, connection_id: Uuid, close: CloseSignal) {
        if let Some(connection) = self.connections.remove(&connection_id) {
            let awareness_clients = connection.awareness_clients.clone();
            connection.close.send_replace(Some(close));
            self.remove_awareness(awareness_clients);
            self.metrics.connection_closed(close.reason);
        }
    }

    fn disconnect(&mut self, connection_id: Uuid) {
        if let Some(connection) = self.connections.remove(&connection_id) {
            self.remove_awareness(connection.awareness_clients);
            self.metrics.connection_closed("disconnected");
        }
    }

    fn close_all(&mut self, close: CloseSignal) {
        for (_, connection) in self.connections.drain() {
            connection.close.send_replace(Some(close));
            self.metrics.connection_closed(close.reason);
        }
    }

    fn remove_awareness(&mut self, client_ids: HashSet<ClientID>) {
        if client_ids.is_empty() {
            return;
        }
        for client_id in &client_ids {
            self.awareness.remove_state(*client_id);
        }
        if let Ok(update) = self.awareness.update_with_clients(client_ids) {
            self.broadcast(
                &encode_message(&SyncProtocolMessage::Awareness(update)),
                None,
            );
        }
    }
}

fn decode_messages(payload: &[u8]) -> std::result::Result<Vec<SyncProtocolMessage>, CloseSignal> {
    if payload.is_empty() {
        return Err(CLOSE_INVALID_PROTOCOL);
    }
    let mut decoder = DecoderV1::new(Cursor::new(payload));
    let messages = MessageReader::new(&mut decoder)
        .collect::<std::result::Result<Vec<_>, _>>()
        .map_err(|_| CLOSE_INVALID_PROTOCOL)?;
    if messages.is_empty() {
        Err(CLOSE_INVALID_PROTOCOL)
    } else {
        Ok(messages)
    }
}

fn encode_message(message: &SyncProtocolMessage) -> Vec<u8> {
    message.encode_v1()
}

fn encode_messages(messages: impl IntoIterator<Item = SyncProtocolMessage>) -> Vec<u8> {
    let mut encoder = EncoderV1::new();
    for message in messages {
        message.encode(&mut encoder);
    }
    encoder.to_vec()
}

fn is_empty_update(update: &[u8]) -> bool {
    update == [0, 0]
}

fn close_for_service_error(error: &ServiceError) -> CloseSignal {
    match error.code() {
        ErrorCode::InvalidInput => CLOSE_INVALID_UPDATE,
        ErrorCode::Unauthenticated => CLOSE_SESSION_EXPIRED,
        ErrorCode::Forbidden => CLOSE_FORBIDDEN,
        ErrorCode::NotFound | ErrorCode::Conflict | ErrorCode::PreconditionFailed => {
            CLOSE_DOCUMENT_INVALIDATED
        }
        ErrorCode::Unavailable | ErrorCode::Internal => CLOSE_DEPENDENCY_UNAVAILABLE,
    }
}

fn close_as_service_error(close: CloseSignal) -> ServiceError {
    ServiceError::unavailable(anyhow::anyhow!(
        "document actor rejected a command with close code {}",
        close.code
    ))
}

fn operation_context(maximum_wait: Duration) -> RequestContext {
    let mut context = RequestContext::new(Uuid::now_v7().simple().to_string());
    context.deadline = std::time::Instant::now().checked_add(maximum_wait);
    context
}

fn lock_abort_handles(
    handles: &StdMutex<HashMap<Uuid, AbortHandle>>,
) -> std::sync::MutexGuard<'_, HashMap<Uuid, AbortHandle>> {
    match handles.lock() {
        Ok(handles) => handles,
        Err(poisoned) => poisoned.into_inner(),
    }
}

#[cfg(test)]
mod tests {
    use std::{
        sync::{
            Arc,
            atomic::{AtomicBool, Ordering},
        },
        time::{Duration, Instant},
    };

    use super::{
        ActorTasks, CLOSE_INVALID_PROTOCOL, ConnectionRate, PermissionRevisionCache, RateKind,
        SyncMessage, SyncProtocolMessage, decode_messages, encode_message, lock_abort_handles,
    };
    use crate::domain::DocumentId;
    use tokio::sync::Notify;
    use uuid::Uuid;
    use yrs::StateVector;

    struct DropSignal(Arc<AtomicBool>);

    impl Drop for DropSignal {
        fn drop(&mut self) {
            self.0.store(true, Ordering::Release);
        }
    }

    #[tokio::test]
    async fn overdue_actor_task_is_aborted_and_joined() {
        let tasks = ActorTasks::default();
        let started = Arc::new(Notify::new());
        let dropped = Arc::new(AtomicBool::new(false));
        tasks.spawn(Uuid::now_v7(), {
            let started = Arc::clone(&started);
            let dropped = Arc::clone(&dropped);
            async move {
                let _drop_signal = DropSignal(dropped);
                started.notify_one();
                std::future::pending::<()>().await;
            }
        });
        started.notified().await;

        assert!(tasks.shutdown(Duration::ZERO).await.is_err());
        assert!(dropped.load(Ordering::Acquire));
        assert!(lock_abort_handles(&tasks.abort_handles).is_empty());
    }

    #[test]
    fn y_sync_messages_round_trip_and_reject_empty_frames() {
        let message = SyncProtocolMessage::Sync(SyncMessage::SyncStep1(StateVector::default()));
        let encoded = encode_message(&message);
        assert_eq!(decode_messages(&encoded).expect("valid message"), [message]);
        assert_eq!(decode_messages(&[]), Err(CLOSE_INVALID_PROTOCOL));
    }

    #[test]
    fn rate_window_is_bounded_and_resets_deterministically() {
        let started = Instant::now();
        let mut rate = ConnectionRate {
            window_started: started,
            updates: 0,
            awareness: 0,
        };
        assert!(rate.allow_at(RateKind::Update, 2, started));
        assert!(rate.allow_at(RateKind::Update, 2, started));
        assert!(!rate.allow_at(RateKind::Update, 2, started));
        assert!(rate.allow_at(RateKind::Update, 2, started + Duration::from_secs(1)));
    }

    #[test]
    fn permission_revision_cache_keeps_the_high_watermark_and_expires() {
        let started = Instant::now();
        let document_id = DocumentId::new();
        let mut cache = PermissionRevisionCache::new(Duration::from_millis(10));
        cache.record(document_id, 3, started);
        cache.record(document_id, 2, started + Duration::from_millis(1));
        assert_eq!(cache.revision(document_id, started), Some(3));
        assert_eq!(
            cache.revision(document_id, started + Duration::from_millis(12)),
            None
        );
        assert!(cache.revisions.is_empty());
    }
}
