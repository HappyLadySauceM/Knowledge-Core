use std::{
    cell::RefCell, env, error::Error, io::Write as _, net::SocketAddr, path::PathBuf, sync::Arc,
    time::Duration,
};

use async_trait::async_trait;
use knowledge_core_collaboration::{
    SERVICE_NAME,
    config::{RpcConfig, TlsConfig},
    domain::Secret,
    endpoint::resolve_socket_addresses,
    error::ServiceError,
    generated::{collaboration, common, knowledge},
    rpc::{
        ACCESS_TOKEN_KEY, BAGGAGE_KEY, REQUEST_ID_KEY, RpcReadiness, RpcServer, TRACE_PARENT_KEY,
        TRACE_STATE_KEY, current_baggage, current_request_context, current_trace_id,
        current_trace_state, service_error,
        tls::{RpcIncoming, client_transport},
    },
};
use metainfo::{Forward, METAINFO, MetaInfo};
use pilota::FastStr;
use tokio_util::sync::CancellationToken;
use volo::net::Address;
use volo_thrift::{ClientError, ServerError, client::CallOpt};

const USAGE: &str = "usage: volo_interop_fixture <server|client>";

#[tokio::main]
async fn main() -> std::result::Result<(), Box<dyn Error>> {
    match env::args().nth(1).as_deref() {
        Some("server") => run_server().await,
        Some("client") => run_client().await,
        _ => Err(USAGE.into()),
    }
}

async fn run_server() -> std::result::Result<(), Box<dyn Error>> {
    let address = required("KC_INTEROP_ADDRESS")?.parse::<SocketAddr>()?;
    let request_timeout = duration("KC_INTEROP_SERVER_TIMEOUT_MS", 500)?;
    let tls = tls_config();
    let shutdown = CancellationToken::new();
    let incoming = RpcIncoming::bind(address, &tls, request_timeout, shutdown).await?;
    let local_address = incoming.local_addr()?;
    let handler = FixtureHandler {
        expected_token: required("KC_INTEROP_EXPECT_TOKEN")?,
        expected_request_id: required("KC_INTEROP_EXPECT_REQUEST_ID")?,
        expected_trace_id: required("KC_INTEROP_EXPECT_TRACE_ID")?,
        expected_trace_state: required("KC_INTEROP_TRACE_STATE")?,
        expected_baggage: required("KC_INTEROP_BAGGAGE")?,
        delay: duration("KC_INTEROP_DELAY_MS", 2_000)?,
    };
    let config = RpcConfig {
        address,
        advertised_address: local_address.to_string(),
        service_name: "knowledge-core.collaboration".to_owned(),
        request_timeout,
        tls,
    };
    let server = RpcServer::new(&config, handler, incoming, Arc::new(Ready))?;
    println!("READY {local_address}");
    std::io::stdout().flush()?;
    server.serve().await?;
    Ok(())
}

async fn run_client() -> std::result::Result<(), Box<dyn Error>> {
    let request_timeout = duration("KC_INTEROP_CLIENT_TIMEOUT_MS", 500)?;
    let endpoint = required("KC_INTEROP_ADDRESS")?;
    let address = resolve_socket_addresses(&endpoint, request_timeout)
        .await?
        .into_iter()
        .next()
        .ok_or("interop endpoint resolution returned no addresses")?;
    let tls = tls_config();
    let metadata = FixtureMetadata {
        token: required("KC_INTEROP_EXPECT_TOKEN")?,
        request_id: required("KC_INTEROP_EXPECT_REQUEST_ID")?,
        trace_parent: required("KC_INTEROP_TRACE_PARENT")?,
        trace_state: required("KC_INTEROP_TRACE_STATE")?,
        baggage: required("KC_INTEROP_BAGGAGE")?,
    };
    let builder = knowledge::KnowledgeServiceClientBuilder::new("knowledge-core.knowledge")
        .caller_name(SERVICE_NAME)
        .address(Address::Ip(address))
        .connect_timeout(Some(request_timeout))
        .read_write_timeout(Some(request_timeout))
        .rpc_timeout(Some(request_timeout));
    let client = if tls.enabled {
        builder.make_transport(client_transport(&tls)?).build()
    } else {
        builder.build()
    };

    let live = scope_liveness(&client, request_timeout, &metadata).await?;
    if live.service.as_str() != "knowledge" || live.status.as_str() != "live" {
        return Err("Go fixture returned an invalid Live response".into());
    }

    let ok = scope_metadata(
        &client,
        common::PingRequest {
            message: Some(FastStr::from_static_str("ok")),
        },
        request_timeout,
        &metadata,
    )
    .await?;
    if ok.service.as_str() != "knowledge" || ok.status.as_str() != "ready" {
        return Err("Go fixture returned an invalid Ping response".into());
    }

    let business = scope_metadata(
        &client,
        common::PingRequest {
            message: Some(FastStr::from_static_str("biz")),
        },
        request_timeout,
        &metadata,
    )
    .await
    .expect_err("Go fixture must return a BizStatus error");
    assert_business_error(&business)?;

    let deadline = duration("KC_INTEROP_DEADLINE_MS", 50)?;
    let delayed = scope_metadata(
        &client,
        common::PingRequest {
            message: Some(FastStr::from_static_str("delay")),
        },
        deadline,
        &metadata,
    )
    .await;
    if delayed.is_ok() || matches!(delayed, Err(ClientError::Biz(_))) {
        return Err("Go fixture did not enforce the Volo client deadline".into());
    }

    println!("CLIENT_OK");
    Ok(())
}

async fn scope_liveness(
    client: &knowledge::KnowledgeServiceClient,
    timeout: Duration,
    values: &FixtureMetadata,
) -> std::result::Result<common::PingResponse, ClientError> {
    let mut option = CallOpt::new();
    option.config.set_rpc_timeout(Some(timeout));
    METAINFO
        .scope(
            RefCell::new(fixture_metainfo(values)),
            client
                .clone()
                .with_callopt(option)
                .live(common::PingRequest { message: None }),
        )
        .await
}

async fn scope_metadata(
    client: &knowledge::KnowledgeServiceClient,
    request: common::PingRequest,
    timeout: Duration,
    values: &FixtureMetadata,
) -> std::result::Result<common::PingResponse, ClientError> {
    let mut option = CallOpt::new();
    option.config.set_rpc_timeout(Some(timeout));
    METAINFO
        .scope(
            RefCell::new(fixture_metainfo(values)),
            client.clone().with_callopt(option).ping(request),
        )
        .await
}

fn fixture_metainfo(values: &FixtureMetadata) -> MetaInfo {
    let mut metadata = MetaInfo::new();
    metadata.set_persistent(ACCESS_TOKEN_KEY, values.token.clone());
    metadata.set_persistent(REQUEST_ID_KEY, values.request_id.clone());
    metadata.set_persistent(TRACE_PARENT_KEY, values.trace_parent.clone());
    metadata.set_persistent(TRACE_STATE_KEY, values.trace_state.clone());
    metadata.set_persistent(BAGGAGE_KEY, values.baggage.clone());
    metadata
}

struct FixtureMetadata {
    token: String,
    request_id: String,
    trace_parent: String,
    trace_state: String,
    baggage: String,
}

fn assert_business_error(error: &ClientError) -> std::result::Result<(), Box<dyn Error>> {
    let ClientError::Biz(error) = error else {
        return Err("Go fixture did not return BizStatus".into());
    };
    if error.status_code != knowledge::CODE_INVALID_INPUT {
        return Err("Go fixture returned the wrong BizStatus code".into());
    }
    let extra = error
        .extra
        .as_ref()
        .ok_or("Go fixture omitted BizStatus extra")?;
    let request_id = env::var("KC_INTEROP_EXPECT_REQUEST_ID")?;
    let trace_id = env::var("KC_INTEROP_EXPECT_TRACE_ID")?;
    let expected = [
        ("error_key", "knowledge.invalid_input"),
        ("error_kind", "invalid_argument"),
        ("request_id", request_id.as_str()),
        ("trace_id", trace_id.as_str()),
    ];
    for (key, value) in expected {
        if extra.get(key).map(FastStr::as_str) != Some(value) {
            return Err(format!("Go fixture BizStatus extra {key} did not match").into());
        }
    }
    Ok(())
}

#[derive(Clone)]
struct FixtureHandler {
    expected_token: String,
    expected_request_id: String,
    expected_trace_id: String,
    expected_trace_state: String,
    expected_baggage: String,
    delay: Duration,
}

impl FixtureHandler {
    fn verify_metadata(&self) -> std::result::Result<(), ServerError> {
        let context = current_request_context().map_err(|error| service_error(&error))?;
        let token = context.access_token.as_ref().map(Secret::expose);
        if context.request_id.as_ref() != self.expected_request_id
            || token != Some(self.expected_token.as_str())
            || current_trace_id().as_deref() != Some(self.expected_trace_id.as_str())
            || current_trace_state().as_deref() != Some(self.expected_trace_state.as_str())
            || current_baggage().as_deref() != Some(self.expected_baggage.as_str())
        {
            return Err(service_error(&ServiceError::invalid_input(
                "interop metadata did not match",
            )));
        }
        Ok(())
    }

    fn unsupported() -> ServerError {
        service_error(&ServiceError::invalid_input(
            "interop method is not supported",
        ))
    }
}

impl collaboration::CollaborationService for FixtureHandler {
    async fn ping(
        &self,
        request: common::PingRequest,
    ) -> std::result::Result<common::PingResponse, ServerError> {
        self.verify_metadata()?;
        match request.message.as_deref() {
            Some("biz") => Err(service_error(&ServiceError::invalid_input(
                "interop business error",
            ))),
            Some("delay") => {
                tokio::time::sleep(self.delay).await;
                Ok(ready_response("collaboration"))
            }
            Some("ok") => Ok(ready_response("collaboration")),
            _ => Err(Self::unsupported()),
        }
    }

    async fn create_session(
        &self,
        _request: collaboration::CreateSessionRequest,
    ) -> std::result::Result<collaboration::CollaborationSession, ServerError> {
        Err(Self::unsupported())
    }

    async fn list_versions(
        &self,
        _request: collaboration::ListVersionsRequest,
    ) -> std::result::Result<collaboration::VersionPage, ServerError> {
        Err(Self::unsupported())
    }

    async fn create_version(
        &self,
        _request: collaboration::CreateVersionRequest,
    ) -> std::result::Result<collaboration::Version, ServerError> {
        Err(Self::unsupported())
    }

    async fn get_version(
        &self,
        _request: collaboration::GetVersionRequest,
    ) -> std::result::Result<collaboration::VersionDetail, ServerError> {
        Err(Self::unsupported())
    }

    async fn restore_version(
        &self,
        _request: collaboration::RestoreVersionRequest,
    ) -> std::result::Result<collaboration::Version, ServerError> {
        Err(Self::unsupported())
    }

    async fn purge_document(
        &self,
        _request: collaboration::PurgeDocumentRequest,
    ) -> std::result::Result<(), ServerError> {
        Err(Self::unsupported())
    }
}

struct Ready;

#[async_trait]
impl RpcReadiness for Ready {
    async fn ready(&self) -> knowledge_core_collaboration::error::Result<()> {
        Ok(())
    }
}

fn ready_response(service: &'static str) -> common::PingResponse {
    common::PingResponse {
        service: FastStr::from_static_str(service),
        status: FastStr::from_static_str("ready"),
        unix_time: time::OffsetDateTime::now_utc().unix_timestamp(),
    }
}

fn tls_config() -> TlsConfig {
    let certificate = optional_path("KC_INTEROP_TLS_CERT_FILE");
    let key = optional_path("KC_INTEROP_TLS_KEY_FILE");
    let ca = optional_path("KC_INTEROP_TLS_CA_FILE");
    let enabled = certificate.is_some() || key.is_some() || ca.is_some();
    TlsConfig {
        enabled,
        ca_file: ca,
        cert_file: certificate,
        key_file: key,
        server_name: env::var("KC_INTEROP_TLS_SERVER_NAME").ok(),
    }
}

fn duration(name: &str, fallback_ms: u64) -> std::result::Result<Duration, Box<dyn Error>> {
    let milliseconds = env::var(name)
        .ok()
        .map_or(Ok(fallback_ms), |value| value.parse::<u64>())?;
    if milliseconds == 0 {
        return Err(format!("{name} must be positive").into());
    }
    Ok(Duration::from_millis(milliseconds))
}

fn required(name: &str) -> std::result::Result<String, Box<dyn Error>> {
    let value = env::var(name)?;
    if value.is_empty() {
        return Err(format!("{name} is required").into());
    }
    Ok(value)
}

fn optional_path(name: &str) -> Option<PathBuf> {
    env::var(name)
        .ok()
        .filter(|value| !value.is_empty())
        .map(PathBuf::from)
}
