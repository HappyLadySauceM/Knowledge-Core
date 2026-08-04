use std::{
    collections::HashMap,
    sync::{
        Arc, Mutex as StdMutex,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    task::Poll,
    time::Duration,
};

use async_trait::async_trait;
use knowledge_core_collaboration::{
    actor::{
        ActorLimits, ActorRegistry, ActorSession, CLOSE_DEPENDENCY_UNAVAILABLE,
        CLOSE_DOCUMENT_INVALIDATED, CLOSE_FORBIDDEN, CLOSE_SESSION_EXPIRED, CLOSE_SLOW_CONSUMER,
        ConnectionEvent,
    },
    config::{ActorConfig, PublicConfig},
    domain::{
        Access, DocumentId, DocumentVersion, PublicUser, RequestContext, VersionId, VersionKind,
    },
    error::{Result, ServiceError},
    richtext,
    storage::{
        CommittedUpdate, DocumentStore, LoadedDocument, RestorationCandidate, RestoreVersion,
        StoredUpdate, UpdateLimits,
    },
    telemetry::Metrics,
};
use tokio::sync::{Mutex, Notify, Semaphore};
use tokio_util::sync::CancellationToken;
use yrs::{
    ReadTxn, Transact, XmlElementPrelim, XmlFragment,
    block::ClientID,
    encoding::read::Cursor,
    sync::{AwarenessUpdate, Message, MessageReader, SyncMessage, awareness::AwarenessUpdateEntry},
    updates::{decoder::DecoderV1, encoder::Encode},
};

struct MockStore {
    first: LoadedDocument,
    current: Mutex<LoadedDocument>,
    updates: Mutex<Vec<StoredUpdate>>,
    load_count: AtomicUsize,
    block_append: AtomicBool,
    append_started: Semaphore,
    append_release: Notify,
    block_restore: AtomicBool,
    restore_started: Semaphore,
    restore_release: Notify,
    restorations: Mutex<Vec<RestoreObservation>>,
    initial_contexts: StdMutex<Vec<(Arc<str>, Option<std::time::Instant>)>>,
}

struct RestoreObservation {
    request_id: Arc<str>,
    deadline: Option<std::time::Instant>,
    baseline_generation: i64,
    baseline_sequence: i64,
    expected_sequence: i64,
}

impl MockStore {
    fn new() -> Self {
        let state = richtext::initial_state();
        let loaded = LoadedDocument {
            generation: 0,
            sequence: 0,
            state,
        };
        Self {
            first: loaded.clone(),
            current: Mutex::new(loaded),
            updates: Mutex::new(Vec::new()),
            load_count: AtomicUsize::new(0),
            block_append: AtomicBool::new(false),
            append_started: Semaphore::new(0),
            append_release: Notify::new(),
            block_restore: AtomicBool::new(false),
            restore_started: Semaphore::new(0),
            restore_release: Notify::new(),
            restorations: Mutex::new(Vec::new()),
            initial_contexts: StdMutex::new(Vec::new()),
        }
    }

    async fn with_remote_snapshot() -> Self {
        let store = Self::new();
        let update = append_paragraph_update(&store.first.state);
        let state = richtext::merge_updates(&store.first.state, &[update]).expect("remote state");
        *store.current.lock().await = LoadedDocument {
            generation: 0,
            sequence: 2,
            state,
        };
        store
    }

    fn block_next_append(&self) {
        self.block_append.store(true, Ordering::Release);
    }

    async fn wait_for_append(&self) {
        self.append_started
            .acquire()
            .await
            .expect("append signal")
            .forget();
    }

    fn release_append(&self) {
        self.append_release.notify_one();
    }

    fn block_next_restore(&self) {
        self.block_restore.store(true, Ordering::Release);
    }

    async fn wait_for_restore(&self) {
        self.restore_started
            .acquire()
            .await
            .expect("restore signal")
            .forget();
    }

    fn release_restore(&self) {
        self.restore_release.notify_one();
    }
}

#[async_trait]
impl DocumentStore for MockStore {
    async fn initialize_document(
        &self,
        context: &RequestContext,
        _document_id: DocumentId,
    ) -> Result<()> {
        self.initial_contexts
            .lock()
            .expect("initial context lock")
            .push((Arc::clone(&context.request_id), context.deadline));
        Ok(())
    }

    async fn load_document(
        &self,
        context: &RequestContext,
        _document_id: DocumentId,
    ) -> Result<LoadedDocument> {
        self.initial_contexts
            .lock()
            .expect("initial context lock")
            .push((Arc::clone(&context.request_id), context.deadline));
        if self.load_count.fetch_add(1, Ordering::AcqRel) == 0 {
            Ok(self.first.clone())
        } else {
            Ok(self.current.lock().await.clone())
        }
    }

    async fn append_update(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
        update: &[u8],
        _actor: &PublicUser,
        _limits: UpdateLimits,
    ) -> Result<CommittedUpdate> {
        if self.block_append.swap(false, Ordering::AcqRel) {
            self.append_started.add_permits(1);
            self.append_release.notified().await;
        }
        let mut current = self.current.lock().await;
        let state = richtext::merge_updates(&current.state, &[update.to_vec()])?;
        let sequence = current.sequence + 1;
        let projection = richtext::projection_from_state(&state)?;
        *current = LoadedDocument {
            generation: current.generation,
            sequence,
            state: state.clone(),
        };
        self.updates.lock().await.push(StoredUpdate {
            generation: current.generation,
            sequence,
            update: update.to_vec(),
        });
        Ok(CommittedUpdate {
            generation: current.generation,
            sequence,
            state,
            projection,
            update: Some(update.to_vec()),
        })
    }

    async fn commit_restoration(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        candidate: RestorationCandidate<'_>,
    ) -> Result<RestoreVersion> {
        self.restorations.lock().await.push(RestoreObservation {
            request_id: Arc::clone(&context.request_id),
            deadline: context.deadline,
            baseline_generation: candidate.baseline_generation,
            baseline_sequence: candidate.baseline_sequence,
            expected_sequence: candidate.expected_sequence,
        });
        if self.block_restore.swap(false, Ordering::AcqRel) {
            self.restore_started.add_permits(1);
            self.restore_release.notified().await;
        }
        let mut current = self.current.lock().await;
        if current.generation != candidate.baseline_generation
            || current.sequence != candidate.baseline_sequence
            || current.sequence != candidate.expected_sequence
        {
            return Err(ServiceError::precondition_failed());
        }
        let validated = richtext::candidate_from_update(
            &current.state,
            candidate.update,
            candidate.limits.maximum_update_bytes,
            candidate.limits.maximum_document_bytes,
        )?;
        let (state, projection, update) = if let Some((document, projection)) = validated {
            (
                richtext::full_state(&document),
                projection,
                Some(candidate.update.to_vec()),
            )
        } else {
            (
                current.state.clone(),
                richtext::projection_from_state(&current.state)?,
                None,
            )
        };
        if projection != richtext::projection_from_state(&candidate.target.state)? {
            return Err(ServiceError::internal(anyhow::anyhow!(
                "test restoration did not reach target projection"
            )));
        }
        let generation = current.generation + 1;
        let sequence = current.sequence + i64::from(update.is_some());
        *current = LoadedDocument {
            generation,
            sequence,
            state: state.clone(),
        };
        Ok(RestoreVersion {
            version: DocumentVersion {
                id: VersionId::new(),
                document_id,
                sequence,
                kind: VersionKind::Restoration,
                label: Some(format!("Restored from {}", candidate.target.id)),
                state: state.clone(),
                created_by: candidate.actor.clone(),
                created_at: time::OffsetDateTime::now_utc(),
            },
            committed: Some(CommittedUpdate {
                generation,
                sequence,
                state,
                projection,
                update,
            }),
        })
    }

    async fn updates_after(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
        sequence: i64,
        limit: i64,
    ) -> Result<Vec<StoredUpdate>> {
        Ok(self
            .updates
            .lock()
            .await
            .iter()
            .filter(|update| update.sequence > sequence)
            .take(usize::try_from(limit).unwrap_or(usize::MAX))
            .cloned()
            .collect())
    }

    async fn current_sequence(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
    ) -> Result<i64> {
        Ok(self.current.lock().await.sequence)
    }
}

#[tokio::test]
async fn actor_initialization_reuses_the_handshake_request_context() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store.clone(), 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let context = request_context();
    let mut connection = registry
        .connect(&context, document_id, session(Access::Editor, 1))
        .await
        .expect("connection");
    drain_initial(&mut connection).await;

    {
        let observations = store.initial_contexts.lock().expect("initial context lock");
        assert_eq!(observations.len(), 2);
        assert!(observations.iter().all(|(request_id, deadline)| {
            Arc::ptr_eq(request_id, &context.request_id) && *deadline == context.deadline
        }));
    }
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn committed_update_is_invisible_until_storage_releases() {
    let store = Arc::new(MockStore::new());
    store.block_next_append();
    let registry = registry(store.clone(), 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut writer = registry
        .connect(&request_context(), document_id, session(Access::Editor, 1))
        .await
        .expect("writer");
    let mut observer = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 2))
        .await
        .expect("observer");
    drain_initial(&mut writer).await;
    drain_initial(&mut observer).await;

    let frame = update_frame(append_paragraph_update(&store.first.state));
    let send = tokio::spawn(async move { writer.send(frame).await });
    store.wait_for_append().await;
    let receive = observer.recv();
    tokio::pin!(receive);
    assert!(matches!(futures_util::poll!(&mut receive), Poll::Pending));

    store.release_append();
    assert!(send.await.expect("send task").is_ok());
    assert!(matches!(receive.await, ConnectionEvent::Binary(_)));
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn restoration_uses_the_latest_serialized_state_and_closes_after_commit() {
    let store = Arc::new(MockStore::new());
    store.block_next_append();
    store.block_next_restore();
    let registry = registry(store.clone(), 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut writer = registry
        .connect(&request_context(), document_id, session(Access::Editor, 1))
        .await
        .expect("writer");
    let mut observer = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 2))
        .await
        .expect("observer");
    drain_initial(&mut writer).await;
    drain_initial(&mut observer).await;

    let update = append_paragraph_update(&store.first.state);
    let send = tokio::spawn(async move { writer.send(update_frame(update)).await });
    store.wait_for_append().await;
    let restore_context = request_context();
    let restore_context_for_task = restore_context.clone();
    let restore_registry = registry.clone();
    let target = manual_version(document_id, 0, richtext::initial_state());
    let restore = tokio::spawn(async move {
        restore_registry
            .restore_version(
                &restore_context_for_task,
                document_id,
                target,
                1,
                session(Access::Owner, 3).actor,
                Some("serialized-restore".to_owned()),
            )
            .await
    });

    store.release_append();
    send.await.expect("update task").expect("committed update");
    assert!(matches!(observer.recv().await, ConnectionEvent::Binary(_)));
    store.wait_for_restore().await;
    let receive = observer.recv();
    tokio::pin!(receive);
    assert!(matches!(futures_util::poll!(&mut receive), Poll::Pending));
    {
        let observations = store.restorations.lock().await;
        let observation = observations.last().expect("restoration observation");
        assert!(Arc::ptr_eq(
            &observation.request_id,
            &restore_context.request_id
        ));
        assert_eq!(observation.deadline, restore_context.deadline);
        assert_eq!(observation.baseline_generation, 0);
        assert_eq!(observation.baseline_sequence, 1);
        assert_eq!(observation.expected_sequence, 1);
    }

    store.release_restore();
    let restored = restore.await.expect("restore task").expect("restoration");
    assert_eq!(restored.kind, VersionKind::Restoration);
    assert_eq!(restored.sequence, 2);
    assert_eq!(
        receive.await,
        ConnectionEvent::Close(knowledge_core_collaboration::actor::CLOSE_DOCUMENT_INVALIDATED)
    );
    wait_until_no_actors(&registry).await;
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn no_op_restoration_advances_generation_and_invalidates_connections() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store.clone(), 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut connection = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 1))
        .await
        .expect("connection");
    drain_initial(&mut connection).await;

    let restored = registry
        .restore_version(
            &request_context(),
            document_id,
            manual_version(document_id, 0, richtext::initial_state()),
            0,
            session(Access::Owner, 2).actor,
            Some("no-op-restore".to_owned()),
        )
        .await
        .expect("no-op restoration");
    assert_eq!(restored.sequence, 0);
    assert_eq!(store.current.lock().await.generation, 1);
    assert_eq!(
        connection.recv().await,
        ConnectionEvent::Close(knowledge_core_collaboration::actor::CLOSE_DOCUMENT_INVALIDATED)
    );
    wait_until_no_actors(&registry).await;
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn failed_restoration_keeps_current_connections_open() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store, 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut connection = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 1))
        .await
        .expect("connection");
    drain_initial(&mut connection).await;

    let error = registry
        .restore_version(
            &request_context(),
            document_id,
            manual_version(document_id, 0, richtext::initial_state()),
            1,
            session(Access::Owner, 2).actor,
            None,
        )
        .await
        .expect_err("stale restoration must fail");
    assert_eq!(
        error.code(),
        knowledge_core_collaboration::error::ErrorCode::PreconditionFailed
    );
    {
        let receive = connection.recv();
        tokio::pin!(receive);
        assert!(matches!(futures_util::poll!(&mut receive), Poll::Pending));
    }
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn restoration_closes_an_actor_that_missed_a_generation_change() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store.clone(), 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut connection = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 1))
        .await
        .expect("connection");
    drain_initial(&mut connection).await;
    store.current.lock().await.generation = 1;

    let error = registry
        .restore_version(
            &request_context(),
            document_id,
            manual_version(document_id, 0, richtext::initial_state()),
            1,
            session(Access::Owner, 2).actor,
            None,
        )
        .await
        .expect_err("stale generation restoration must fail");
    assert_eq!(
        error.code(),
        knowledge_core_collaboration::error::ErrorCode::PreconditionFailed
    );
    assert_eq!(
        connection.recv().await,
        ConnectionEvent::Close(knowledge_core_collaboration::actor::CLOSE_DOCUMENT_INVALIDATED)
    );
    wait_until_no_actors(&registry).await;
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn viewer_update_closes_with_forbidden_code() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store.clone(), 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut viewer = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 1))
        .await
        .expect("viewer");
    drain_initial(&mut viewer).await;

    let error = viewer
        .send(update_frame(append_paragraph_update(&store.first.state)))
        .await
        .expect_err("viewer update must fail");
    assert_eq!(error, CLOSE_FORBIDDEN);
    assert_eq!(viewer.recv().await, ConnectionEvent::Close(CLOSE_FORBIDDEN));
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn full_outbound_queue_closes_the_slow_connection() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store.clone(), 1, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut writer = registry
        .connect(&request_context(), document_id, session(Access::Editor, 1))
        .await
        .expect("writer");
    let mut slow = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 2))
        .await
        .expect("slow observer");
    drain_initial(&mut writer).await;

    writer
        .send(update_frame(append_paragraph_update(&store.first.state)))
        .await
        .expect("commit update");
    assert_eq!(
        slow.recv().await,
        ConnectionEvent::Close(CLOSE_SLOW_CONSUMER)
    );
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn disconnected_actor_is_removed_after_its_idle_deadline() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store, 8, Duration::from_millis(10));
    let document_id = DocumentId::new();
    let mut connection = registry
        .connect(&request_context(), document_id, session(Access::Editor, 1))
        .await
        .expect("connection");
    drain_initial(&mut connection).await;
    drop(connection);

    tokio::time::timeout(Duration::from_secs(1), async {
        while registry.active_documents().await != 0 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("actor idle removal");
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn actor_with_a_rejected_first_connection_still_expires_when_idle() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store, 8, Duration::from_millis(10));
    let document_id = DocumentId::new();
    let mut expired = session(Access::Editor, 1);
    expired.expires_at = time::OffsetDateTime::now_utc() - time::Duration::seconds(1);
    assert_eq!(
        registry
            .connect(&request_context(), document_id, expired)
            .await
            .err(),
        Some(CLOSE_SESSION_EXPIRED)
    );

    wait_until_no_actors(&registry).await;
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn disconnect_removes_owned_awareness_and_broadcasts_null_state() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store, 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut owner = registry
        .connect(&request_context(), document_id, session(Access::Editor, 1))
        .await
        .expect("owner");
    let mut observer = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 2))
        .await
        .expect("observer");
    drain_initial(&mut owner).await;
    drain_initial(&mut observer).await;

    let client_id = ClientID::new(77);
    let update = AwarenessUpdate {
        clients: HashMap::from([(
            client_id,
            AwarenessUpdateEntry {
                clock: 1,
                json: Arc::from(r#"{"user":"one"}"#),
            },
        )]),
    };
    owner
        .send(Message::Awareness(update).encode_v1())
        .await
        .expect("awareness update");
    assert!(matches!(observer.recv().await, ConnectionEvent::Binary(_)));

    drop(owner);
    let ConnectionEvent::Binary(removal) = observer.recv().await else {
        panic!("expected awareness removal broadcast");
    };
    let messages = decode_messages(&removal);
    let removed = messages.into_iter().find_map(|message| match message {
        Message::Awareness(update) => update.clients.get(&client_id).cloned(),
        _ => None,
    });
    assert_eq!(removed.expect("removed client").json.as_ref(), "null");
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn remote_sequence_gap_reloads_the_committed_snapshot() {
    let store = Arc::new(MockStore::with_remote_snapshot().await);
    let registry = registry(store.clone(), 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut observer = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 1))
        .await
        .expect("observer");
    drain_initial(&mut observer).await;

    registry
        .apply_remote(document_id, 0, 2)
        .await
        .expect("remote catch-up");
    assert!(matches!(observer.recv().await, ConnectionEvent::Binary(_)));
    assert_eq!(store.load_count.load(Ordering::Acquire), 2);
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn permission_revision_closes_only_older_active_sessions() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store, 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut old = registry
        .connect(
            &request_context(),
            document_id,
            session_with_revision(Access::Viewer, 1, 1),
        )
        .await
        .expect("old connection");
    let mut same = registry
        .connect(
            &request_context(),
            document_id,
            session_with_revision(Access::Viewer, 2, 2),
        )
        .await
        .expect("same-revision connection");
    let mut newer = registry
        .connect(
            &request_context(),
            document_id,
            session_with_revision(Access::Viewer, 3, 3),
        )
        .await
        .expect("newer connection");
    drain_initial(&mut old).await;
    drain_initial(&mut same).await;
    drain_initial(&mut newer).await;

    registry
        .invalidate_permissions(document_id, 2)
        .await
        .expect("permission invalidation");
    registry
        .apply_remote(document_id, 0, 0)
        .await
        .expect("actor mailbox barrier");
    assert_eq!(old.recv().await, ConnectionEvent::Close(CLOSE_FORBIDDEN));
    assert_connection_open(&mut same).await;
    assert_connection_open(&mut newer).await;

    for stale_or_duplicate in [1, 2] {
        registry
            .invalidate_permissions(document_id, stale_or_duplicate)
            .await
            .expect("stale or duplicate permission event");
        registry
            .apply_remote(document_id, 0, 0)
            .await
            .expect("actor mailbox barrier");
    }
    assert_connection_open(&mut same).await;
    assert_connection_open(&mut newer).await;
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn permission_event_before_first_connect_rejects_older_ticket() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store, 8, Duration::from_millis(10));
    let document_id = DocumentId::new();
    registry
        .invalidate_permissions(document_id, 2)
        .await
        .expect("permission watermark");
    assert_eq!(registry.active_documents().await, 0);

    assert_eq!(
        registry
            .connect(
                &request_context(),
                document_id,
                session_with_revision(Access::Viewer, 1, 1),
            )
            .await
            .err(),
        Some(CLOSE_FORBIDDEN)
    );
    let mut current = registry
        .connect(
            &request_context(),
            document_id,
            session_with_revision(Access::Viewer, 2, 2),
        )
        .await
        .expect("current permission connection");
    drain_initial(&mut current).await;
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn permission_watermark_survives_actor_idle_reclamation() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store, 8, Duration::from_millis(10));
    let document_id = DocumentId::new();
    let mut old = registry
        .connect(
            &request_context(),
            document_id,
            session_with_revision(Access::Viewer, 1, 1),
        )
        .await
        .expect("old connection");
    drain_initial(&mut old).await;

    registry
        .invalidate_permissions(document_id, 2)
        .await
        .expect("permission invalidation");
    assert_eq!(old.recv().await, ConnectionEvent::Close(CLOSE_FORBIDDEN));
    wait_until_no_actors(&registry).await;

    assert_eq!(
        registry
            .connect(
                &request_context(),
                document_id,
                session_with_revision(Access::Viewer, 2, 1),
            )
            .await
            .err(),
        Some(CLOSE_FORBIDDEN)
    );
    assert_eq!(registry.active_documents().await, 0);
    let mut current = registry
        .connect(
            &request_context(),
            document_id,
            session_with_revision(Access::Viewer, 3, 2),
        )
        .await
        .expect("current permission connection");
    drain_initial(&mut current).await;
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn saturated_actor_queue_rejects_critical_invalidation() {
    let store = Arc::new(MockStore::new());
    store.block_next_append();
    let registry = registry_with_command_limits(
        store.clone(),
        1,
        Duration::from_millis(20),
        8,
        Duration::from_millis(20),
    );
    let document_id = DocumentId::new();
    let mut writer = registry
        .connect(&request_context(), document_id, session(Access::Editor, 1))
        .await
        .expect("writer");
    drain_initial(&mut writer).await;

    let frame = update_frame(append_paragraph_update(&store.first.state));
    let send = tokio::spawn(async move { writer.send(frame).await });
    store.wait_for_append().await;

    let remote = registry.apply_remote(document_id, 0, 0);
    tokio::pin!(remote);
    // The first poll fills the queue. Its response deadline is intentionally irrelevant here:
    // waiting for it after the equal invalidation timeout would make timer ordering observable.
    assert!(matches!(futures_util::poll!(&mut remote), Poll::Pending));

    assert!(
        registry
            .invalidate(document_id, CLOSE_DOCUMENT_INVALIDATED)
            .await
            .is_err()
    );

    store.release_append();
    let _ = send.await.expect("send task");
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

#[tokio::test]
async fn failed_remote_reload_terminates_stale_actor_before_reconnect() {
    let store = Arc::new(MockStore::new());
    let registry = registry(store.clone(), 8, Duration::from_millis(20));
    let document_id = DocumentId::new();
    let mut first = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 1))
        .await
        .expect("first connection");
    drain_initial(&mut first).await;

    assert!(registry.apply_remote(document_id, 0, 2).await.is_err());
    assert_eq!(
        first.recv().await,
        ConnectionEvent::Close(CLOSE_DEPENDENCY_UNAVAILABLE)
    );
    wait_until_no_actors(&registry).await;

    let mut reloaded = registry
        .connect(&request_context(), document_id, session(Access::Viewer, 2))
        .await
        .expect("reloaded connection");
    drain_initial(&mut reloaded).await;
    assert!(store.load_count.load(Ordering::Acquire) >= 3);
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("shutdown");
}

fn registry(
    store: Arc<MockStore>,
    outbound_capacity: usize,
    idle_timeout: Duration,
) -> ActorRegistry {
    registry_with_command_limits(
        store,
        8,
        Duration::from_secs(1),
        outbound_capacity,
        idle_timeout,
    )
}

fn registry_with_command_limits(
    store: Arc<MockStore>,
    command_capacity: usize,
    command_timeout: Duration,
    outbound_capacity: usize,
    idle_timeout: Duration,
) -> ActorRegistry {
    let actor = ActorConfig {
        command_capacity,
        outbound_capacity,
        idle_timeout,
        command_timeout,
        awareness_messages_per_second: 20,
        updates_per_second: 50,
    };
    let public = PublicConfig {
        address: "127.0.0.1:0".parse().expect("address"),
        allowed_origins: vec!["http://localhost:3000".to_owned()],
        max_frame_bytes: 1024 * 1024,
        max_update_bytes: 1024 * 1024,
        max_document_bytes: 4 * 1024 * 1024,
        max_awareness_bytes: 64 * 1024,
        max_connections: 32,
        max_connections_per_document: 8,
        handshakes_per_second: 100,
        handshake_burst: 100,
        handshake_timeout: Duration::from_secs(1),
    };
    let limits = ActorLimits::from_config(&actor, &public).expect("actor limits");
    ActorRegistry::new(
        store,
        limits,
        Metrics::new().expect("metrics"),
        CancellationToken::new(),
    )
}

fn session(access: Access, id: i64) -> ActorSession {
    session_with_revision(access, id, 1)
}

fn session_with_revision(access: Access, id: i64, permission_revision: i64) -> ActorSession {
    ActorSession {
        actor: PublicUser {
            id,
            username: format!("user-{id}"),
            avatar: String::new(),
        },
        access,
        permission_revision,
        expires_at: time::OffsetDateTime::now_utc() + time::Duration::hours(1),
    }
}

async fn assert_connection_open(
    connection: &mut knowledge_core_collaboration::actor::ActorConnection,
) {
    let receive = connection.recv();
    tokio::pin!(receive);
    assert!(matches!(futures_util::poll!(&mut receive), Poll::Pending));
}

fn manual_version(document_id: DocumentId, sequence: i64, state: Vec<u8>) -> DocumentVersion {
    DocumentVersion {
        id: VersionId::new(),
        document_id,
        sequence,
        kind: VersionKind::Manual,
        label: Some("Checkpoint".to_owned()),
        state,
        created_by: session(Access::Owner, 99).actor,
        created_at: time::OffsetDateTime::now_utc(),
    }
}

fn request_context() -> RequestContext {
    let mut context = RequestContext::new(uuid::Uuid::now_v7().simple().to_string());
    context.deadline = std::time::Instant::now().checked_add(Duration::from_secs(1));
    context
}

async fn drain_initial(connection: &mut knowledge_core_collaboration::actor::ActorConnection) {
    assert!(matches!(
        connection.recv().await,
        ConnectionEvent::Binary(_)
    ));
}

async fn wait_until_no_actors(registry: &ActorRegistry) {
    tokio::time::timeout(Duration::from_secs(1), async {
        while registry.active_documents().await != 0 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("actor removal");
}

fn append_paragraph_update(state: &[u8]) -> Vec<u8> {
    let document = richtext::document_from_state(state).expect("document");
    let before = document.transact().state_vector();
    let fragment = document.get_or_insert_xml_fragment(richtext::FRAGMENT_NAME);
    fragment.push_back(
        &mut document.transact_mut(),
        XmlElementPrelim::empty("paragraph"),
    );
    document.transact().encode_state_as_update_v1(&before)
}

fn update_frame(update: Vec<u8>) -> Vec<u8> {
    Message::Sync(SyncMessage::Update(update)).encode_v1()
}

fn decode_messages(payload: &[u8]) -> Vec<Message> {
    let mut decoder = DecoderV1::new(Cursor::new(payload));
    MessageReader::new(&mut decoder)
        .collect::<std::result::Result<Vec<_>, _>>()
        .expect("y-sync messages")
}
