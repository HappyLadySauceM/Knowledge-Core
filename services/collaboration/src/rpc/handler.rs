use std::sync::Arc;

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use pilota::FastStr;
use serde::{Deserialize, Serialize};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};
use volo_thrift::ServerError;

use crate::{
    actor::{ActorRegistry, CLOSE_DOCUMENT_INVALIDATED},
    config::TicketConfig,
    domain::{Authorization, DocumentId, DocumentVersion, RequestContext, VersionId},
    error::{Result, ServiceError},
    generated::{collaboration, common, knowledge},
    ports::KnowledgePort,
    richtext::projection_from_state,
    storage::{VersionCursor, VersionStore},
    ticket::TicketService,
};

use super::{RpcReadiness, current_request_context, knowledge::projection_to_wire, service_error};

const DEFAULT_VERSION_LIMIT: i32 = 20;
const MAXIMUM_VERSION_LIMIT: i32 = 100;
const MAXIMUM_CURSOR_LENGTH: usize = 1_024;

#[derive(Clone)]
pub struct CollaborationHandler {
    knowledge: Arc<dyn KnowledgePort>,
    tickets: TicketService,
    versions: Arc<dyn VersionStore>,
    actors: ActorRegistry,
    subprotocol: Arc<str>,
    fragment: Arc<str>,
    readiness: Arc<dyn RpcReadiness>,
}

impl CollaborationHandler {
    /// Creates the production Collaboration RPC handler.
    ///
    /// # Errors
    ///
    /// Returns an error when the public session contract is invalid.
    pub fn new(
        knowledge: Arc<dyn KnowledgePort>,
        tickets: TicketService,
        versions: Arc<dyn VersionStore>,
        actors: ActorRegistry,
        ticket: &TicketConfig,
        readiness: Arc<dyn RpcReadiness>,
    ) -> Result<Self> {
        if !valid_contract_token(&ticket.subprotocol) || !valid_contract_token(&ticket.fragment) {
            return Err(ServiceError::invalid_input(
                "Collaboration session contract is invalid",
            ));
        }
        Ok(Self {
            knowledge,
            tickets,
            versions,
            actors,
            subprotocol: Arc::from(ticket.subprotocol.as_str()),
            fragment: Arc::from(ticket.fragment.as_str()),
            readiness,
        })
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
            })
        }
        .await;
        result.map_err(|error| rpc_error(&error))
    }

    async fn list_versions(
        &self,
        request: collaboration::ListVersionsRequest,
    ) -> std::result::Result<collaboration::VersionPage, ServerError> {
        let result = async {
            self.require_ready().await?;
            let document_id = DocumentId::parse(request.document_id.as_str())?;
            let (context, _) = self.authorization(document_id, false).await?;
            let cursor = request.cursor.as_deref().map(decode_cursor).transpose()?;
            let limit = request.limit.unwrap_or(DEFAULT_VERSION_LIMIT);
            if !(1..=MAXIMUM_VERSION_LIMIT).contains(&limit) {
                return Err(ServiceError::invalid_input(
                    "limit must be between 1 and 100",
                ));
            }
            let page = self
                .versions
                .list_versions(&context, document_id, cursor.as_ref(), i64::from(limit))
                .await?;
            let next_cursor = if page.has_more {
                let last = page.items.last().ok_or_else(|| {
                    ServiceError::internal(anyhow::anyhow!(
                        "version store returned an empty partial page"
                    ))
                })?;
                Some(encode_cursor(last)?)
            } else {
                None
            };
            let items = page
                .items
                .iter()
                .map(|version| version_to_wire(version, document_id))
                .collect::<Result<Vec<_>>>()?;
            Ok(collaboration::VersionPage {
                items,
                page: collaboration::PageInfo {
                    next_cursor,
                    has_more: page.has_more,
                },
            })
        }
        .await;
        result.map_err(|error| rpc_error(&error))
    }

    async fn create_version(
        &self,
        request: collaboration::CreateVersionRequest,
    ) -> std::result::Result<collaboration::Version, ServerError> {
        let result = async {
            self.require_ready().await?;
            let document_id = DocumentId::parse(request.document_id.as_str())?;
            let label = request.label.as_deref().map(validate_label).transpose()?;
            let idempotency_key = request
                .idempotency_key
                .as_deref()
                .map(validate_idempotency_key)
                .transpose()?;
            let (context, authorization) = self.authorization(document_id, true).await?;
            let version = self
                .versions
                .create_manual_version(
                    &context,
                    document_id,
                    &authorization.actor,
                    label.as_deref(),
                    idempotency_key,
                )
                .await?;
            version_to_wire(&version, document_id)
        }
        .await;
        result.map_err(|error| rpc_error(&error))
    }

    async fn get_version(
        &self,
        request: collaboration::GetVersionRequest,
    ) -> std::result::Result<collaboration::VersionDetail, ServerError> {
        let result = async {
            self.require_ready().await?;
            let document_id = DocumentId::parse(request.document_id.as_str())?;
            let version_id = VersionId::parse(request.version_id.as_str())?;
            let (context, _) = self.authorization(document_id, false).await?;
            let version = self
                .versions
                .get_version(&context, document_id, version_id)
                .await?;
            let projection = projection_from_state(&version.state).map_err(|error| {
                ServiceError::internal(
                    anyhow::Error::new(error).context("project stored collaboration version"),
                )
            })?;
            Ok(collaboration::VersionDetail {
                version: version_to_wire(&version, document_id)?,
                content: projection_to_wire(&projection)?,
                plain_text: FastStr::from_string(projection.plain_text),
            })
        }
        .await;
        result.map_err(|error| rpc_error(&error))
    }

    async fn restore_version(
        &self,
        request: collaboration::RestoreVersionRequest,
    ) -> std::result::Result<collaboration::Version, ServerError> {
        let result = async {
            self.require_ready().await?;
            let document_id = DocumentId::parse(request.document_id.as_str())?;
            let version_id = VersionId::parse(request.version_id.as_str())?;
            if request.expected_sequence < 0 {
                return Err(ServiceError::invalid_input(
                    "expected_sequence must not be negative",
                ));
            }
            let idempotency_key = request
                .idempotency_key
                .as_deref()
                .map(validate_idempotency_key)
                .transpose()?;
            let (context, authorization) = self.authorization(document_id, true).await?;
            let target = self
                .versions
                .get_version(&context, document_id, version_id)
                .await?;
            let restored = self
                .actors
                .restore_version(
                    &context,
                    document_id,
                    target,
                    request.expected_sequence,
                    authorization.actor,
                    idempotency_key.map(ToOwned::to_owned),
                )
                .await;
            version_to_wire(&restored?, document_id)
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
            self.versions.purge_document(&context, document_id).await
        }
        .await;
        result.map_err(|error| rpc_error(&error))
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct CursorPayload {
    version: u8,
    created_at: String,
    id: String,
}

fn decode_cursor(value: &str) -> Result<VersionCursor> {
    if value.is_empty() || value.len() > MAXIMUM_CURSOR_LENGTH {
        return Err(ServiceError::invalid_input("cursor is invalid"));
    }
    let bytes = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| ServiceError::invalid_input("cursor is invalid"))?;
    if URL_SAFE_NO_PAD.encode(&bytes) != value {
        return Err(ServiceError::invalid_input("cursor is invalid"));
    }
    let payload: CursorPayload = serde_json::from_slice(&bytes)
        .map_err(|_| ServiceError::invalid_input("cursor is invalid"))?;
    if payload.version != 1 {
        return Err(ServiceError::invalid_input("cursor is invalid"));
    }
    let created_at = OffsetDateTime::parse(&payload.created_at, &Rfc3339)
        .map_err(|_| ServiceError::invalid_input("cursor is invalid"))?;
    let id = VersionId::parse(&payload.id)
        .map_err(|_| ServiceError::invalid_input("cursor is invalid"))?;
    Ok(VersionCursor { created_at, id })
}

fn encode_cursor(version: &DocumentVersion) -> Result<FastStr> {
    let payload = CursorPayload {
        version: 1,
        created_at: formatted_time(version.created_at)?,
        id: version.id.to_string(),
    };
    let bytes = serde_json::to_vec(&payload).map_err(|error| {
        ServiceError::internal(anyhow::Error::new(error).context("encode version cursor"))
    })?;
    Ok(FastStr::from_string(URL_SAFE_NO_PAD.encode(bytes)))
}

fn version_to_wire(
    version: &DocumentVersion,
    expected_document: DocumentId,
) -> Result<collaboration::Version> {
    if version.document_id != expected_document
        || version.sequence < 0
        || version
            .label
            .as_deref()
            .is_some_and(|label| !valid_label(label))
    {
        return Err(ServiceError::internal(anyhow::anyhow!(
            "version store returned an inconsistent version"
        )));
    }
    version.created_by.validate().map_err(|error| {
        ServiceError::internal(anyhow::Error::new(error).context("validate stored version actor"))
    })?;
    Ok(collaboration::Version {
        id: FastStr::from_string(version.id.to_string()),
        document_id: FastStr::from_string(version.document_id.to_string()),
        sequence: version.sequence,
        kind: FastStr::from_static_str(version.kind.as_str()),
        label: version.label.clone().map(FastStr::from_string),
        created_by: knowledge::PublicUser {
            id: version.created_by.id,
            username: FastStr::from_string(version.created_by.username.clone()),
            avatar: FastStr::from_string(version.created_by.avatar.clone()),
        },
        created_at: format_time(version.created_at)?,
    })
}

fn validate_label(value: &str) -> Result<String> {
    if !valid_label(value) {
        return Err(ServiceError::invalid_input(
            "label must contain between 1 and 200 characters",
        ));
    }
    Ok(value.to_owned())
}

fn valid_label(value: &str) -> bool {
    value.trim() == value
        && !value.chars().any(char::is_control)
        && (1..=200).contains(&value.chars().count())
}

fn validate_idempotency_key(value: &str) -> Result<&str> {
    if value.is_empty()
        || value.len() > 128
        || !value.bytes().all(|byte| (b'!'..=b'~').contains(&byte))
    {
        return Err(ServiceError::invalid_input(
            "idempotency_key must contain 1-128 visible ASCII characters",
        ));
    }
    Ok(value)
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

#[cfg(test)]
mod tests {
    use std::{
        sync::{
            Arc,
            atomic::{AtomicUsize, Ordering},
        },
        time::Duration,
    };

    use async_trait::async_trait;
    use bytes::Bytes;
    use pilota::thrift::Message;
    use time::OffsetDateTime;
    use tokio_util::sync::CancellationToken;
    use volo_thrift::ServerError;

    use super::{
        CollaborationHandler, decode_cursor, encode_cursor, validate_idempotency_key,
        validate_label, version_to_wire,
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
            }),
        )
        .await
        .expect("owner creates version");
        assert_eq!(created.document_id.as_str(), document_id.to_string());
        assert_eq!(owner_store.create_calls.load(Ordering::Relaxed), 1);
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
            documents,
            ActorLimits::for_test(),
            Metrics::new().expect("metrics"),
            CancellationToken::new(),
        );
        let handler = CollaborationHandler::new(
            knowledge_port,
            tickets,
            versions,
            actors,
            &ticket,
            readiness,
        )
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
        list_calls: AtomicUsize,
        create_calls: AtomicUsize,
        get_calls: AtomicUsize,
        restore_calls: AtomicUsize,
        purge_calls: AtomicUsize,
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
            Ok(LoadedDocument {
                generation: 1,
                sequence: self.version.sequence,
                state: self.version.state.clone(),
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
