use std::{
    cell::RefCell,
    future::Future,
    sync::Arc,
    time::{Duration, Instant},
};

use metainfo::{Forward, METAINFO, MetaInfo};
use motore::{layer::Layer, service::Service};
use opentelemetry::{
    Context as OpenTelemetryContext, global,
    propagation::{Extractor, Injector},
};
use tracing::Instrument as _;
use tracing_opentelemetry::OpenTelemetrySpanExt as _;
use uuid::Uuid;
use volo::context::Context;
use volo_thrift::{ServerError, context::ServerContext};

use crate::{
    domain::{RequestContext, Secret},
    error::{Result, ServiceError},
};

pub const ACCESS_TOKEN_KEY: &str = "knowledge-core-access-token";
pub const REQUEST_ID_KEY: &str = "x-request-id";
pub const TRACE_PARENT_KEY: &str = "traceparent";
pub const TRACE_STATE_KEY: &str = "tracestate";
pub const BAGGAGE_KEY: &str = "baggage";
const MAX_REQUEST_ID_LENGTH: usize = 128;
const MAX_TRACE_STATE_LENGTH: usize = 512;
const MAX_BAGGAGE_LENGTH: usize = 8_192;

#[derive(Clone)]
struct RpcMetadata {
    request: RequestContext,
    trace_parent: Option<Arc<str>>,
    trace_state: Option<Arc<str>>,
    baggage: Option<Arc<str>>,
}

impl Extractor for RpcMetadata {
    fn get(&self, key: &str) -> Option<&str> {
        match key {
            TRACE_PARENT_KEY => self.trace_parent.as_deref(),
            TRACE_STATE_KEY => self.trace_state.as_deref(),
            BAGGAGE_KEY => self.baggage.as_deref(),
            _ => None,
        }
    }

    fn keys(&self) -> Vec<&str> {
        vec![TRACE_PARENT_KEY, TRACE_STATE_KEY, BAGGAGE_KEY]
    }
}

tokio::task_local! {
    static RPC_METADATA: RpcMetadata;
}

#[derive(Clone, Copy, Debug)]
pub struct RequestContextLayer {
    maximum_timeout: Duration,
}

impl RequestContextLayer {
    /// Creates the inbound RPC context layer with a hard timeout ceiling.
    ///
    /// # Errors
    ///
    /// Returns an error when `maximum_timeout` is zero.
    pub fn new(maximum_timeout: Duration) -> Result<Self> {
        if maximum_timeout.is_zero() {
            return Err(ServiceError::invalid_input(
                "RPC maximum timeout must be greater than zero",
            ));
        }
        Ok(Self { maximum_timeout })
    }
}

impl<S> Layer<S> for RequestContextLayer {
    type Service = RequestContextService<S>;

    fn layer(self, inner: S) -> Self::Service {
        RequestContextService {
            inner,
            maximum_timeout: self.maximum_timeout,
        }
    }
}

#[derive(Clone)]
pub struct RequestContextService<S> {
    inner: S,
    maximum_timeout: Duration,
}

impl<S, Request> Service<ServerContext, Request> for RequestContextService<S>
where
    S: Service<ServerContext, Request, Error = ServerError> + Send + Sync + 'static,
    S::Response: Send + 'static,
    Request: Send + 'static,
{
    type Response = S::Response;
    type Error = ServerError;

    async fn call(
        &self,
        context: &mut ServerContext,
        request: Request,
    ) -> std::result::Result<Self::Response, Self::Error> {
        let timeout = context
            .rpc_info()
            .config()
            .rpc_timeout()
            .filter(|timeout| !timeout.is_zero())
            .map_or(self.maximum_timeout, |timeout| {
                timeout.min(self.maximum_timeout)
            });
        let metadata = extract_metadata(timeout)
            .map_err(|error| super::biz::service_error_with_context(&error, None, None))?;
        let request_context = metadata.request.clone();
        let method = context.rpc_info().method().to_string();
        let span = (!is_ignored_rpc_method(&method)).then(|| {
            let span = tracing::info_span!(
                "collaboration.rpc.server",
                rpc.system = "volo_thrift",
                rpc.service = crate::SERVICE_NAME,
                rpc.method = %method,
                request_id = %request_context.request_id,
            );
            let parent = global::get_text_map_propagator(|propagator| {
                propagator.extract_with_context(&OpenTelemetryContext::new(), &metadata)
            });
            let _ = span.set_parent(parent);
            span
        });

        RPC_METADATA
            .scope(
                metadata,
                async {
                    match tokio::time::timeout(timeout, self.inner.call(context, request)).await {
                        Ok(result) => result,
                        Err(_) => Err(super::biz::deadline_error(&request_context)),
                    }
                }
                .instrument(span.unwrap_or_else(tracing::Span::none)),
            )
            .await
    }
}

fn is_ignored_rpc_method(method: &str) -> bool {
    matches!(
        method.to_ascii_lowercase().as_str(),
        "ping" | "live" | "health" | "healthcheck"
    )
}

/// Returns the request context scoped to the current inbound RPC task.
///
/// # Errors
///
/// Returns an error outside an inbound RPC task.
pub fn current_request_context() -> Result<RequestContext> {
    try_current_request_context().ok_or_else(|| {
        ServiceError::internal(anyhow::anyhow!("RPC request context is unavailable"))
    })
}

pub(crate) fn try_current_request_context() -> Option<RequestContext> {
    RPC_METADATA
        .try_with(|metadata| metadata.request.clone())
        .ok()
}

pub fn current_trace_id() -> Option<String> {
    RPC_METADATA
        .try_with(|metadata| {
            metadata
                .trace_parent
                .as_deref()
                .and_then(trace_id_from_parent)
                .map(ToOwned::to_owned)
        })
        .ok()
        .flatten()
}

pub fn current_trace_state() -> Option<String> {
    RPC_METADATA
        .try_with(|metadata| metadata.trace_state.as_deref().map(ToOwned::to_owned))
        .ok()
        .flatten()
}

pub fn current_baggage() -> Option<String> {
    RPC_METADATA
        .try_with(|metadata| metadata.baggage.as_deref().map(ToOwned::to_owned))
        .ok()
        .flatten()
}

pub async fn scope_outgoing_metadata<Output>(
    context: &RequestContext,
    future: impl Future<Output = Output>,
) -> Output {
    let propagation = RPC_METADATA
        .try_with(|metadata| {
            (
                metadata.trace_parent.clone(),
                metadata.trace_state.clone(),
                metadata.baggage.clone(),
            )
        })
        .unwrap_or_default();
    let mut injected = PropagationInjector::default();
    global::get_text_map_propagator(|propagator| {
        propagator.inject_context(&OpenTelemetryContext::current(), &mut injected);
    });
    let mut metadata = MetaInfo::new();
    metadata.set_persistent(REQUEST_ID_KEY, context.request_id.to_string());
    if let Some(token) = &context.access_token {
        metadata.set_persistent(ACCESS_TOKEN_KEY, token.expose().to_owned());
    }
    if let Some(value) = injected
        .trace_parent
        .or_else(|| propagation.0.map(|value| value.to_string()))
    {
        metadata.set_persistent(TRACE_PARENT_KEY, value);
    }
    if let Some(value) = injected
        .trace_state
        .or_else(|| propagation.1.map(|value| value.to_string()))
    {
        metadata.set_persistent(TRACE_STATE_KEY, value);
    }
    if let Some(value) = injected
        .baggage
        .or_else(|| propagation.2.map(|value| value.to_string()))
    {
        metadata.set_persistent(BAGGAGE_KEY, value);
    }
    METAINFO.scope(RefCell::new(metadata), future).await
}

#[derive(Default)]
struct PropagationInjector {
    trace_parent: Option<String>,
    trace_state: Option<String>,
    baggage: Option<String>,
}

impl Injector for PropagationInjector {
    fn set(&mut self, key: &str, value: String) {
        match key {
            TRACE_PARENT_KEY => self.trace_parent = Some(value),
            TRACE_STATE_KEY => self.trace_state = Some(value),
            BAGGAGE_KEY if value.len() <= MAX_BAGGAGE_LENGTH => self.baggage = Some(value),
            _ => {}
        }
    }
}

#[cfg(test)]
pub(crate) async fn scope_request_context_for_test<Output>(
    request: RequestContext,
    future: impl Future<Output = Output>,
) -> Output {
    RPC_METADATA
        .scope(
            RpcMetadata {
                request,
                trace_parent: None,
                trace_state: None,
                baggage: None,
            },
            future,
        )
        .await
}

fn extract_metadata(timeout: Duration) -> Result<RpcMetadata> {
    let values = METAINFO
        .try_with(|metadata| {
            let metadata = metadata.borrow();
            (
                metadata.get_persistent(REQUEST_ID_KEY),
                metadata.get_persistent(ACCESS_TOKEN_KEY),
                metadata.get_persistent(TRACE_PARENT_KEY),
                metadata.get_persistent(TRACE_STATE_KEY),
                metadata.get_persistent(BAGGAGE_KEY),
            )
        })
        .map_err(|_| ServiceError::internal(anyhow::anyhow!("TTHeader metadata is unavailable")))?;

    let request_id = values
        .0
        .as_deref()
        .filter(|value| valid_request_id(value))
        .map_or_else(new_request_id, ToOwned::to_owned);
    let access_token = values
        .1
        .map(|value| Secret::new(value.as_str()))
        .transpose()?;
    let trace_parent = values
        .2
        .filter(|value| valid_trace_parent(value))
        .map(|value| Arc::<str>::from(value.as_str()));
    let trace_state = values
        .3
        .filter(|value| valid_trace_state(value))
        .map(|value| Arc::<str>::from(value.as_str()));
    let baggage = values
        .4
        .filter(|value| valid_baggage(value))
        .map(|value| Arc::<str>::from(value.as_str()));

    Ok(RpcMetadata {
        request: RequestContext {
            request_id: Arc::from(request_id),
            access_token,
            deadline: Instant::now().checked_add(timeout),
            trace_parent: trace_parent.clone(),
            trace_state: trace_state.clone(),
            baggage: baggage.clone(),
        },
        trace_parent,
        trace_state,
        baggage,
    })
}

pub(crate) fn new_request_id() -> String {
    Uuid::now_v7().simple().to_string()
}

pub(crate) fn valid_request_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= MAX_REQUEST_ID_LENGTH
        && value.bytes().all(|byte| (b'!'..=b'~').contains(&byte))
}

pub(crate) fn valid_trace_parent(value: &str) -> bool {
    if value.len() != 55
        || &value[0..3] != "00-"
        || value.as_bytes()[35] != b'-'
        || value.as_bytes()[52] != b'-'
    {
        return false;
    }
    let trace_id = &value[3..35];
    let parent_id = &value[36..52];
    let flags = &value[53..55];
    valid_hex(trace_id)
        && trace_id.bytes().any(|byte| byte != b'0')
        && valid_hex(parent_id)
        && parent_id.bytes().any(|byte| byte != b'0')
        && valid_hex(flags)
}

fn trace_id_from_parent(value: &str) -> Option<&str> {
    valid_trace_parent(value).then(|| &value[3..35])
}

fn valid_hex(value: &str) -> bool {
    value.bytes().all(|byte| byte.is_ascii_hexdigit())
}

fn valid_metadata_value(value: &str, maximum_length: usize) -> bool {
    !value.is_empty()
        && value.len() <= maximum_length
        && value
            .bytes()
            .all(|byte| byte == b'\t' || (b' '..=b'~').contains(&byte))
}

pub(crate) fn valid_trace_state(value: &str) -> bool {
    valid_metadata_value(value, MAX_TRACE_STATE_LENGTH)
}

pub(crate) fn valid_baggage(value: &str) -> bool {
    valid_metadata_value(value, MAX_BAGGAGE_LENGTH)
}

#[cfg(test)]
mod tests {
    use std::{cell::RefCell, sync::Arc};

    use metainfo::{Forward, METAINFO, MetaInfo};

    use super::{
        ACCESS_TOKEN_KEY, BAGGAGE_KEY, REQUEST_ID_KEY, RPC_METADATA, RpcMetadata, TRACE_PARENT_KEY,
        TRACE_STATE_KEY, scope_outgoing_metadata, trace_id_from_parent, valid_request_id,
        valid_trace_parent,
    };
    use crate::domain::{RequestContext, Secret};

    const TRACE_PARENT: &str = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";

    #[test]
    fn validates_request_ids() {
        assert!(valid_request_id("request-123"));
        assert!(!valid_request_id("request id"));
        assert!(!valid_request_id("request\nforged"));
    }

    #[test]
    fn validates_trace_parent_and_extracts_trace_id() {
        assert!(valid_trace_parent(TRACE_PARENT));
        assert_eq!(
            trace_id_from_parent(TRACE_PARENT),
            Some("4bf92f3577b34da6a3ce929d0e0e4736")
        );
        assert!(!valid_trace_parent(
            "00-00000000000000000000000000000000-00f067aa0ba902b7-01"
        ));
    }

    #[tokio::test]
    async fn outgoing_metadata_forwards_only_the_explicit_allowlist() {
        let mut request = RequestContext::new("request-123");
        request.access_token = Some(Secret::new("opaque-token").expect("access token"));
        let scoped = RpcMetadata {
            request: request.clone(),
            trace_parent: Some(Arc::from(TRACE_PARENT)),
            trace_state: Some(Arc::from("vendor=value")),
            baggage: Some(Arc::from("tenant=bounded")),
        };
        let mut ambient = MetaInfo::new();
        ambient.set_persistent("authorization", "must-not-forward");
        ambient.set_persistent("cookie", "must-not-forward");
        ambient.set_persistent("x-user-id", "must-not-forward");

        METAINFO
            .scope(RefCell::new(ambient), async {
                RPC_METADATA
                    .scope(scoped, async {
                        scope_outgoing_metadata(&request, async {
                            METAINFO
                                .try_with(|metadata| {
                                    let metadata = metadata.borrow();
                                    assert_eq!(
                                        metadata.get_persistent(REQUEST_ID_KEY).as_deref(),
                                        Some("request-123")
                                    );
                                    assert_eq!(
                                        metadata.get_persistent(ACCESS_TOKEN_KEY).as_deref(),
                                        Some("opaque-token")
                                    );
                                    assert_eq!(
                                        metadata.get_persistent(TRACE_PARENT_KEY).as_deref(),
                                        Some(TRACE_PARENT)
                                    );
                                    assert_eq!(
                                        metadata.get_persistent(TRACE_STATE_KEY).as_deref(),
                                        Some("vendor=value")
                                    );
                                    assert_eq!(
                                        metadata.get_persistent(BAGGAGE_KEY).as_deref(),
                                        Some("tenant=bounded")
                                    );
                                    for forbidden in ["authorization", "cookie", "x-user-id"] {
                                        assert!(metadata.get_persistent(forbidden).is_none());
                                    }
                                })
                                .expect("outgoing metadata scope");
                        })
                        .await;
                    })
                    .await;
            })
            .await;
    }
}
