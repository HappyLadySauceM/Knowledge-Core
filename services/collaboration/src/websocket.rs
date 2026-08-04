use std::{
    collections::HashMap,
    future::Future,
    net::SocketAddr,
    sync::{
        Arc, Mutex as StdMutex,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use axum::{
    Router,
    body::Body,
    extract::{Extension, Path, State, WebSocketUpgrade, ws},
    http::{HeaderMap, HeaderValue, Request, Response, StatusCode, header},
    middleware::{self, Next},
    routing::get,
};
use futures_util::future::{AbortHandle, Abortable};
use opentelemetry::{Context as OpenTelemetryContext, global, propagation::Extractor};
use tokio::{
    net::TcpListener,
    sync::{Mutex, OwnedSemaphorePermit, Semaphore},
    task::JoinHandle,
};
use tokio_util::sync::CancellationToken;
use tokio_util::task::TaskTracker;
use tracing_opentelemetry::OpenTelemetrySpanExt as _;
use uuid::Uuid;

use crate::{
    actor::{
        ActorRegistry, ActorSession, CLOSE_INVALID_PROTOCOL, CLOSE_SERVICE_RESTART, CloseSignal,
        ConnectionEvent,
    },
    admin::HealthState,
    config::{PublicConfig, TicketConfig},
    domain::{DocumentId, RequestContext},
    error::{ErrorCode, Result, ServiceError},
    rpc::{
        BAGGAGE_KEY, REQUEST_ID_KEY, TRACE_PARENT_KEY, TRACE_STATE_KEY, new_request_id,
        valid_baggage, valid_request_id, valid_trace_parent, valid_trace_state,
    },
    telemetry::Metrics,
    ticket::TicketService,
};

const TICKET_PROTOCOL_PREFIX: &str = "ticket.";
const NANOS_PER_SECOND: u128 = 1_000_000_000;

#[derive(Clone)]
struct InboundPropagation {
    request_id: Arc<str>,
    trace_parent: Option<Arc<str>>,
    trace_state: Option<Arc<str>>,
    baggage: Option<Arc<str>>,
}

impl InboundPropagation {
    fn from_headers(headers: &HeaderMap) -> Self {
        let request_id = single_header(headers, REQUEST_ID_KEY)
            .filter(|value| valid_request_id(value))
            .map_or_else(|| Arc::from(new_request_id()), Arc::from);
        Self {
            request_id,
            trace_parent: single_header(headers, TRACE_PARENT_KEY)
                .filter(|value| valid_trace_parent(value))
                .map(Arc::from),
            trace_state: single_header(headers, TRACE_STATE_KEY)
                .filter(|value| valid_trace_state(value))
                .map(Arc::from),
            baggage: single_header(headers, BAGGAGE_KEY)
                .filter(|value| valid_baggage(value))
                .map(Arc::from),
        }
    }
}

impl Extractor for InboundPropagation {
    fn get(&self, key: &str) -> Option<&str> {
        match key {
            TRACE_PARENT_KEY => self.trace_parent.as_deref(),
            TRACE_STATE_KEY => self.trace_state.as_deref(),
            BAGGAGE_KEY => self.baggage.as_deref(),
            _ => None,
        }
    }

    fn keys(&self) -> Vec<&str> {
        let mut keys = Vec::with_capacity(3);
        if self.trace_parent.is_some() {
            keys.push(TRACE_PARENT_KEY);
        }
        if self.trace_state.is_some() {
            keys.push(TRACE_STATE_KEY);
        }
        if self.baggage.is_some() {
            keys.push(BAGGAGE_KEY);
        }
        keys
    }
}

#[derive(Clone)]
struct PublicState {
    config: Arc<PublicConfig>,
    subprotocol: Arc<str>,
    tickets: TicketService,
    actors: ActorRegistry,
    metrics: Metrics,
    connections: Arc<Semaphore>,
    handshake_limiter: HandshakeLimiter,
    accepting: Arc<AtomicBool>,
    health: HealthState,
    connection_tasks: ConnectionTasks,
    cancellation: CancellationToken,
}

pub struct WebSocketServer {
    local_address: SocketAddr,
    state: PublicState,
    running: Arc<AtomicBool>,
    task: Mutex<Option<JoinHandle<Result<()>>>>,
}

#[derive(Clone, Default)]
struct ConnectionTasks {
    tracker: TaskTracker,
    abort_handles: Arc<StdMutex<HashMap<Uuid, AbortHandle>>>,
}

#[derive(Clone)]
struct HandshakeLimiter {
    state: Arc<StdMutex<TokenBucket>>,
}

impl HandshakeLimiter {
    fn new(rate: u32, burst: u32) -> Result<Self> {
        if rate == 0 || burst == 0 {
            return Err(ServiceError::invalid_input(
                "WebSocket handshake rate limits must be greater than zero",
            ));
        }
        Ok(Self {
            state: Arc::new(StdMutex::new(TokenBucket {
                rate: u64::from(rate),
                capacity: u64::from(burst),
                tokens: u64::from(burst),
                refill_remainder: 0,
                last_refill: Instant::now(),
            })),
        })
    }

    fn allow(&self) -> bool {
        lock_bucket(&self.state).allow_at(Instant::now())
    }
}

struct TokenBucket {
    rate: u64,
    capacity: u64,
    tokens: u64,
    refill_remainder: u128,
    last_refill: Instant,
}

impl TokenBucket {
    fn allow_at(&mut self, now: Instant) -> bool {
        if let Some(elapsed) = now.checked_duration_since(self.last_refill) {
            let credit = elapsed
                .as_nanos()
                .saturating_mul(u128::from(self.rate))
                .saturating_add(self.refill_remainder);
            let added = credit / NANOS_PER_SECOND;
            self.refill_remainder = credit % NANOS_PER_SECOND;
            self.last_refill = now;
            self.tokens = self
                .tokens
                .saturating_add(u64::try_from(added).unwrap_or(u64::MAX))
                .min(self.capacity);
            if self.tokens == self.capacity {
                self.refill_remainder = 0;
            }
        }
        if self.tokens == 0 {
            false
        } else {
            self.tokens -= 1;
            true
        }
    }
}

impl ConnectionTasks {
    fn track<F>(&self, future: F) -> impl Future<Output = ()> + Send + 'static + use<F>
    where
        F: Future<Output = ()> + Send + 'static,
    {
        let id = Uuid::now_v7();
        let (abort, registration) = AbortHandle::new_pair();
        lock_handles(&self.abort_handles).insert(id, abort);
        let handles = Arc::clone(&self.abort_handles);
        self.tracker.track_future(async move {
            let _ = Abortable::new(future, registration).await;
            lock_handles(&handles).remove(&id);
        })
    }

    async fn shutdown(&self, maximum_wait: Duration) -> Result<()> {
        self.tracker.close();
        if tokio::time::timeout(maximum_wait, self.tracker.wait())
            .await
            .is_ok()
        {
            return Ok(());
        }
        let handles = lock_handles(&self.abort_handles)
            .values()
            .cloned()
            .collect::<Vec<_>>();
        for handle in handles {
            handle.abort();
        }
        self.tracker.wait().await;
        Err(ServiceError::internal(anyhow::anyhow!(
            "WebSocket connections did not stop before the shutdown deadline"
        )))
    }
}

impl WebSocketServer {
    /// Binds the public listener and starts it in a paused readiness state.
    ///
    /// # Errors
    ///
    /// Returns an error when configuration is invalid or the listener cannot bind.
    pub async fn start(
        public: &PublicConfig,
        ticket: &TicketConfig,
        tickets: TicketService,
        actors: ActorRegistry,
        metrics: Metrics,
        health: HealthState,
        parent_cancellation: &CancellationToken,
    ) -> Result<Self> {
        let listener = TcpListener::bind(public.address).await.map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("bind WebSocket listener"))
        })?;
        let local_address = listener.local_addr().map_err(|error| {
            ServiceError::internal(
                anyhow::anyhow!(error).context("read WebSocket listener address"),
            )
        })?;
        let cancellation = parent_cancellation.child_token();
        let connection_tasks = ConnectionTasks::default();
        let handshake_limiter =
            HandshakeLimiter::new(public.handshakes_per_second, public.handshake_burst)?;
        let state = PublicState {
            config: Arc::new(public.clone()),
            subprotocol: Arc::from(ticket.subprotocol.as_str()),
            tickets,
            actors,
            metrics,
            connections: Arc::new(Semaphore::new(public.max_connections)),
            handshake_limiter,
            accepting: Arc::new(AtomicBool::new(false)),
            health,
            connection_tasks,
            cancellation: cancellation.clone(),
        };
        let router = Router::new()
            .route("/v1/documents/{document_id}", get(upgrade))
            .fallback(not_found)
            .with_state(state.clone())
            .layer(middleware::from_fn(inbound_context));
        let running = Arc::new(AtomicBool::new(true));
        let task_running = Arc::clone(&running);
        let task_accepting = Arc::clone(&state.accepting);
        let task = tokio::spawn(async move {
            let result = axum::serve(listener, router)
                .with_graceful_shutdown(cancellation.cancelled_owned())
                .await
                .map_err(|error| {
                    ServiceError::internal(
                        anyhow::anyhow!(error).context("serve WebSocket listener"),
                    )
                });
            task_accepting.store(false, Ordering::Release);
            task_running.store(false, Ordering::Release);
            result
        });
        Ok(Self {
            local_address,
            state,
            running,
            task: Mutex::new(Some(task)),
        })
    }

    pub fn local_address(&self) -> SocketAddr {
        self.local_address
    }

    pub fn stop_accepting(&self) {
        self.state.accepting.store(false, Ordering::Release);
    }

    /// Enables upgrades after all process dependencies have passed readiness checks.
    ///
    /// # Errors
    ///
    /// Returns an unavailable error when the listener has already stopped.
    pub fn start_accepting(&self) -> Result<()> {
        if !self.running.load(Ordering::Acquire) || self.state.cancellation.is_cancelled() {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "WebSocket listener is not running"
            )));
        }
        self.state.accepting.store(true, Ordering::Release);
        Ok(())
    }

    /// Verifies that the listener, actor registry, and ticket backend are ready.
    ///
    /// # Errors
    ///
    /// Returns an unavailable error when any required component is not ready.
    pub async fn ready(&self) -> Result<()> {
        if !self.running.load(Ordering::Acquire)
            || !self.state.accepting.load(Ordering::Acquire)
            || !self.state.actors.is_accepting()
        {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "WebSocket listener is not accepting connections"
            )));
        }
        self.state.tickets.ping().await
    }

    /// Stops upgrades and drains active WebSocket connections and the listener.
    ///
    /// # Errors
    ///
    /// Returns an error when a connection or the listener exceeds the shutdown deadline.
    pub async fn shutdown(&self, maximum_wait: Duration) -> Result<()> {
        self.stop_accepting();
        self.state.cancellation.cancel();
        let deadline = Instant::now() + maximum_wait;
        let connection_result = self
            .state
            .connection_tasks
            .shutdown(remaining(deadline))
            .await;
        let Some(task) = self.task.lock().await.take() else {
            return connection_result;
        };
        let server_result = join_server_task(task, remaining(deadline)).await;
        connection_result.and(server_result)
    }
}

async fn inbound_context(mut request: Request<Body>, next: Next) -> Response<Body> {
    let propagation = InboundPropagation::from_headers(request.headers());
    let request_id = Arc::clone(&propagation.request_id);
    request.extensions_mut().insert(propagation);
    with_request_id(next.run(request).await, &request_id)
}

#[tracing::instrument(
    skip_all,
    fields(http.route = "/v1/documents/{document_id}", request_id = tracing::field::Empty)
)]
async fn upgrade(
    State(state): State<PublicState>,
    Path(document_id): Path<String>,
    Extension(propagation): Extension<InboundPropagation>,
    headers: HeaderMap,
    websocket: WebSocketUpgrade,
) -> Response<Body> {
    let span = tracing::Span::current();
    let parent = global::get_text_map_propagator(|propagator| {
        propagator.extract_with_context(&OpenTelemetryContext::new(), &propagation)
    });
    let _ = span.set_parent(parent);
    span.record("request_id", propagation.request_id.as_ref());

    if !state.accepting.load(Ordering::Acquire) || !state.health.is_ready() {
        return reject(&state.metrics, HttpReject::Unavailable);
    }
    let Ok(document_id) = DocumentId::parse(&document_id) else {
        return reject(&state.metrics, HttpReject::BadRequest);
    };
    if !valid_origin(&headers, &state.config.allowed_origins) {
        return reject(&state.metrics, HttpReject::Forbidden);
    }
    let ticket = match requested_ticket(&websocket, &state.subprotocol) {
        Ok(ticket) => ticket,
        Err(rejection) => return reject(&state.metrics, rejection),
    };
    if !state.handshake_limiter.allow() {
        return reject(&state.metrics, HttpReject::RateLimited);
    }
    let Ok(permit) = Arc::clone(&state.connections).try_acquire_owned() else {
        return reject(&state.metrics, HttpReject::RateLimited);
    };
    let context = handshake_context(
        state.config.handshake_timeout,
        Arc::clone(&propagation.request_id),
    );
    let authorization = tokio::time::timeout(
        state.config.handshake_timeout,
        authorize(&state, &context, document_id, &ticket),
    )
    .await;
    let connection = match authorization {
        Ok(Ok(connection)) => connection,
        Ok(Err(rejection)) => return reject(&state.metrics, rejection),
        Err(_) => return reject(&state.metrics, HttpReject::Unavailable),
    };
    state.metrics.handshake("accepted");
    let protocol = state.subprotocol.to_string();
    let maximum_frame_bytes = state.config.max_frame_bytes;
    let write_timeout = state.config.handshake_timeout;
    let cancellation = state.cancellation.child_token();
    let connection_tasks = state.connection_tasks.clone();
    websocket
        .max_frame_size(maximum_frame_bytes)
        .max_message_size(maximum_frame_bytes)
        .protocols([protocol])
        .on_upgrade(move |socket| {
            connection_tasks.track(run_connection(
                socket,
                connection,
                permit,
                maximum_frame_bytes,
                write_timeout,
                cancellation,
            ))
        })
}

async fn authorize(
    state: &PublicState,
    context: &RequestContext,
    document_id: DocumentId,
    ticket: &str,
) -> std::result::Result<crate::actor::ActorConnection, HttpReject> {
    let claims = state
        .tickets
        .consume(context, ticket, document_id)
        .await
        .map_err(|error| HttpReject::from_service_error(&error))?;
    state
        .actors
        .connect(context, document_id, ActorSession::from(claims))
        .await
        .map_err(HttpReject::from_close)
}

fn handshake_context(maximum_wait: Duration, request_id: Arc<str>) -> RequestContext {
    let mut context = RequestContext::new(request_id);
    context.deadline = Instant::now().checked_add(maximum_wait);
    context
}

async fn run_connection(
    mut socket: ws::WebSocket,
    mut connection: crate::actor::ActorConnection,
    _permit: OwnedSemaphorePermit,
    maximum_frame_bytes: usize,
    write_timeout: Duration,
    cancellation: CancellationToken,
) {
    loop {
        tokio::select! {
            biased;
            () = cancellation.cancelled() => {
                send_close(&mut socket, CLOSE_SERVICE_RESTART, write_timeout).await;
                break;
            }
            event = connection.recv() => {
                match event {
                    ConnectionEvent::Binary(payload) => {
                        if !send_message(
                            &mut socket,
                            ws::Message::Binary(payload.into()),
                            write_timeout,
                        ).await {
                            break;
                        }
                    }
                    ConnectionEvent::Close(close) => {
                        send_close(&mut socket, close, write_timeout).await;
                        break;
                    }
                }
            }
            incoming = socket.recv() => {
                match incoming {
                    Some(Ok(ws::Message::Binary(payload))) => {
                        if payload.len() > maximum_frame_bytes {
                            send_close(&mut socket, CLOSE_INVALID_PROTOCOL, write_timeout).await;
                            break;
                        }
                        if let Err(close) = connection.send(payload.to_vec()).await {
                            send_close(&mut socket, close, write_timeout).await;
                            break;
                        }
                    }
                    Some(Ok(ws::Message::Text(_))) => {
                        send_close(&mut socket, CLOSE_INVALID_PROTOCOL, write_timeout).await;
                        break;
                    }
                    Some(Ok(ws::Message::Close(_)) | Err(_)) | None => break,
                    Some(Ok(ws::Message::Ping(_) | ws::Message::Pong(_))) => {}
                }
            }
        }
    }
    drop(connection);
}

async fn send_message(
    socket: &mut ws::WebSocket,
    message: ws::Message,
    maximum_wait: Duration,
) -> bool {
    matches!(
        tokio::time::timeout(maximum_wait, socket.send(message)).await,
        Ok(Ok(()))
    )
}

async fn send_close(socket: &mut ws::WebSocket, close: CloseSignal, maximum_wait: Duration) {
    let _ = tokio::time::timeout(
        maximum_wait,
        socket.send(ws::Message::Close(Some(ws::CloseFrame {
            code: close.code,
            reason: close.reason.into(),
        }))),
    )
    .await;
}

async fn join_server_task(mut task: JoinHandle<Result<()>>, maximum_wait: Duration) -> Result<()> {
    if let Ok(result) = tokio::time::timeout(maximum_wait, &mut task).await {
        result.map_err(|error| {
            ServiceError::internal(anyhow::Error::new(error).context("join WebSocket server task"))
        })?
    } else {
        task.abort();
        match task.await {
            Err(error) if error.is_cancelled() => {}
            Err(error) => {
                return Err(ServiceError::internal(
                    anyhow::Error::new(error).context("abort WebSocket server task"),
                ));
            }
            Ok(result) => result?,
        }
        Err(ServiceError::internal(anyhow::anyhow!(
            "WebSocket server did not stop before the shutdown deadline"
        )))
    }
}

fn remaining(deadline: Instant) -> Duration {
    deadline
        .checked_duration_since(Instant::now())
        .unwrap_or(Duration::ZERO)
}

fn lock_handles(
    handles: &StdMutex<HashMap<Uuid, AbortHandle>>,
) -> std::sync::MutexGuard<'_, HashMap<Uuid, AbortHandle>> {
    handles
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

fn lock_bucket(bucket: &StdMutex<TokenBucket>) -> std::sync::MutexGuard<'_, TokenBucket> {
    bucket
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

fn single_header<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    let mut values = headers.get_all(name).iter();
    let value = values.next()?.to_str().ok()?;
    values.next().is_none().then_some(value)
}

fn valid_origin(headers: &HeaderMap, allowed_origins: &[String]) -> bool {
    let mut origins = headers.get_all(header::ORIGIN).iter();
    let Some(origin) = origins.next() else {
        return false;
    };
    if origins.next().is_some() {
        return false;
    }
    origin.to_str().ok().is_some_and(|origin| {
        !origin.is_empty() && allowed_origins.iter().any(|allowed| allowed == origin)
    })
}

fn requested_ticket(
    websocket: &WebSocketUpgrade,
    expected_protocol: &str,
) -> std::result::Result<String, HttpReject> {
    let protocols = websocket
        .requested_protocols()
        .map(|value| value.to_str().map(ToOwned::to_owned))
        .collect::<std::result::Result<Vec<_>, _>>()
        .map_err(|_| HttpReject::BadRequest)?;
    extract_ticket_from_protocols(&protocols, expected_protocol).map(ToOwned::to_owned)
}

fn extract_ticket_from_protocols<'a>(
    protocols: &'a [String],
    expected_protocol: &str,
) -> std::result::Result<&'a str, HttpReject> {
    if protocols.len() != 2 || protocols[0] != expected_protocol {
        return Err(HttpReject::BadRequest);
    }
    let ticket = protocols[1]
        .strip_prefix(TICKET_PROTOCOL_PREFIX)
        .filter(|value| !value.is_empty())
        .ok_or(HttpReject::BadRequest)?;
    Ok(ticket)
}

#[derive(Clone, Copy, Debug)]
enum HttpReject {
    BadRequest,
    Unauthenticated,
    Forbidden,
    RateLimited,
    Unavailable,
}

impl HttpReject {
    fn from_service_error(error: &ServiceError) -> Self {
        match error.code() {
            ErrorCode::InvalidInput => Self::BadRequest,
            ErrorCode::Unauthenticated | ErrorCode::NotFound => Self::Unauthenticated,
            ErrorCode::Forbidden | ErrorCode::Conflict | ErrorCode::PreconditionFailed => {
                Self::Forbidden
            }
            ErrorCode::Unavailable | ErrorCode::Internal => Self::Unavailable,
        }
    }

    const fn from_close(close: CloseSignal) -> Self {
        match close.code {
            4401 => Self::Unauthenticated,
            4403 | 4409 => Self::Forbidden,
            4429 => Self::RateLimited,
            4503 => Self::Unavailable,
            _ => Self::BadRequest,
        }
    }

    const fn status(self) -> StatusCode {
        match self {
            Self::BadRequest => StatusCode::BAD_REQUEST,
            Self::Unauthenticated => StatusCode::UNAUTHORIZED,
            Self::Forbidden => StatusCode::FORBIDDEN,
            Self::RateLimited => StatusCode::TOO_MANY_REQUESTS,
            Self::Unavailable => StatusCode::SERVICE_UNAVAILABLE,
        }
    }

    const fn metric(self) -> &'static str {
        match self {
            Self::BadRequest => "bad_request",
            Self::Unauthenticated => "unauthenticated",
            Self::Forbidden => "forbidden",
            Self::RateLimited => "rate_limited",
            Self::Unavailable => "unavailable",
        }
    }

    const fn body(self) -> &'static str {
        match self {
            Self::BadRequest => r#"{"error":"invalid_request"}"#,
            Self::Unauthenticated => r#"{"error":"authentication_required"}"#,
            Self::Forbidden => r#"{"error":"permission_denied"}"#,
            Self::RateLimited => r#"{"error":"rate_limited"}"#,
            Self::Unavailable => r#"{"error":"dependency_unavailable"}"#,
        }
    }
}

fn reject(metrics: &Metrics, rejection: HttpReject) -> Response<Body> {
    metrics.handshake(rejection.metric());
    response(rejection.status(), rejection.body())
}

async fn not_found() -> Response<Body> {
    response(StatusCode::NOT_FOUND, r#"{"error":"not_found"}"#)
}

fn response(status: StatusCode, body: &'static str) -> Response<Body> {
    let mut response = Response::new(Body::from(body));
    *response.status_mut() = status;
    let headers = response.headers_mut();
    headers.insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    headers.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    headers.insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
    response
}

fn with_request_id(mut response: Response<Body>, request_id: &str) -> Response<Body> {
    if let Ok(value) = HeaderValue::from_str(request_id) {
        response.headers_mut().insert(REQUEST_ID_KEY, value);
    }
    response
}

#[cfg(test)]
mod tests {
    use std::{
        sync::{
            Arc,
            atomic::{AtomicBool, Ordering},
        },
        time::Duration,
    };

    use super::{
        ConnectionTasks, InboundPropagation, TICKET_PROTOCOL_PREFIX, TokenBucket,
        extract_ticket_from_protocols, inbound_context, valid_origin,
    };
    use axum::{
        Router,
        body::Body,
        http::{HeaderMap, HeaderValue, Request, StatusCode, header},
        middleware,
        routing::get,
    };
    use opentelemetry::{
        Context as OpenTelemetryContext,
        baggage::BaggageExt as _,
        propagation::{Extractor as _, TextMapCompositePropagator, TextMapPropagator as _},
        trace::TraceContextExt as _,
    };
    use opentelemetry_sdk::propagation::{BaggagePropagator, TraceContextPropagator};
    use tokio::sync::Notify;
    use tower::ServiceExt as _;

    use crate::rpc::{
        BAGGAGE_KEY, REQUEST_ID_KEY, TRACE_PARENT_KEY, TRACE_STATE_KEY, valid_request_id,
    };

    const TRACE_PARENT: &str = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";

    struct DropSignal(Arc<AtomicBool>);

    impl Drop for DropSignal {
        fn drop(&mut self) {
            self.0.store(true, Ordering::Release);
        }
    }

    #[test]
    fn origin_must_be_present_once_and_match_exactly() {
        let mut headers = HeaderMap::new();
        let allowed = vec!["https://studio.example.com".to_owned()];
        assert!(!valid_origin(&headers, &allowed));
        headers.insert(
            header::ORIGIN,
            HeaderValue::from_static("https://studio.example.com"),
        );
        assert!(valid_origin(&headers, &allowed));
        headers.insert(
            header::ORIGIN,
            HeaderValue::from_static("https://studio.example.com/"),
        );
        assert!(!valid_origin(&headers, &allowed));
    }

    #[test]
    fn inbound_request_id_accepts_one_bounded_value_and_regenerates_untrusted_values() {
        let mut headers = HeaderMap::new();
        headers.insert(REQUEST_ID_KEY, HeaderValue::from_static("request-123"));
        assert_eq!(
            InboundPropagation::from_headers(&headers)
                .request_id
                .as_ref(),
            "request-123"
        );

        headers.insert(REQUEST_ID_KEY, HeaderValue::from_static("request id"));
        let regenerated = InboundPropagation::from_headers(&headers).request_id;
        assert_ne!(regenerated.as_ref(), "request id");
        assert!(valid_request_id(&regenerated));

        headers.insert(REQUEST_ID_KEY, HeaderValue::from_static("request-123"));
        headers.append(REQUEST_ID_KEY, HeaderValue::from_static("request-456"));
        let regenerated = InboundPropagation::from_headers(&headers).request_id;
        assert_ne!(regenerated.as_ref(), "request-123");
        assert_ne!(regenerated.as_ref(), "request-456");
        assert!(valid_request_id(&regenerated));
    }

    #[tokio::test]
    async fn every_upgrade_rejection_returns_the_normalized_request_id() {
        let app = Router::new()
            .route("/400", get(|| async { StatusCode::BAD_REQUEST }))
            .route("/401", get(|| async { StatusCode::UNAUTHORIZED }))
            .route("/403", get(|| async { StatusCode::FORBIDDEN }))
            .route("/429", get(|| async { StatusCode::TOO_MANY_REQUESTS }))
            .route("/503", get(|| async { StatusCode::SERVICE_UNAVAILABLE }))
            .layer(middleware::from_fn(inbound_context));
        for (path, expected_status) in [
            ("/400", StatusCode::BAD_REQUEST),
            ("/401", StatusCode::UNAUTHORIZED),
            ("/403", StatusCode::FORBIDDEN),
            ("/429", StatusCode::TOO_MANY_REQUESTS),
            ("/503", StatusCode::SERVICE_UNAVAILABLE),
        ] {
            let request = Request::builder()
                .uri(path)
                .header(REQUEST_ID_KEY, "request-123")
                .body(Body::empty())
                .expect("request");
            let response = app.clone().oneshot(request).await.expect("response");
            assert_eq!(response.status(), expected_status);
            assert_eq!(
                response.headers().get(REQUEST_ID_KEY),
                Some(&HeaderValue::from_static("request-123"))
            );
        }
    }

    #[test]
    fn inbound_w3c_headers_are_bounded_and_form_a_remote_parent_context() {
        let mut headers = HeaderMap::new();
        headers.insert(TRACE_PARENT_KEY, HeaderValue::from_static(TRACE_PARENT));
        headers.insert(TRACE_STATE_KEY, HeaderValue::from_static("vendor=value"));
        headers.insert(BAGGAGE_KEY, HeaderValue::from_static("tenant=bounded"));
        let propagation = InboundPropagation::from_headers(&headers);
        let propagator = TextMapCompositePropagator::new(vec![
            Box::new(TraceContextPropagator::new()),
            Box::new(BaggagePropagator::new()),
        ]);
        let context = propagator.extract_with_context(&OpenTelemetryContext::new(), &propagation);
        let span = context.span();
        assert!(span.span_context().is_remote());
        assert_eq!(
            span.span_context().trace_id().to_string(),
            "4bf92f3577b34da6a3ce929d0e0e4736"
        );
        assert_eq!(
            context
                .baggage()
                .get("tenant")
                .map(ToString::to_string)
                .as_deref(),
            Some("bounded")
        );

        headers.append(TRACE_PARENT_KEY, HeaderValue::from_static(TRACE_PARENT));
        let propagation = InboundPropagation::from_headers(&headers);
        assert!(propagation.get(TRACE_PARENT_KEY).is_none());
        headers.insert(
            BAGGAGE_KEY,
            HeaderValue::from_str(&"a".repeat(8_193)).expect("large header value"),
        );
        let propagation = InboundPropagation::from_headers(&headers);
        assert!(propagation.get(BAGGAGE_KEY).is_none());
    }

    #[test]
    fn ticket_subprotocol_prefix_is_stable() {
        assert_eq!(TICKET_PROTOCOL_PREFIX, "ticket.");
        let protocols = vec![
            "knowledge-core-yjs-v1".to_owned(),
            "ticket.opaque".to_owned(),
        ];
        assert_eq!(
            extract_ticket_from_protocols(&protocols, "knowledge-core-yjs-v1")
                .expect("strict protocols"),
            "opaque"
        );
        assert!(extract_ticket_from_protocols(&protocols, "another-protocol").is_err());
        let mut extra = protocols;
        extra.push("extra".to_owned());
        assert!(extract_ticket_from_protocols(&extra, "knowledge-core-yjs-v1").is_err());
    }

    #[tokio::test]
    async fn overdue_connection_is_aborted_and_joined() {
        let connections = ConnectionTasks::default();
        let started = Arc::new(Notify::new());
        let dropped = Arc::new(AtomicBool::new(false));
        let task = tokio::spawn(connections.track({
            let started = Arc::clone(&started);
            let dropped = Arc::clone(&dropped);
            async move {
                let _drop_signal = DropSignal(dropped);
                started.notify_one();
                std::future::pending::<()>().await;
            }
        }));
        started.notified().await;

        assert!(connections.shutdown(Duration::ZERO).await.is_err());
        task.await.expect("tracked task");
        assert!(dropped.load(Ordering::Acquire));
    }

    #[test]
    fn handshake_bucket_honors_burst_and_integer_refill() {
        let started = std::time::Instant::now();
        let mut bucket = TokenBucket {
            rate: 2,
            capacity: 3,
            tokens: 3,
            refill_remainder: 0,
            last_refill: started,
        };
        assert!(bucket.allow_at(started));
        assert!(bucket.allow_at(started));
        assert!(bucket.allow_at(started));
        assert!(!bucket.allow_at(started));
        assert!(!bucket.allow_at(started + Duration::from_millis(499)));
        assert!(bucket.allow_at(started + Duration::from_millis(500)));
        assert!(!bucket.allow_at(started + Duration::from_millis(999)));
        assert!(bucket.allow_at(started + Duration::from_secs(1)));
    }
}
