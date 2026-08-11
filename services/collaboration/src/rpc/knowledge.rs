use std::{str::FromStr, time::Duration};

use async_trait::async_trait;
use pilota::FastStr;
use serde_json::{Map, Value};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};
use tracing::Instrument as _;
use volo::loadbalance::random::WeightedRandomBalance;
use volo_thrift::client::CallOpt;

use crate::{
    SERVICE_NAME,
    config::KnowledgeConfig,
    domain::{Access, Authorization, DocumentId, Projection, PublicUser, RequestContext},
    error::{ErrorCode, Result, ServiceError},
    generated::{common, knowledge},
    ports::KnowledgePort,
    richtext::validate_rich_text,
};

use super::{
    context::scope_outgoing_metadata, etcd::EtcdDiscovery, knowledge_client_error,
    tls::client_transport,
};

#[derive(Clone)]
pub struct KnowledgeClient {
    client: knowledge::KnowledgeServiceClient,
    request_timeout: Duration,
}

impl KnowledgeClient {
    /// Creates a typed Knowledge RPC client backed by Etcd discovery.
    ///
    /// # Errors
    ///
    /// Returns an error for invalid RPC or TLS configuration.
    pub fn new(config: &KnowledgeConfig, discovery: EtcdDiscovery) -> Result<Self> {
        if config.service_name.trim() != config.service_name
            || config.service_name.is_empty()
            || config.service_name.contains('/')
            || config.request_timeout.is_zero()
        {
            return Err(ServiceError::invalid_input(
                "Knowledge RPC configuration is invalid",
            ));
        }
        let builder = knowledge::KnowledgeServiceClientBuilder::new(&config.service_name)
            .caller_name(SERVICE_NAME)
            .connect_timeout(Some(config.request_timeout))
            .read_write_timeout(Some(config.request_timeout))
            .rpc_timeout(Some(config.request_timeout))
            .load_balance(WeightedRandomBalance::<String>::new())
            .discover(discovery)
            .retry_count(0);
        let client = if config.tls.enabled {
            builder
                .make_transport(client_transport(&config.tls)?)
                .build()
        } else {
            builder.build()
        };
        Ok(Self {
            client,
            request_timeout: config.request_timeout,
        })
    }

    async fn authorize_call(
        &self,
        context: &RequestContext,
        request: knowledge::AuthorizeCollaborationRequest,
    ) -> Result<knowledge::CollaborationAuthorization> {
        let (option, timeout) = call_option(context, self.request_timeout)?;
        let call = self
            .client
            .clone()
            .with_callopt(option)
            .authorize_collaboration(request);
        let span = tracing::info_span!(
            "collaboration.rpc.client",
            rpc.system = "volo_thrift",
            rpc.service = "knowledge",
            rpc.method = "authorize_collaboration",
            request_id = %context.request_id,
        );
        let result = tokio::time::timeout(
            timeout,
            scope_outgoing_metadata(context, call).instrument(span),
        )
        .await
        .map_err(|_| deadline_error())?;
        result.map_err(knowledge_client_error)
    }

    async fn project_call(
        &self,
        context: &RequestContext,
        request: knowledge::ProjectCollaborationRequest,
    ) -> Result<()> {
        let (option, timeout) = call_option(context, self.request_timeout)?;
        let call = self
            .client
            .clone()
            .with_callopt(option)
            .project_collaboration(request);
        let span = tracing::info_span!(
            "collaboration.rpc.client",
            rpc.system = "volo_thrift",
            rpc.service = "knowledge",
            rpc.method = "project_collaboration",
            request_id = %context.request_id,
        );
        let result = tokio::time::timeout(
            timeout,
            scope_outgoing_metadata(context, call).instrument(span),
        )
        .await
        .map_err(|_| deadline_error())?;
        result.map_err(knowledge_client_error)
    }

    async fn live_call(&self, context: &RequestContext) -> Result<common::PingResponse> {
        let (option, timeout) = call_option(context, self.request_timeout)?;
        let call = self
            .client
            .clone()
            .with_callopt(option)
            .live(common::PingRequest { message: None });
        let result = tokio::time::timeout(timeout, scope_outgoing_metadata(context, call))
            .await
            .map_err(|_| deadline_error())?;
        result.map_err(knowledge_client_error)
    }
}

#[async_trait]
impl KnowledgePort for KnowledgeClient {
    async fn authorize(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<Authorization> {
        let response = self
            .authorize_call(
                context,
                knowledge::AuthorizeCollaborationRequest {
                    document_id: FastStr::from_string(document_id.to_string()),
                },
            )
            .await?;
        let response_document =
            DocumentId::parse(response.document_id.as_str()).map_err(|error| {
                ServiceError::internal(
                    anyhow::Error::new(error)
                        .context("Knowledge returned an invalid collaboration document identifier"),
                )
            })?;
        if response_document != document_id || response.permission_revision < 0 {
            return Err(ServiceError::internal(anyhow::anyhow!(
                "Knowledge returned inconsistent collaboration authorization"
            )));
        }
        let actor = PublicUser {
            id: response.actor.id,
            username: response.actor.username.to_string(),
            avatar: response.actor.avatar.to_string(),
        };
        actor.validate().map_err(|error| {
            ServiceError::internal(
                anyhow::Error::new(error)
                    .context("Knowledge returned an invalid collaboration actor"),
            )
        })?;
        let access = Access::from_str(response.access.as_str()).map_err(|error| {
            ServiceError::internal(
                anyhow::Error::new(error)
                    .context("Knowledge returned an invalid collaboration access level"),
            )
        })?;
        let token_expires_at = OffsetDateTime::parse(response.token_expires_at.as_str(), &Rfc3339)
            .map_err(|error| {
                ServiceError::internal(
                    anyhow::Error::new(error)
                        .context("Knowledge returned an invalid token expiration"),
                )
            })?;
        Ok(Authorization {
            document_id,
            actor,
            access,
            permission_revision: response.permission_revision,
            token_expires_at,
        })
    }

    async fn project(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        sequence: i64,
        projection: &Projection,
    ) -> Result<()> {
        if sequence < 0 {
            return Err(ServiceError::invalid_input(
                "projection sequence must not be negative",
            ));
        }
        self.project_call(
            context,
            knowledge::ProjectCollaborationRequest {
                document_id: FastStr::from_string(document_id.to_string()),
                sequence,
                content: projection_to_wire(projection)?,
                plain_text: FastStr::from_string(projection.plain_text.clone()),
            },
        )
        .await
    }

    async fn ping(&self, context: &RequestContext) -> Result<()> {
        let response = self.live_call(context).await?;
        validate_liveness(&response)
    }
}

fn validate_liveness(response: &common::PingResponse) -> Result<()> {
    if response.service.as_str() != "knowledge" || response.status.as_str() != "live" {
        return Err(ServiceError::unavailable(anyhow::anyhow!(
            "Knowledge returned an invalid liveness response"
        )));
    }
    Ok(())
}

fn call_option(context: &RequestContext, maximum: Duration) -> Result<(CallOpt, Duration)> {
    let timeout = context.deadline.map_or(maximum, |deadline| {
        deadline
            .checked_duration_since(std::time::Instant::now())
            .unwrap_or_default()
            .min(maximum)
    });
    if timeout.is_zero() {
        return Err(deadline_error());
    }
    let mut option = CallOpt::new();
    option.config.set_rpc_timeout(Some(timeout));
    Ok((option, timeout))
}

fn deadline_error() -> ServiceError {
    ServiceError::new(
        ErrorCode::Unavailable,
        "collaboration.deadline_exceeded",
        "request deadline exceeded",
    )
}

pub(super) fn projection_to_wire(projection: &Projection) -> Result<knowledge::RichTextDocument> {
    validate_rich_text(&projection.content)?;
    let root = projection
        .content
        .as_object()
        .ok_or_else(invalid_projection)?;
    let content = root
        .get("content")
        .and_then(Value::as_array)
        .ok_or_else(invalid_projection)?
        .iter()
        .map(node_to_wire)
        .collect::<Result<Vec<_>>>()?;
    Ok(knowledge::RichTextDocument {
        r#type: FastStr::from_static_str("doc"),
        content,
    })
}

fn node_to_wire(value: &Value) -> Result<knowledge::RichTextNode> {
    let node = value.as_object().ok_or_else(invalid_projection)?;
    Ok(knowledge::RichTextNode {
        r#type: FastStr::from_string(wire_string(node, "type")?.to_owned()),
        attrs: node.get("attrs").map(attrs_to_wire).transpose()?,
        content: node
            .get("content")
            .map(|value| {
                value
                    .as_array()
                    .ok_or_else(invalid_projection)?
                    .iter()
                    .map(node_to_wire)
                    .collect::<Result<Vec<_>>>()
            })
            .transpose()?,
        text: optional_string(node, "text"),
        marks: node
            .get("marks")
            .map(|value| {
                value
                    .as_array()
                    .ok_or_else(invalid_projection)?
                    .iter()
                    .map(mark_to_wire)
                    .collect::<Result<Vec<_>>>()
            })
            .transpose()?,
    })
}

fn mark_to_wire(value: &Value) -> Result<knowledge::RichTextMark> {
    let mark = value.as_object().ok_or_else(invalid_projection)?;
    Ok(knowledge::RichTextMark {
        r#type: FastStr::from_string(wire_string(mark, "type")?.to_owned()),
        attrs: mark.get("attrs").map(attrs_to_wire).transpose()?,
    })
}

fn attrs_to_wire(value: &Value) -> Result<knowledge::RichTextAttrs> {
    let attrs = value.as_object().ok_or_else(invalid_projection)?;
    Ok(knowledge::RichTextAttrs {
        level: optional_i32(attrs, "level")?,
        start: optional_i32(attrs, "start")?,
        checked: attrs.get("checked").and_then(Value::as_bool),
        language: optional_string(attrs, "language"),
        href: optional_string(attrs, "href"),
        attachment_id: optional_string(attrs, "attachmentId"),
        alt: optional_string(attrs, "alt"),
        title: optional_string(attrs, "title"),
        text_align: optional_string(attrs, "textAlign"),
        colspan: optional_i32(attrs, "colspan")?,
        rowspan: optional_i32(attrs, "rowspan")?,
        colwidth: attrs
            .get("colwidth")
            .filter(|value| !value.is_null())
            .map(|value| {
                value
                    .as_array()
                    .ok_or_else(invalid_projection)?
                    .iter()
                    .map(|value| {
                        value
                            .as_i64()
                            .ok_or_else(invalid_projection)
                            .and_then(|value| {
                                i32::try_from(value).map_err(|_| invalid_projection())
                            })
                    })
                    .collect::<Result<Vec<_>>>()
            })
            .transpose()?,
    })
}

fn wire_string<'a>(object: &'a Map<String, Value>, name: &str) -> Result<&'a str> {
    object
        .get(name)
        .and_then(Value::as_str)
        .ok_or_else(invalid_projection)
}

fn optional_string(object: &Map<String, Value>, name: &str) -> Option<FastStr> {
    object
        .get(name)
        .and_then(Value::as_str)
        .map(|value| FastStr::from_string(value.to_owned()))
}

fn optional_i32(object: &Map<String, Value>, name: &str) -> Result<Option<i32>> {
    object
        .get(name)
        .map(|value| {
            value
                .as_i64()
                .ok_or_else(invalid_projection)
                .and_then(|value| i32::try_from(value).map_err(|_| invalid_projection()))
        })
        .transpose()
}

fn invalid_projection() -> ServiceError {
    ServiceError::invalid_input("projection content is invalid")
}

#[cfg(test)]
mod tests {
    use std::time::{Duration, Instant};

    use pilota::FastStr;

    use crate::{
        domain::{Projection, RequestContext},
        error::ErrorCode,
        generated::common,
    };

    use super::{call_option, projection_to_wire, validate_liveness};

    #[test]
    fn call_timeout_uses_remaining_deadline() {
        let mut context = RequestContext::new("request-123");
        context.deadline = Some(Instant::now() + Duration::from_millis(50));
        let (_, timeout) = call_option(&context, Duration::from_secs(5)).expect("call option");
        assert!(timeout <= Duration::from_millis(50));
        assert!(timeout > Duration::ZERO);
    }

    #[test]
    fn expired_deadline_fails_before_the_rpc() {
        let mut context = RequestContext::new("request-123");
        context.deadline = Some(
            Instant::now()
                .checked_sub(Duration::from_millis(1))
                .expect("test deadline"),
        );
        let error = call_option(&context, Duration::from_secs(5)).expect_err("expired deadline");
        assert_eq!(error.key(), "collaboration.deadline_exceeded");
    }

    #[test]
    fn liveness_response_requires_exact_service_and_status() {
        let valid = common::PingResponse {
            service: FastStr::from_static_str("knowledge"),
            status: FastStr::from_static_str("live"),
            unix_time: 1,
        };
        validate_liveness(&valid).expect("valid liveness response");

        for (service, status) in [
            ("knowledge", "ready"),
            ("knowledge", "not_ready"),
            ("collaboration", "live"),
        ] {
            let response = common::PingResponse {
                service: FastStr::from_static_str(service),
                status: FastStr::from_static_str(status),
                unix_time: 1,
            };
            let error = validate_liveness(&response).expect_err("invalid liveness response");
            assert_eq!(error.code(), ErrorCode::Unavailable);
        }
    }

    #[test]
    fn projection_maps_to_typed_knowledge_contract() {
        let projection = Projection {
            content: serde_json::json!({
                "type": "doc",
                "content": [{
                    "type": "paragraph",
                    "content": [{
                        "type": "text",
                        "text": "hello",
                        "marks": [{"type": "bold"}]
                    }]
                }]
            }),
            plain_text: "hello".to_owned(),
        };
        let wire = projection_to_wire(&projection).expect("wire projection");
        assert_eq!(wire.r#type.as_str(), "doc");
        assert_eq!(wire.content[0].r#type.as_str(), "paragraph");
        assert_eq!(
            wire.content[0].content.as_ref().expect("children")[0]
                .text
                .as_ref()
                .expect("text")
                .as_str(),
            "hello"
        );
    }
}
