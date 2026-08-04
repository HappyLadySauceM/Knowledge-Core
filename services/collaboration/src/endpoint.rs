use std::{
    future::Future,
    io,
    net::{IpAddr, SocketAddr},
    time::Duration,
};

use thiserror::Error;
use url::{Host, Url};

#[derive(Debug, Error)]
pub enum EndpointResolutionError {
    #[error("endpoint must be host:port with a non-zero port")]
    InvalidEndpoint,
    #[error("endpoint resolution timeout must be positive")]
    InvalidTimeout,
    #[error("endpoint DNS resolution timed out")]
    Timeout,
    #[error("endpoint DNS resolution failed")]
    Lookup(#[source] io::Error),
    #[error("endpoint DNS resolution returned no addresses")]
    NoAddresses,
}

enum EndpointHost {
    Ip(IpAddr),
    Domain(String),
}

/// Validates the canonical `host:port` syntax accepted by outbound TCP clients.
///
/// # Errors
///
/// Returns an error for whitespace, credentials, paths, query strings, fragments, missing hosts,
/// missing ports, zero ports, or unbracketed IPv6 literals.
pub fn validate_socket_endpoint(endpoint: &str) -> Result<(), EndpointResolutionError> {
    parse_socket_endpoint(endpoint).map(drop)
}

/// Resolves a strict `host:port` endpoint to a stable, non-empty set of socket addresses.
///
/// IP literals do not perform DNS I/O. Hostname lookup is bounded by `timeout`; returned addresses
/// are normalized to the configured port, sorted, and deduplicated.
///
/// # Errors
///
/// Returns an error when the endpoint or timeout is invalid, DNS lookup fails or times out, or the
/// resolver returns no addresses.
pub async fn resolve_socket_addresses(
    endpoint: &str,
    timeout: Duration,
) -> Result<Vec<SocketAddr>, EndpointResolutionError> {
    resolve_socket_addresses_with(endpoint, timeout, |host, port| async move {
        tokio::net::lookup_host((host.as_str(), port))
            .await
            .map(Iterator::collect)
    })
    .await
}

async fn resolve_socket_addresses_with<F, Fut>(
    endpoint: &str,
    timeout: Duration,
    lookup: F,
) -> Result<Vec<SocketAddr>, EndpointResolutionError>
where
    F: FnOnce(String, u16) -> Fut,
    Fut: Future<Output = io::Result<Vec<SocketAddr>>>,
{
    if timeout.is_zero() {
        return Err(EndpointResolutionError::InvalidTimeout);
    }
    let (host, port) = parse_socket_endpoint(endpoint)?;
    match host {
        EndpointHost::Ip(address) => Ok(vec![SocketAddr::new(address, port)]),
        EndpointHost::Domain(host) => {
            let addresses = tokio::time::timeout(timeout, lookup(host, port))
                .await
                .map_err(|_| EndpointResolutionError::Timeout)?
                .map_err(EndpointResolutionError::Lookup)?;
            normalize_addresses(addresses, port)
        }
    }
}

fn parse_socket_endpoint(endpoint: &str) -> Result<(EndpointHost, u16), EndpointResolutionError> {
    if endpoint.is_empty() || endpoint.trim() != endpoint || endpoint.contains('@') {
        return Err(EndpointResolutionError::InvalidEndpoint);
    }
    let parsed = Url::parse(&format!("tcp://{endpoint}"))
        .map_err(|_| EndpointResolutionError::InvalidEndpoint)?;
    if parsed.scheme() != "tcp"
        || !parsed.username().is_empty()
        || parsed.password().is_some()
        || !parsed.path().is_empty()
        || parsed.query().is_some()
        || parsed.fragment().is_some()
    {
        return Err(EndpointResolutionError::InvalidEndpoint);
    }
    let port = parsed
        .port()
        .filter(|port| *port != 0)
        .ok_or(EndpointResolutionError::InvalidEndpoint)?;
    let host = match parsed
        .host()
        .ok_or(EndpointResolutionError::InvalidEndpoint)?
    {
        Host::Ipv4(address) => EndpointHost::Ip(IpAddr::V4(address)),
        Host::Ipv6(address) => EndpointHost::Ip(IpAddr::V6(address)),
        Host::Domain(domain) if !domain.is_empty() => EndpointHost::Domain(domain.to_owned()),
        Host::Domain(_) => return Err(EndpointResolutionError::InvalidEndpoint),
    };
    Ok((host, port))
}

fn normalize_addresses(
    addresses: Vec<SocketAddr>,
    port: u16,
) -> Result<Vec<SocketAddr>, EndpointResolutionError> {
    let mut addresses = addresses
        .into_iter()
        .map(|address| SocketAddr::new(address.ip(), port))
        .collect::<Vec<_>>();
    addresses.sort_unstable();
    addresses.dedup();
    if addresses.is_empty() {
        return Err(EndpointResolutionError::NoAddresses);
    }
    Ok(addresses)
}

#[cfg(test)]
mod tests {
    use std::{future::pending, io, net::SocketAddr, time::Duration};

    use super::{EndpointResolutionError, resolve_socket_addresses_with};

    #[tokio::test]
    async fn ip_endpoint_does_not_require_dns() {
        let addresses = resolve_socket_addresses_with(
            "[2001:db8::1]:8443",
            Duration::from_secs(1),
            |_, _| async { panic!("IP literals must not invoke DNS") },
        )
        .await
        .expect("resolve IP literal");

        assert_eq!(
            addresses,
            vec!["[2001:db8::1]:8443".parse().expect("socket address")]
        );
    }

    #[tokio::test]
    async fn hostname_addresses_are_normalized_sorted_and_deduplicated() {
        let addresses = resolve_socket_addresses_with(
            "knowledge.internal:8443",
            Duration::from_secs(1),
            |host, port| async move {
                assert_eq!(host, "knowledge.internal");
                assert_eq!(port, 8443);
                Ok(vec![
                    "[::1]:1".parse().expect("IPv6 address"),
                    "127.0.0.2:2".parse().expect("IPv4 address"),
                    "127.0.0.2:3".parse().expect("duplicate IPv4 address"),
                ])
            },
        )
        .await
        .expect("resolve hostname");

        assert_eq!(
            addresses,
            vec![
                "127.0.0.2:8443".parse().expect("IPv4 address"),
                "[::1]:8443".parse().expect("IPv6 address"),
            ]
        );
    }

    #[tokio::test]
    async fn invalid_endpoints_are_rejected_before_dns() {
        for endpoint in [
            "",
            " knowledge:8443",
            "knowledge:8443 ",
            "knowledge",
            "knowledge:0",
            "http://knowledge:8443",
            "knowledge:8443/path",
            "user@knowledge:8443",
            "knowledge:8443?zone=internal",
            "knowledge:8443#fragment",
            "2001:db8::1:8443",
        ] {
            let result =
                resolve_socket_addresses_with(endpoint, Duration::from_secs(1), |_, _| async {
                    panic!("invalid endpoints must not invoke DNS")
                })
                .await;
            assert!(
                matches!(result, Err(EndpointResolutionError::InvalidEndpoint)),
                "{endpoint}"
            );
        }
    }

    #[tokio::test]
    async fn empty_dns_result_is_rejected() {
        let result = resolve_socket_addresses_with(
            "knowledge.internal:8443",
            Duration::from_secs(1),
            |_, _| async { Ok(Vec::new()) },
        )
        .await;

        assert!(matches!(result, Err(EndpointResolutionError::NoAddresses)));
    }

    #[tokio::test]
    async fn dns_lookup_failure_preserves_the_source() {
        let result = resolve_socket_addresses_with(
            "knowledge.internal:8443",
            Duration::from_secs(1),
            |_, _| async { Err(io::Error::other("fixture lookup failure")) },
        )
        .await;

        assert!(matches!(result, Err(EndpointResolutionError::Lookup(_))));
    }

    #[tokio::test]
    async fn dns_lookup_is_bounded() {
        let result = resolve_socket_addresses_with(
            "knowledge.internal:8443",
            Duration::from_nanos(1),
            |_, _| pending::<io::Result<Vec<SocketAddr>>>(),
        )
        .await;

        assert!(matches!(result, Err(EndpointResolutionError::Timeout)));
    }

    #[tokio::test]
    async fn zero_timeout_is_rejected_before_dns() {
        let result = resolve_socket_addresses_with(
            "knowledge.internal:8443",
            Duration::ZERO,
            |_, _| async { panic!("an invalid timeout must not invoke DNS") },
        )
        .await;

        assert!(matches!(
            result,
            Err(EndpointResolutionError::InvalidTimeout)
        ));
    }
}
