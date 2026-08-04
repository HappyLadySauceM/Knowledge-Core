use anyhow::anyhow;
use motore::{layer::Layer, service::Service};
use pilota::{AHashMap, FastStr};
use volo_thrift::context::ServerContext;
use volo_thrift::{BizError, ClientError, ServerError};

use crate::{
    domain::RequestContext,
    error::{ErrorCode, ServiceError},
    generated::collaboration::{
        CollaborationServiceCreateSessionResultSend, CollaborationServiceCreateVersionResultSend,
        CollaborationServiceGetVersionResultSend, CollaborationServiceListVersionsResultSend,
        CollaborationServicePingResultSend, CollaborationServicePurgeDocumentResultSend,
        CollaborationServiceRequestRecv, CollaborationServiceResponseSend,
        CollaborationServiceRestoreVersionResultSend,
    },
};

const EXTRA_ERROR_KEY: &str = "error_key";
const EXTRA_ERROR_KIND: &str = "error_kind";
const EXTRA_REQUEST_ID: &str = "request_id";
const EXTRA_TRACE_ID: &str = "trace_id";

/// Keeps Volo's `TTHeader` `BizStatus` while emitting the normal Thrift reply Kitex expects.
#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct KitexBizCompatibilityLayer;

impl<S> Layer<S> for KitexBizCompatibilityLayer {
    type Service = KitexBizCompatibilityService<S>;

    fn layer(self, inner: S) -> Self::Service {
        KitexBizCompatibilityService { inner }
    }
}

#[derive(Clone)]
pub(crate) struct KitexBizCompatibilityService<S> {
    inner: S,
}

impl<S> Service<ServerContext, CollaborationServiceRequestRecv> for KitexBizCompatibilityService<S>
where
    S: Service<
            ServerContext,
            CollaborationServiceRequestRecv,
            Response = CollaborationServiceResponseSend,
            Error = ServerError,
        > + Send
        + Sync
        + 'static,
{
    type Response = CollaborationServiceResponseSend;
    type Error = ServerError;

    async fn call(
        &self,
        context: &mut ServerContext,
        request: CollaborationServiceRequestRecv,
    ) -> std::result::Result<Self::Response, Self::Error> {
        let fallback = default_response(&request);
        match self.inner.call(context, request).await {
            Err(ServerError::Biz(_)) => Ok(fallback),
            result => result,
        }
    }
}

fn default_response(request: &CollaborationServiceRequestRecv) -> CollaborationServiceResponseSend {
    match request {
        CollaborationServiceRequestRecv::Ping(_) => {
            CollaborationServiceResponseSend::Ping(CollaborationServicePingResultSend::default())
        }
        CollaborationServiceRequestRecv::CreateSession(_) => {
            CollaborationServiceResponseSend::CreateSession(
                CollaborationServiceCreateSessionResultSend::default(),
            )
        }
        CollaborationServiceRequestRecv::ListVersions(_) => {
            CollaborationServiceResponseSend::ListVersions(
                CollaborationServiceListVersionsResultSend::default(),
            )
        }
        CollaborationServiceRequestRecv::CreateVersion(_) => {
            CollaborationServiceResponseSend::CreateVersion(
                CollaborationServiceCreateVersionResultSend::default(),
            )
        }
        CollaborationServiceRequestRecv::GetVersion(_) => {
            CollaborationServiceResponseSend::GetVersion(
                CollaborationServiceGetVersionResultSend::default(),
            )
        }
        CollaborationServiceRequestRecv::RestoreVersion(_) => {
            CollaborationServiceResponseSend::RestoreVersion(
                CollaborationServiceRestoreVersionResultSend::default(),
            )
        }
        CollaborationServiceRequestRecv::PurgeDocument(_) => {
            CollaborationServiceResponseSend::PurgeDocument(
                CollaborationServicePurgeDocumentResultSend::default(),
            )
        }
    }
}

pub fn service_error(error: &ServiceError) -> ServerError {
    service_error_with_context(
        error,
        super::context::try_current_request_context().as_ref(),
        None,
    )
}

pub(crate) fn deadline_error(context: &RequestContext) -> ServerError {
    let error = ServiceError::new(
        ErrorCode::Unavailable,
        "collaboration.deadline_exceeded",
        "request deadline exceeded",
    );
    service_error_with_context(&error, Some(context), Some("deadline_exceeded"))
}

pub(crate) fn service_error_with_context(
    error: &ServiceError,
    context: Option<&RequestContext>,
    kind: Option<&'static str>,
) -> ServerError {
    let mut extra = AHashMap::with_capacity(4);
    extra.insert(
        FastStr::from_static_str(EXTRA_ERROR_KEY),
        FastStr::from_static_str(error.key()),
    );
    extra.insert(
        FastStr::from_static_str(EXTRA_ERROR_KIND),
        FastStr::from_static_str(kind.unwrap_or_else(|| error_kind(error.code()))),
    );
    if let Some(context) = context
        && !context.request_id.is_empty()
    {
        extra.insert(
            FastStr::from_static_str(EXTRA_REQUEST_ID),
            FastStr::from_string(context.request_id.to_string()),
        );
    }
    if let Some(trace_id) = super::context::current_trace_id() {
        extra.insert(
            FastStr::from_static_str(EXTRA_TRACE_ID),
            FastStr::from_string(trace_id),
        );
    }

    BizError::with_extra(
        error.numeric_code(),
        FastStr::from_string(error.detail().to_owned()),
        extra,
    )
    .into()
}

pub fn knowledge_client_error(error: ClientError) -> ServiceError {
    match error {
        ClientError::Biz(business) => match business.status_code {
            30_001 => ServiceError::invalid_input("knowledge rejected the request"),
            30_002 | 30_008 => ServiceError::not_found("document not found"),
            30_003 => ServiceError::conflict("document state changed"),
            30_004 | 30_009 => ServiceError::forbidden(),
            30_005 => ServiceError::unauthenticated(),
            30_006 => ServiceError::unavailable(anyhow!("knowledge unavailable")),
            30_007 => ServiceError::precondition_failed(),
            _ => ServiceError::internal(anyhow!("unknown Knowledge business status")),
        },
        ClientError::Transport(error) => ServiceError::unavailable(anyhow!(error)),
        ClientError::Application(error) => ServiceError::internal(anyhow!(error)),
        ClientError::Protocol(error) => ServiceError::internal(anyhow!(error)),
    }
}

const fn error_kind(code: ErrorCode) -> &'static str {
    match code {
        ErrorCode::InvalidInput => "invalid_argument",
        ErrorCode::Unauthenticated => "unauthenticated",
        ErrorCode::Forbidden => "permission_denied",
        ErrorCode::NotFound => "not_found",
        ErrorCode::Conflict | ErrorCode::PreconditionFailed => "conflict",
        ErrorCode::Unavailable => "unavailable",
        ErrorCode::Internal => "internal",
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use volo_thrift::ServerError;

    use super::service_error_with_context;
    use crate::{domain::RequestContext, error::ServiceError};

    #[test]
    fn business_status_contains_only_stable_public_details() {
        let context = RequestContext {
            request_id: Arc::from("request-123"),
            access_token: None,
            deadline: None,
        };
        let ServerError::Biz(error) = service_error_with_context(
            &ServiceError::invalid_input("document_id is invalid"),
            Some(&context),
            None,
        ) else {
            panic!("service error must map to BizStatus");
        };
        assert_eq!(error.status_code, 40_001);
        assert_eq!(error.status_message.as_str(), "document_id is invalid");
        let extra = error.extra.expect("BizStatus extra");
        assert_eq!(
            extra.get("error_key").map(pilota::FastStr::as_str),
            Some("collaboration.invalid_input")
        );
        assert_eq!(
            extra.get("error_kind").map(pilota::FastStr::as_str),
            Some("invalid_argument")
        );
        assert_eq!(
            extra.get("request_id").map(pilota::FastStr::as_str),
            Some("request-123")
        );
        assert!(extra.get("trace_id").is_none());
    }
}
