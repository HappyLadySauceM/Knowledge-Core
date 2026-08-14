//! Static `host:port` discovery for outbound Knowledge RPC.
//! 出站 Knowledge RPC 使用静态 `host:port` 发现。

use std::{collections::HashMap, io, sync::Arc, time::Duration};

use async_broadcast::Receiver;
use volo::{
    context::Endpoint,
    discovery::{Change, Discover, Instance},
    loadbalance::error::LoadBalanceError,
    net::Address,
};

use crate::{
    config::KnowledgeConfig,
    endpoint::{resolve_socket_addresses, validate_socket_endpoint},
    error::{Result, ServiceError},
};

const INSTANCE_WEIGHT: u32 = 10;

/// Resolves a configured Knowledge RPC `host:port` on each discover call.
/// 每次 discover 时解析已配置的 Knowledge RPC `host:port`。
#[derive(Clone)]
pub struct StaticDiscover {
    service_name: String,
    address: String,
    timeout: Duration,
}

impl StaticDiscover {
    /// Builds a fail-closed discoverer from Knowledge client configuration.
    /// 从 Knowledge 客户端配置构造 fail-closed 发现器。
    ///
    /// # Errors
    ///
    /// Returns an error when the service name, address, or timeout is invalid.
    pub fn new(config: &KnowledgeConfig) -> Result<Self> {
        if config.service_name.trim() != config.service_name
            || config.service_name.is_empty()
            || config.service_name.contains('/')
            || config.request_timeout.is_zero()
        {
            return Err(ServiceError::invalid_input(
                "Knowledge RPC configuration is invalid",
            ));
        }
        validate_socket_endpoint(&config.address).map_err(|error| {
            ServiceError::invalid_input("Knowledge RPC address must be host:port")
                .with_source(error)
        })?;
        Ok(Self {
            service_name: config.service_name.clone(),
            address: config.address.clone(),
            timeout: config.request_timeout,
        })
    }
}

impl Discover for StaticDiscover {
    type Key = String;
    type Error = LoadBalanceError;

    async fn discover<'s>(
        &'s self,
        endpoint: &'s Endpoint,
    ) -> std::result::Result<Vec<Arc<Instance>>, Self::Error> {
        if endpoint.service_name_ref() != self.service_name {
            return Err(discovery_error(
                "static discovery service name does not match",
            ));
        }
        let addresses = resolve_socket_addresses(&self.address, self.timeout)
            .await
            .map_err(|error| discovery_error(&error.to_string()))?;
        if addresses.is_empty() {
            return Err(discovery_error("static discovery returned no instances"));
        }
        Ok(addresses
            .into_iter()
            .map(|address| {
                Arc::new(Instance {
                    address: Address::Ip(address),
                    weight: INSTANCE_WEIGHT,
                    tags: HashMap::new(),
                })
            })
            .collect())
    }

    fn key(&self, endpoint: &Endpoint) -> Self::Key {
        endpoint.service_name_ref().to_owned()
    }

    fn watch(&self, _keys: Option<&[Self::Key]>) -> Option<Receiver<Change<Self::Key>>> {
        None
    }
}

fn discovery_error(message: &str) -> LoadBalanceError {
    LoadBalanceError::Discover(Box::new(io::Error::other(message.to_owned())))
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use pilota::FastStr;
    use volo::{context::Endpoint, discovery::Discover};

    use crate::config::{KnowledgeConfig, TlsConfig};

    use super::StaticDiscover;

    fn config(address: &str) -> KnowledgeConfig {
        KnowledgeConfig {
            service_name: "knowledge-core.knowledge".to_owned(),
            address: address.to_owned(),
            request_timeout: Duration::from_secs(1),
            tls: TlsConfig {
                enabled: false,
                ca_file: None,
                cert_file: None,
                key_file: None,
                server_name: None,
            },
        }
    }

    fn endpoint(service_name: &str) -> Endpoint {
        Endpoint::new(FastStr::from_string(service_name.to_owned()))
    }

    #[test]
    fn valid_host_port_is_accepted() {
        for address in ["127.0.0.1:8882", "knowledge:8882", "[::1]:8882"] {
            StaticDiscover::new(&config(address)).unwrap_or_else(|_| panic!("{address}"));
        }
    }

    #[test]
    fn invalid_address_fails_closed() {
        for address in [
            "knowledge",
            "knowledge:0",
            "127.0.0.1:8882/path",
            "user@127.0.0.1:8882",
            "127.0.0.1:8882?zone=internal",
        ] {
            assert!(StaticDiscover::new(&config(address)).is_err(), "{address}");
        }
    }

    #[tokio::test]
    async fn valid_host_port_discovers_resolved_instances() {
        let discover = StaticDiscover::new(&config("127.0.0.1:8882")).expect("valid address");
        let instances = discover
            .discover(&endpoint("knowledge-core.knowledge"))
            .await
            .expect("discover");
        assert_eq!(instances.len(), 1);
        assert_eq!(instances[0].address.to_string(), "127.0.0.1:8882");
        assert!(discover.watch(None).is_none());
    }

    #[tokio::test]
    async fn dns_failure_fails_closed() {
        let discover = StaticDiscover::new(&config("this-host-does-not-exist.invalid:8882"))
            .expect("syntax is valid");
        assert!(
            discover
                .discover(&endpoint("knowledge-core.knowledge"))
                .await
                .is_err()
        );
    }
}
