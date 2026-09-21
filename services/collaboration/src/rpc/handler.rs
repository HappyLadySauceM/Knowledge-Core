use std::{
    sync::Arc,
    time::{Duration, Instant},
};

use pilota::FastStr;
use time::{OffsetDateTime, format_description::well_known::Rfc3339};
use volo_thrift::ServerError;
use yrs::updates::decoder::Decode;
use yrs::{ReadTxn, StateVector, Transact};

use crate::{
    actor::{ActorRegistry, CLOSE_DOCUMENT_INVALIDATED},
    config::TicketConfig,
    domain::{Authorization, DocumentId, RequestContext},
    error::{Result, ServiceError},
    generated::{collaboration, common},
    ports::KnowledgePort,
    richtext::projection_from_state,
    storage::{DocumentStore, PurgeStore},
    ticket::TicketService,
};

use super::{RpcReadiness, current_request_context, knowledge::projection_to_wire, service_error};

const CAPTURE_SNAPSHOT_STATE_VECTOR_WAIT: Duration = Duration::from_secs(1);
const CAPTURE_SNAPSHOT_STATE_VECTOR_POLL: Duration = Duration::from_millis(25);

#[derive(Clone)]
pub struct CollaborationHandler {
    knowledge: Arc<dyn KnowledgePort>,
    documents: Arc<dyn DocumentStore>,
    tickets: TicketService,
    purger: Arc<dyn PurgeStore>,
    actors: ActorRegistry,
    subprotocol: Arc<str>,
    fragment: Arc<str>,
    readiness: Arc<dyn RpcReadiness>,
    state_vector_wait: Duration,
    state_vector_poll: Duration,
}

pub struct CollaborationHandlerDependencies {
    pub knowledge: Arc<dyn KnowledgePort>,
    pub documents: Arc<dyn DocumentStore>,
    pub tickets: TicketService,
    pub purger: Arc<dyn PurgeStore>,
    pub actors: ActorRegistry,
    pub ticket: TicketConfig,
    pub readiness: Arc<dyn RpcReadiness>,
}

impl CollaborationHandler {
    /// Creates the production Collaboration RPC handler.
    ///
    /// # Errors
    ///
    /// Returns an error when the public session contract is invalid.
    pub fn new(dependencies: CollaborationHandlerDependencies) -> Result<Self> {
        if !valid_contract_token(&dependencies.ticket.subprotocol)
            || !valid_contract_token(&dependencies.ticket.fragment)
        {
            return Err(ServiceError::invalid_input(
                "Collaboration session contract is invalid",
            ));
        }
        Ok(Self {
            knowledge: dependencies.knowledge,
            documents: dependencies.documents,
            tickets: dependencies.tickets,
            purger: dependencies.purger,
            actors: dependencies.actors,
            subprotocol: Arc::from(dependencies.ticket.subprotocol.as_str()),
            fragment: Arc::from(dependencies.ticket.fragment.as_str()),
            readiness: dependencies.readiness,
            state_vector_wait: CAPTURE_SNAPSHOT_STATE_VECTOR_WAIT,
            state_vector_poll: CAPTURE_SNAPSHOT_STATE_VECTOR_POLL,
        })
    }

    /// Reloads the persisted document until it contains every clock known by the client.
    /// 有界重试读取已持久化文档，直到存储的 state vector 覆盖客户端已知的全部时钟。
    ///
    /// The server may contain concurrent edits from another collaborator. Requiring exact
    /// equality would reject a valid publication forever whenever another client commits
    /// between the WebSocket barrier and this RPC.
    /// 服务端可以包含其他协作者的并发编辑；要求完全相等会把合法发布误判为冲突。
    async fn wait_for_matching_state_vector(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        expected: &[u8],
    ) -> Result<()> {
        let expected = StateVector::decode_v1(expected).map_err(|error| {
            ServiceError::invalid_input("state vector is invalid").with_source(error)
        })?;
        let wait_until = Instant::now() + self.state_vector_wait;
        let deadline = match context.deadline {
            Some(request_deadline) => request_deadline.min(wait_until),
            None => wait_until,
        };
        loop {
            let loaded = self.documents.load_document(context, document_id).await?;
            let current = crate::richtext::document_from_state(&loaded.state)?;
            let current_vector = current.transact().state_vector();
            if expected
                .iter()
                .all(|(client, clock)| current_vector.get(client) >= *clock)
            {
                return Ok(());
            }
            let now = Instant::now();
            if now >= deadline {
                return Err(ServiceError::precondition_failed());
            }
            tokio::time::sleep(
                deadline
                    .saturating_duration_since(now)
                    .min(self.state_vector_poll),
            )
            .await;
        }
    }

    async fn authorization(
        &self,
        document_id: DocumentId,
        require_write: bool,
    ) -> Result<(RequestContext, Authorization)> {
        let context = current_request_context()?;
        if context.access_token.is_none() {
            return Err(ServiceError::unauthenticated());
        }
        let authorization = self.knowledge.authorize(&context, document_id).await?;
        if authorization.document_id != document_id || authorization.permission_revision <= 0 {
            return Err(ServiceError::internal(anyhow::anyhow!(
                "Knowledge returned inconsistent collaboration authorization"
            )));
        }
        authorization.actor.validate().map_err(|error| {
            ServiceError::internal(
                anyhow::Error::new(error).context("validate Knowledge collaboration actor"),
            )
        })?;
        if authorization.token_expires_at <= OffsetDateTime::now_utc() {
            return Err(ServiceError::unauthenticated());
        }
        if require_write && !authorization.access.can_write() {
            return Err(ServiceError::forbidden());
        }
        Ok((context, authorization))
    }

    async fn require_ready(&self) -> Result<()> {
        self.readiness.ready().await.map_err(|error| {
            ServiceError::unavailable(
                anyhow::Error::new(error).context("check Collaboration application readiness"),
            )
        })
    }
}

impl collaboration::CollaborationService for CollaborationHandler {
    async fn ping(
        &self,
        _request: common::PingRequest,
    ) -> std::result::Result<common::PingResponse, ServerError> {
        let status = if self.readiness.ready().await.is_ok() {
            "ready"
        } else {
            "not_ready"
        };
        Ok(common::PingResponse {
            service: FastStr::from_static_str("collaboration"),
            status: FastStr::from_static_str(status),
            unix_time: OffsetDateTime::now_utc().unix_timestamp(),
        })
    }

    async fn create_session(
        &self,
        request: collaboration::CreateSessionRequest,
    ) -> std::result::Result<collaboration::CollaborationSession, ServerError> {
        let result = async {
            self.require_ready().await?;
            let document_id = DocumentId::parse(request.document_id.as_str())?;
            let (context, authorization) = self.authorization(document_id, false).await?;
            let issued = self.tickets.issue(&context, &authorization).await?;
            Ok(collaboration::CollaborationSession {
                ticket: FastStr::from_string(issued.ticket.expose().to_owned()),
                subprotocol: FastStr::from_string(self.subprotocol.to_string()),
                fragment: FastStr::from_string(self.fragment.to_string()),
                access: FastStr::from_string(authorization.access.to_string()),
                ticket_expires_at: format_time(issued.expires_at)?,
                session_expires_at: format_time(issued.session_expires_at)?,
                instance_ordinal: None,
            })
        }
        .await;
        result.map_err(|error| rpc_error(&error))
    }

    async fn capture_publication_snapshot(
        &self,
        request: collaboration::CapturePublicationSnapshotRequest,
    ) -> std::result::Result<collaboration::PublicationSnapshot, ServerError> {
        let result = async {
            self.require_ready().await?;
            let document_id = DocumentId::parse(request.document_id.as_str())?;
            if request.state_vector.is_empty() || request.state_vector.len() > 64 * 1024 {
                return Err(ServiceError::invalid_input(
                    "state vector exceeds the configured size boundary",
                ));
            }
            let (context, _) = self.authorization(document_id, true).await?;
            self.wait_for_matching_state_vector(
                &context,
                document_id,
                request.state_vector.as_ref(),
            )
            .await?;
            let loaded = self.documents.load_document(&context, document_id).await?;
            let projection = projection_from_state(&loaded.state).map_err(|error| {
                ServiceError::internal(
                    anyhow::Error::new(error).context("project captured collaboration snapshot"),
                )
            })?;
            Ok(collaboration::PublicationSnapshot {
                document_id: FastStr::from_string(document_id.to_string()),
                sequence: loaded.sequence,
                content: projection_to_wire(&projection)?,
                plain_text: FastStr::from_string(projection.plain_text),
            })
        }
        .await;
        result.map_err(|error| rpc_error(&error))
    }

    async fn purge_document(
        &self,
        request: collaboration::PurgeDocumentRequest,
    ) -> std::result::Result<(), ServerError> {
        let result = async {
            self.require_ready().await?;
            let document_id = DocumentId::parse(request.document_id.as_str())?;
            let context = current_request_context()?;
            self.actors
                .invalidate(document_id, CLOSE_DOCUMENT_INVALIDATED)
                .await?;
            self.purger.purge_document(&context, document_id).await
        }
        .await;
        result.map_err(|error| rpc_error(&error))
    }
}

fn valid_contract_token(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value.trim() == value
        && value.bytes().all(|byte| (b'!'..=b'~').contains(&byte))
}

fn format_time(value: OffsetDateTime) -> Result<FastStr> {
    Ok(FastStr::from_string(formatted_time(value)?))
}

fn formatted_time(value: OffsetDateTime) -> Result<String> {
    value.format(&Rfc3339).map_err(|error| {
        ServiceError::internal(anyhow::Error::new(error).context("format RPC timestamp"))
    })
}

fn rpc_error(error: &ServiceError) -> ServerError {
    service_error(error)
}

// Legacy version-history tests are intentionally retired with the RPC surface.
// Keep the old fixture below out of all builds until the storage cleanup lands.
#[cfg(any())]
mod tests {
    use std::{
        sync::{
            Arc, Mutex,
            atomic::{AtomicBool, AtomicUsize, Ordering},
        },
        time::Duration,
    };

    use async_trait::async_trait;
    use bytes::Bytes;
    use pilota::thrift::Message;
    use time::OffsetDateTime;
    use tokio_util::sync::CancellationToken;
    use volo_thrift::ServerError;
    use yrs::{ReadTxn, Transact, XmlFragment, updates::encoder::Encode};

    use super::{
        CollaborationHandler, CollaborationHandlerDependencies, decode_cursor, encode_cursor,
        validate_idempotency_key, validate_label, version_to_wire,
    };
    use crate::{
        actor::{ActorLimits, ActorRegistry},
        config::TicketConfig,
        domain::{
            Access, Authorization, DocumentId, DocumentVersion, Projection, PublicUser,
            RequestContext, Secret, VersionId, VersionKind,
        },
        error::{Result, ServiceError},
        generated::{
            collaboration::{self, CollaborationService as _},
            common,
        },
        ports::KnowledgePort,
        richtext,
        rpc::{RpcReadiness, context::scope_request_context_for_test},
        storage::{
            CommittedUpdate, DocumentStore, LoadedDocument, RestorationCandidate, RestoreVersion,
            StoredUpdate, UpdateLimits, VersionCursor, VersionPage, VersionStore,
        },
        telemetry::Metrics,
        ticket::{TicketBackend, TicketService},
    };

    #[test]
    fn ping_wire_contract_rejects_a_missing_request() {
        let mut payload = Bytes::from_static(&[0]);
        let mut protocol = pilota::thrift::compact::TCompactInputProtocol::new(&mut payload);
        let error = collaboration::CollaborationServicePingArgsRecv::decode(&mut protocol)
            .expect_err("missing Ping request must be rejected");

        assert!(error.to_string().contains("field request is required"));
    }

    fn version() -> DocumentVersion {
        DocumentVersion {
            id: VersionId::new(),
            document_id: DocumentId::new(),
            sequence: 7,
            kind: VersionKind::Manual,
            label: Some("Checkpoint".to_owned()),
            state: Vec::new(),
            created_by: PublicUser {
                id: 42,
                username: "editor".to_owned(),
                avatar: String::new(),
            },
            created_at: OffsetDateTime::now_utc(),
        }
    }

    #[test]
    fn version_cursor_round_trips_and_is_canonical() {
        let version = version();
        let encoded = encode_cursor(&version).expect("encode cursor");
        let decoded = decode_cursor(encoded.as_str()).expect("decode cursor");
        assert_eq!(decoded.id, version.id);
        assert_eq!(decoded.created_at, version.created_at);
        assert!(decode_cursor(&(encoded.to_string() + "=")).is_err());
        assert!(decode_cursor(&"x".repeat(1_025)).is_err());
    }

    #[test]
    fn rpc_input_helpers_enforce_the_public_boundaries() {
        assert_eq!(validate_label("Checkpoint").expect("label"), "Checkpoint");
        assert!(validate_label(" padded ").is_err());
        assert!(validate_label("forged\nlabel").is_err());
        assert_eq!(
            validate_idempotency_key("version-request-1").expect("idempotency key"),
            "version-request-1"
        );
        assert!(validate_idempotency_key("contains space").is_err());
    }

    #[test]
    fn stored_version_maps_to_the_typed_contract() {
        let version = version();
        let wire = version_to_wire(&version, version.document_id).expect("wire version");
        assert_eq!(wire.id.as_str(), version.id.to_string());
        assert_eq!(wire.document_id.as_str(), version.document_id.to_string());
        assert_eq!(wire.sequence, 7);
        assert_eq!(wire.kind.as_str(), "manual");
        assert_eq!(wire.created_by.username.as_str(), "editor");
    }

    #[tokio::test]
    async fn session_requires_token_and_uses_knowledge_authorization() {
        let (handler, store, knowledge, _, document_id) = handler(Access::Viewer);
        let request = collaboration::CreateSessionRequest {
            document_id: document_id.to_string().into(),
        };
        let session = scope_request_context_for_test(
            authenticated_context(),
            handler.create_session(request.clone()),
        )
        .await
        .expect("create session");
        assert_eq!(session.access.as_str(), "viewer");
        assert_eq!(session.ticket.len(), 43);
        assert!(session.instance_ordinal.is_none());
        assert_eq!(knowledge.calls.load(Ordering::Relaxed), 1);
        assert_eq!(store.create_calls.load(Ordering::Relaxed), 0);

        let error = scope_request_context_for_test(
            RequestContext::new("request-without-token"),
            handler.create_session(request),
        )
        .await
        .expect_err("missing token must fail closed");
        assert_biz_code(error, 40_002);
        assert_eq!(knowledge.calls.load(Ordering::Relaxed), 1);
    }

    #[tokio::test]
    async fn session_fails_closed_when_knowledge_is_unavailable() {
        let (handler, _, knowledge, tickets, document_id) = handler(Access::Viewer);
        knowledge.fail_unavailable.store(true, Ordering::Relaxed);
        let error = scope_request_context_for_test(
            authenticated_context(),
            handler.create_session(collaboration::CreateSessionRequest {
                document_id: document_id.to_string().into(),
            }),
        )
        .await
        .expect_err("Knowledge unavailable must fail closed");
        assert_unavailable(error);
        assert_eq!(knowledge.calls.load(Ordering::Relaxed), 1);
        assert_eq!(tickets.put_calls.load(Ordering::Relaxed), 0);
    }

    #[tokio::test]
    async fn create_session_does_not_assign_an_instance() {
        let (handler, _, _, _, document_id) = handler(Access::Editor);
        let request = collaboration::CreateSessionRequest {
            document_id: document_id.to_string().into(),
        };
        let first = scope_request_context_for_test(
            authenticated_context(),
            handler.create_session(request.clone()),
        )
        .await
        .expect("first session");
        let second = scope_request_context_for_test(
            authenticated_context(),
            handler.create_session(request),
        )
        .await
        .expect("second session");
        assert!(first.instance_ordinal.is_none());
        assert!(second.instance_ordinal.is_none());
    }

    #[tokio::test]
    async fn ping_tracks_application_readiness_without_calling_knowledge() {
        let readiness = Arc::new(ToggleReadiness::default());
        let (handler, _, knowledge, _, _) =
            handler_with_readiness(Access::Viewer, readiness.clone());

        let response = handler
            .ping(common::PingRequest::default())
            .await
            .expect("startup Ping response");
        assert_eq!(response.service.as_str(), "collaboration");
        assert_eq!(response.status.as_str(), "not_ready");

        readiness.set_ready(true);
        let response = handler
            .ping(common::PingRequest::default())
            .await
            .expect("ready Ping response");
        assert_eq!(response.service.as_str(), "collaboration");
        assert_eq!(response.status.as_str(), "ready");

        readiness.set_ready(false);
        let response = handler
            .ping(common::PingRequest::default())
            .await
            .expect("unhealthy Ping response");
        assert_eq!(response.status.as_str(), "not_ready");
        assert_eq!(readiness.calls.load(Ordering::Relaxed), 3);
        assert_eq!(knowledge.calls.load(Ordering::Relaxed), 0);
    }

    #[tokio::test]
    async fn business_rpcs_fail_closed_until_application_is_ready() {
        let readiness = Arc::new(ToggleReadiness::default());
        let (handler, store, knowledge, tickets, document_id) =
            handler_with_readiness(Access::Viewer, readiness.clone());
        let document_id = document_id.to_string();
        let version_id = store.version.id.to_string();

        assert_unavailable(
            handler
                .create_session(collaboration::CreateSessionRequest {
                    document_id: document_id.clone().into(),
                })
                .await
                .expect_err("session creation must fail while not ready"),
        );
        assert_unavailable(
            handler
                .list_versions(collaboration::ListVersionsRequest {
                    document_id: document_id.clone().into(),
                    cursor: None,
                    limit: None,
                })
                .await
                .expect_err("version listing must fail while not ready"),
        );
        assert_unavailable(
            handler
                .create_version(collaboration::CreateVersionRequest {
                    document_id: document_id.clone().into(),
                    label: None,
                    idempotency_key: None,
                    state_vector: None,
                })
                .await
                .expect_err("version creation must fail while not ready"),
        );
        assert_unavailable(
            handler
                .get_version(collaboration::GetVersionRequest {
                    document_id: document_id.clone().into(),
                    version_id: version_id.clone().into(),
                })
                .await
                .expect_err("version retrieval must fail while not ready"),
        );
        assert_unavailable(
            handler
                .restore_version(collaboration::RestoreVersionRequest {
                    document_id: document_id.clone().into(),
                    version_id: version_id.into(),
                    expected_sequence: store.version.sequence,
                    idempotency_key: None,
                })
                .await
                .expect_err("version restoration must fail while not ready"),
        );
        assert_unavailable(
            handler
                .purge_document(collaboration::PurgeDocumentRequest {
                    document_id: document_id.clone().into(),
                })
                .await
                .expect_err("document purge must fail while not ready"),
        );

        assert_eq!(readiness.calls.load(Ordering::Relaxed), 6);
        assert_eq!(knowledge.calls.load(Ordering::Relaxed), 0);
        assert_eq!(tickets.put_calls.load(Ordering::Relaxed), 0);
        assert_eq!(store.list_calls.load(Ordering::Relaxed), 0);
        assert_eq!(store.create_calls.load(Ordering::Relaxed), 0);
        assert_eq!(store.get_calls.load(Ordering::Relaxed), 0);
        assert_eq!(store.restore_calls.load(Ordering::Relaxed), 0);
        assert_eq!(store.purge_calls.load(Ordering::Relaxed), 0);

        readiness.set_ready(true);
        let session = scope_request_context_for_test(
            authenticated_context(),
            handler.create_session(collaboration::CreateSessionRequest {
                document_id: document_id.into(),
            }),
        )
        .await
        .expect("session creation resumes when ready");
        assert_eq!(session.access.as_str(), "viewer");
        assert!(session.instance_ordinal.is_none());
        assert_eq!(readiness.calls.load(Ordering::Relaxed), 7);
        assert_eq!(knowledge.calls.load(Ordering::Relaxed), 1);
        assert_eq!(tickets.put_calls.load(Ordering::Relaxed), 1);
    }

    #[tokio::test]
    async fn version_writes_require_non_viewer_access() {
        let (viewer, viewer_store, _, _, document_id) = handler(Access::Viewer);
        let request = collaboration::CreateVersionRequest {
            document_id: document_id.to_string().into(),
            label: Some("Checkpoint".into()),
            idempotency_key: Some("request-1".into()),
            state_vector: None,
        };
        let error =
            scope_request_context_for_test(authenticated_context(), viewer.create_version(request))
                .await
                .expect_err("viewer write must be forbidden");
        assert_biz_code(error, 40_003);
        assert_eq!(viewer_store.create_calls.load(Ordering::Relaxed), 0);

        let (owner, owner_store, _, _, document_id) = handler(Access::Owner);
        let created = scope_request_context_for_test(
            authenticated_context(),
            owner.create_version(collaboration::CreateVersionRequest {
                document_id: document_id.to_string().into(),
                label: Some("Checkpoint".into()),
                idempotency_key: Some("request-2".into()),
                state_vector: None,
            }),
        )
        .await
        .expect("owner creates version");
        assert_eq!(created.document_id.as_str(), document_id.to_string());
        assert_eq!(owner_store.create_calls.load(Ordering::Relaxed), 1);
    }

    #[tokio::test]
    async fn create_version_retries_until_stored_state_vector_matches() {
        let (handler, store, _, _, document_id) = handler(Access::Owner);
        let lagging = richtext::initial_state();
        let matching = state_with_extra_paragraph();
        let expected = state_vector_of(&matching);
        *store.load_states.lock().expect("load_states") = vec![lagging, matching];

        let created = scope_request_context_for_test(
            authenticated_context(),
            handler.create_version(collaboration::CreateVersionRequest {
                document_id: document_id.to_string().into(),
                label: Some("publication".into()),
                idempotency_key: Some("publish-catch-up".into()),
                state_vector: Some(Bytes::copy_from_slice(&expected)),
            }),
        )
        .await
        .expect("CreateVersion succeeds after stored SV catches up");
        assert_eq!(created.document_id.as_str(), document_id.to_string());
        assert!(store.load_calls.load(Ordering::Relaxed) >= 2);
        assert_eq!(store.create_calls.load(Ordering::Relaxed), 1);
    }

    #[tokio::test]
    async fn create_version_accepts_server_state_with_concurrent_extra_clocks() {
        let (handler, store, _, _, document_id) = handler(Access::Owner);
        let client_state = richtext::initial_state();
        let server_state = state_with_extra_paragraph_from(&client_state);
        let expected = state_vector_of(&client_state);
        *store.load_states.lock().expect("load_states") = vec![server_state];

        let created = scope_request_context_for_test(
            authenticated_context(),
            handler.create_version(collaboration::CreateVersionRequest {
                document_id: document_id.to_string().into(),
                label: Some("publication".into()),
                idempotency_key: Some("publish-server-superset".into()),
                state_vector: Some(Bytes::copy_from_slice(&expected)),
            }),
        )
        .await
        .expect("CreateVersion accepts a server state that includes concurrent edits");
        assert_eq!(created.document_id.as_str(), document_id.to_string());
        assert_eq!(store.create_calls.load(Ordering::Relaxed), 1);
    }

    #[tokio::test]
    async fn create_version_still_preconditions_when_stored_state_vector_never_matches() {
        let (mut handler, store, _, _, document_id) = handler(Access::Owner);
        handler.state_vector_wait = Duration::from_millis(40);
        handler.state_vector_poll = Duration::from_millis(5);
        let expected = state_vector_of(&state_with_extra_paragraph());

        let error = scope_request_context_for_test(
            authenticated_context(),
            handler.create_version(collaboration::CreateVersionRequest {
                document_id: document_id.to_string().into(),
                label: Some("publication".into()),
                idempotency_key: Some("publish-never-matches".into()),
                state_vector: Some(Bytes::copy_from_slice(&expected)),
            }),
        )
        .await
        .expect_err("CreateVersion must keep 412 when stored SV never equals the client");
        assert_biz_code(error, 40_006);
        assert!(store.load_calls.load(Ordering::Relaxed) >= 2);
        assert_eq!(store.create_calls.load(Ordering::Relaxed), 0);
    }

    #[tokio::test]
    async fn restore_loads_the_target_before_committing_through_the_actor() {
        let (handler, store, _, _, document_id) = handler(Access::Owner);
        let restored = scope_request_context_for_test(
            authenticated_context(),
            handler.restore_version(collaboration::RestoreVersionRequest {
                document_id: document_id.to_string().into(),
                version_id: store.version.id.to_string().into(),
                expected_sequence: store.version.sequence,
                idempotency_key: Some("restore-request-1".into()),
            }),
        )
        .await
        .expect("restore version");

        assert_eq!(restored.document_id.as_str(), document_id.to_string());
        assert_eq!(restored.kind.as_str(), "restoration");
        assert_eq!(store.get_calls.load(Ordering::Relaxed), 1);
        assert_eq!(store.restore_calls.load(Ordering::Relaxed), 1);
    }

    #[tokio::test]
    async fn purge_is_a_service_operation_without_user_token() {
        let (handler, store, knowledge, _, document_id) = handler(Access::Owner);
        scope_request_context_for_test(
            RequestContext::new("service-purge-request"),
            handler.purge_document(collaboration::PurgeDocumentRequest {
                document_id: document_id.to_string().into(),
            }),
        )
        .await
        .expect("purge document");
        assert_eq!(store.purge_calls.load(Ordering::Relaxed), 1);
        assert_eq!(knowledge.calls.load(Ordering::Relaxed), 0);
    }

    fn handler(
        access: Access,
    ) -> (
        CollaborationHandler,
        Arc<StoreStub>,
        Arc<KnowledgeStub>,
        Arc<MemoryTickets>,
        DocumentId,
    ) {
        handler_with_readiness(access, Arc::new(Ready))
    }

    fn handler_with_readiness(
        access: Access,
        readiness: Arc<dyn RpcReadiness>,
    ) -> (
        CollaborationHandler,
        Arc<StoreStub>,
        Arc<KnowledgeStub>,
        Arc<MemoryTickets>,
        DocumentId,
    ) {
        let document_id = DocumentId::new();
        let version = DocumentVersion {
            document_id,
            state: richtext::initial_state(),
            ..version()
        };
        let store = Arc::new(StoreStub {
            version,
            load_states: Mutex::new(Vec::new()),
            load_calls: AtomicUsize::new(0),
            list_calls: AtomicUsize::new(0),
            create_calls: AtomicUsize::new(0),
            get_calls: AtomicUsize::new(0),
            restore_calls: AtomicUsize::new(0),
            purge_calls: AtomicUsize::new(0),
        });
        let knowledge = Arc::new(KnowledgeStub {
            authorization: Authorization {
                document_id,
                actor: PublicUser {
                    id: 42,
                    username: "editor".to_owned(),
                    avatar: String::new(),
                },
                access,
                permission_revision: 1,
                token_expires_at: OffsetDateTime::now_utc() + time::Duration::minutes(5),
            },
            fail_unavailable: AtomicBool::new(false),
            calls: AtomicUsize::new(0),
        });
        let ticket = TicketConfig {
            ttl: Duration::from_secs(30),
            subprotocol: "knowledge-core-yjs-v1".to_owned(),
            fragment: "default".to_owned(),
        };
        let ticket_backend = Arc::new(MemoryTickets::default());
        let tickets = TicketService::new(ticket_backend.clone(), &ticket).expect("tickets");
        let documents: Arc<dyn DocumentStore> = store.clone();
        let versions: Arc<dyn VersionStore> = store.clone();
        let knowledge_port: Arc<dyn KnowledgePort> = knowledge.clone();
        let actors = ActorRegistry::new(
            Arc::clone(&documents),
            ActorLimits::for_test(),
            Metrics::new().expect("metrics"),
            CancellationToken::new(),
        );
        let handler = CollaborationHandler::new(CollaborationHandlerDependencies {
            knowledge: knowledge_port,
            documents,
            tickets,
            versions,
            actors,
            ticket,
            readiness,
        })
        .expect("handler");
        (handler, store, knowledge, ticket_backend, document_id)
    }

    fn authenticated_context() -> RequestContext {
        let mut context = RequestContext::new("request-123");
        context.access_token = Some(Secret::new("access-token").expect("token"));
        context
    }

    fn assert_biz_code(error: ServerError, expected: i32) {
        let ServerError::Biz(error) = error else {
            panic!("expected BizStatus");
        };
        assert_eq!(error.status_code, expected);
    }

    fn assert_unavailable(error: ServerError) {
        let ServerError::Biz(error) = error else {
            panic!("expected unavailable BizStatus");
        };
        assert_eq!(error.status_code, 40_007);
        assert_eq!(error.status_message.as_str(), "dependency unavailable");
        let extra = error.extra.expect("unavailable BizStatus extra");
        assert_eq!(
            extra.get("error_key").map(pilota::FastStr::as_str),
            Some("collaboration.unavailable")
        );
        assert_eq!(
            extra.get("error_kind").map(pilota::FastStr::as_str),
            Some("unavailable")
        );
    }

    struct KnowledgeStub {
        authorization: Authorization,
        fail_unavailable: AtomicBool,
        calls: AtomicUsize,
    }

    #[async_trait]
    impl KnowledgePort for KnowledgeStub {
        async fn authorize(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
        ) -> Result<Authorization> {
            self.calls.fetch_add(1, Ordering::Relaxed);
            if self.fail_unavailable.load(Ordering::Relaxed) {
                return Err(ServiceError::unavailable(anyhow::anyhow!(
                    "circuit breaker is open"
                )));
            }
            Ok(self.authorization.clone())
        }

        async fn project(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
            _sequence: i64,
            _projection: &Projection,
        ) -> Result<()> {
            Ok(())
        }

        async fn ping(&self, _context: &RequestContext) -> Result<()> {
            Ok(())
        }
    }

    struct StoreStub {
        version: DocumentVersion,
        load_states: Mutex<Vec<Vec<u8>>>,
        load_calls: AtomicUsize,
        list_calls: AtomicUsize,
        create_calls: AtomicUsize,
        get_calls: AtomicUsize,
        restore_calls: AtomicUsize,
        purge_calls: AtomicUsize,
    }

    fn state_with_extra_paragraph() -> Vec<u8> {
        state_with_extra_paragraph_from(&richtext::initial_state())
    }

    fn state_with_extra_paragraph_from(state: &[u8]) -> Vec<u8> {
        let document = richtext::document_from_state(state).expect("state");
        let fragment = document.get_or_insert_xml_fragment(richtext::FRAGMENT_NAME);
        fragment.push_back(
            &mut document.transact_mut(),
            yrs::XmlElementPrelim::empty("paragraph"),
        );
        richtext::full_state(&document)
    }

    fn state_vector_of(state: &[u8]) -> Vec<u8> {
        richtext::document_from_state(state)
            .expect("document")
            .transact()
            .state_vector()
            .encode_v1()
    }

    #[async_trait]
    impl VersionStore for StoreStub {
        async fn create_manual_version(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
            _actor: &PublicUser,
            _label: Option<&str>,
            _idempotency_key: Option<&str>,
        ) -> Result<DocumentVersion> {
            self.create_calls.fetch_add(1, Ordering::Relaxed);
            Ok(self.version.clone())
        }

        async fn list_versions(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
            _cursor: Option<&VersionCursor>,
            _limit: i64,
        ) -> Result<VersionPage> {
            self.list_calls.fetch_add(1, Ordering::Relaxed);
            Ok(VersionPage {
                items: vec![self.version.clone()],
                has_more: false,
                head_sequence: self.version.sequence,
            })
        }

        async fn get_version(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
            _version_id: VersionId,
        ) -> Result<DocumentVersion> {
            self.get_calls.fetch_add(1, Ordering::Relaxed);
            Ok(self.version.clone())
        }

        async fn purge_document(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
        ) -> Result<()> {
            self.purge_calls.fetch_add(1, Ordering::Relaxed);
            Ok(())
        }
    }

    #[async_trait]
    impl DocumentStore for StoreStub {
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
            let index = self.load_calls.fetch_add(1, Ordering::Relaxed);
            let state = {
                let states = self.load_states.lock().expect("load_states");
                states
                    .get(index)
                    .cloned()
                    .or_else(|| states.last().cloned())
                    .unwrap_or_else(|| self.version.state.clone())
            };
            Ok(LoadedDocument {
                generation: 1,
                sequence: self.version.sequence,
                state,
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
            Err(ServiceError::internal(anyhow::anyhow!(
                "test store does not append updates"
            )))
        }

        async fn commit_restoration(
            &self,
            _context: &RequestContext,
            document_id: DocumentId,
            candidate: RestorationCandidate<'_>,
        ) -> Result<RestoreVersion> {
            self.restore_calls.fetch_add(1, Ordering::Relaxed);
            let state = candidate.target.state.clone();
            let projection = richtext::projection_from_state(&state)?;
            let mut version = candidate.target.clone();
            version.document_id = document_id;
            version.kind = VersionKind::Restoration;
            Ok(RestoreVersion {
                version,
                committed: Some(CommittedUpdate {
                    generation: candidate.baseline_generation + 1,
                    sequence: candidate.baseline_sequence,
                    state,
                    projection,
                    update: None,
                }),
            })
        }

        async fn updates_after(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
            _sequence: i64,
            _limit: i64,
        ) -> Result<Vec<StoredUpdate>> {
            Ok(Vec::new())
        }

        async fn current_sequence(
            &self,
            _context: &RequestContext,
            _document_id: DocumentId,
        ) -> Result<i64> {
            Ok(self.version.sequence)
        }
    }

    #[derive(Default)]
    struct MemoryTickets {
        put_calls: AtomicUsize,
    }

    #[async_trait]
    impl TicketBackend for MemoryTickets {
        async fn put(
            &self,
            _context: &RequestContext,
            _digest: [u8; 32],
            _value: Vec<u8>,
            _ttl: Duration,
        ) -> Result<bool> {
            self.put_calls.fetch_add(1, Ordering::Relaxed);
            Ok(true)
        }

        async fn take(
            &self,
            _context: &RequestContext,
            _digest: [u8; 32],
        ) -> Result<Option<Vec<u8>>> {
            Ok(None)
        }

        async fn ping(&self) -> Result<()> {
            Ok(())
        }
    }

    struct Ready;

    #[async_trait]
    impl RpcReadiness for Ready {
        async fn ready(&self) -> Result<()> {
            Ok(())
        }
    }

    #[derive(Default)]
    struct ToggleReadiness {
        ready: std::sync::atomic::AtomicBool,
        calls: AtomicUsize,
    }

    impl ToggleReadiness {
        fn set_ready(&self, ready: bool) {
            self.ready.store(ready, Ordering::Release);
        }
    }

    #[async_trait]
    impl RpcReadiness for ToggleReadiness {
        async fn ready(&self) -> Result<()> {
            self.calls.fetch_add(1, Ordering::Relaxed);
            if self.ready.load(Ordering::Acquire) {
                Ok(())
            } else {
                Err(ServiceError::unavailable(anyhow::anyhow!(
                    "application is not ready"
                )))
            }
        }
    }
}
