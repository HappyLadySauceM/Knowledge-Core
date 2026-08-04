use std::{fmt, io, net::SocketAddr, path::Path, sync::Arc, time::Duration};

use rustls::{
    ClientConfig, RootCertStore, ServerConfig,
    pki_types::{CertificateDer, PrivateKeyDer, pem::PemObject},
    server::WebPkiClientVerifier,
};
use tokio::net::TcpListener;
use tokio_rustls::TlsAcceptor;
use tokio_util::sync::CancellationToken;
use volo::net::{
    conn::Conn,
    dial::Config as DialConfig,
    incoming::{Incoming, MakeIncoming},
    tls::{ClientTlsConfig, TlsConnector, TlsMakeTransport, TlsStream},
};

use crate::{
    config::TlsConfig,
    error::{Result, ServiceError},
};

pub struct RpcIncoming {
    listener: TcpListener,
    acceptor: Option<TlsAcceptor>,
    handshake_timeout: Duration,
    shutdown: CancellationToken,
}

impl RpcIncoming {
    /// Binds the RPC listener and prepares optional TLS termination.
    ///
    /// # Errors
    ///
    /// Returns an error when the listener cannot bind or TLS configuration is invalid.
    pub async fn bind(
        address: SocketAddr,
        tls: &TlsConfig,
        handshake_timeout: Duration,
        shutdown: CancellationToken,
    ) -> Result<Self> {
        install_crypto_provider();
        if handshake_timeout.is_zero() {
            return Err(ServiceError::invalid_input(
                "RPC TLS handshake timeout must be greater than zero",
            ));
        }
        let acceptor = server_acceptor(tls)?;
        let listener = TcpListener::bind(address).await.map_err(|error| {
            ServiceError::unavailable(anyhow::Error::new(error).context("bind RPC listener"))
        })?;
        Ok(Self {
            listener,
            acceptor,
            handshake_timeout,
            shutdown,
        })
    }

    /// Returns the socket address of the bound listener.
    ///
    /// # Errors
    ///
    /// Returns an error when the operating system cannot report the address.
    pub fn local_addr(&self) -> Result<SocketAddr> {
        self.listener.local_addr().map_err(|error| {
            ServiceError::internal(anyhow::Error::new(error).context("read RPC listener address"))
        })
    }

    pub(crate) fn shutdown_token(&self) -> CancellationToken {
        self.shutdown.clone()
    }
}

impl fmt::Debug for RpcIncoming {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("RpcIncoming")
            .field("local_addr", &self.listener.local_addr().ok())
            .field("tls", &self.acceptor.is_some())
            .finish_non_exhaustive()
    }
}

impl MakeIncoming for RpcIncoming {
    type Incoming = Self;

    async fn make_incoming(self) -> io::Result<Self::Incoming> {
        Ok(self)
    }
}

impl Incoming for RpcIncoming {
    async fn accept(&mut self) -> io::Result<Option<Conn>> {
        loop {
            let accepted = tokio::select! {
                () = self.shutdown.cancelled() => return Ok(None),
                accepted = self.listener.accept() => accepted,
            };
            let (stream, _) = accepted?;
            let Some(acceptor) = &self.acceptor else {
                return Ok(Some(Conn::from(stream)));
            };
            let handshake = tokio::time::timeout(self.handshake_timeout, acceptor.accept(stream));
            let result = tokio::select! {
                () = self.shutdown.cancelled() => return Ok(None),
                result = handshake => result,
            };
            match result {
                Ok(Ok(stream)) => {
                    let stream = tokio_rustls::TlsStream::Server(stream);
                    return Ok(Some(Conn::from(TlsStream::from(stream))));
                }
                Ok(Err(error)) => {
                    tracing::warn!(error.type = %std::any::type_name_of_val(&error), "rejected RPC TLS handshake");
                }
                Err(_) => {
                    tracing::warn!("RPC TLS handshake timed out");
                }
            }
        }
    }
}

/// Creates a Volo transport that verifies the server and optionally presents a client identity.
///
/// # Errors
///
/// Returns an error when TLS is disabled, incomplete, or contains invalid PEM material.
pub fn client_transport(tls: &TlsConfig) -> Result<TlsMakeTransport> {
    install_crypto_provider();
    if !tls.enabled {
        return Err(ServiceError::invalid_input(
            "RPC client TLS transport requires TLS to be enabled",
        ));
    }
    let ca_file = required_path(tls.ca_file.as_deref(), "RPC client CA")?;
    let server_name = tls
        .server_name
        .as_deref()
        .ok_or_else(|| ServiceError::invalid_input("RPC client TLS server name is required"))?;
    let roots = root_store(ca_file, "RPC client CA")?;
    let builder =
        ClientConfig::builder_with_provider(Arc::new(rustls::crypto::ring::default_provider()))
            .with_safe_default_protocol_versions()
            .map_err(|error| {
                tls_configuration_error("select RPC client TLS protocol versions", error)
            })?
            .with_root_certificates(roots);
    let config = match (tls.cert_file.as_deref(), tls.key_file.as_deref()) {
        (Some(certificate), Some(key)) => builder
            .with_client_auth_cert(
                certificates(certificate, "RPC client certificate")?,
                private_key(key, "RPC client private key")?,
            )
            .map_err(|error| tls_configuration_error("configure RPC client identity", error))?,
        (None, None) => builder.with_no_client_auth(),
        _ => {
            return Err(ServiceError::invalid_input(
                "RPC client certificate and private key must be configured together",
            ));
        }
    };
    let connector = TlsConnector::from(config);
    Ok(TlsMakeTransport::new(
        DialConfig::default(),
        ClientTlsConfig::new(server_name, connector),
    ))
}

/// Selects the `ring` `rustls` provider for the process before any TLS configuration is built.
///
/// Calling this more than once is safe. Applications should call it before constructing any
/// `PostgreSQL`, `Redis`, `NATS`, `Etcd`, `OTLP`, or RPC TLS client because the dependency graph
/// enables more than one `rustls` provider feature.
pub fn install_crypto_provider() {
    if rustls::crypto::CryptoProvider::get_default().is_none() {
        let _ = rustls::crypto::ring::default_provider().install_default();
    }
}

fn server_acceptor(tls: &TlsConfig) -> Result<Option<TlsAcceptor>> {
    if !tls.enabled {
        return Ok(None);
    }
    let certificate = required_path(tls.cert_file.as_deref(), "RPC server certificate")?;
    let key = required_path(tls.key_file.as_deref(), "RPC server private key")?;
    let builder =
        ServerConfig::builder_with_provider(Arc::new(rustls::crypto::ring::default_provider()))
            .with_safe_default_protocol_versions()
            .map_err(|error| {
                tls_configuration_error("select RPC server TLS protocol versions", error)
            })?;
    let builder = if let Some(ca_file) = tls.ca_file.as_deref() {
        let verifier =
            WebPkiClientVerifier::builder(Arc::new(root_store(ca_file, "RPC client CA")?))
                .build()
                .map_err(|error| {
                    tls_configuration_error("configure RPC client verification", error)
                })?;
        builder.with_client_cert_verifier(verifier)
    } else {
        builder.with_no_client_auth()
    };
    let config = builder
        .with_single_cert(
            certificates(certificate, "RPC server certificate")?,
            private_key(key, "RPC server private key")?,
        )
        .map_err(|error| tls_configuration_error("configure RPC server identity", error))?;
    Ok(Some(TlsAcceptor::from(Arc::new(config))))
}

pub(crate) fn read_pem(path: &Path, description: &'static str) -> Result<Vec<u8>> {
    std::fs::read(path).map_err(|error| {
        ServiceError::invalid_input(format!("{description} cannot be read")).with_source(error)
    })
}

fn certificates(path: &Path, description: &'static str) -> Result<Vec<CertificateDer<'static>>> {
    let pem = read_pem(path, description)?;
    let certificates = CertificateDer::pem_slice_iter(&pem)
        .collect::<std::result::Result<Vec<_>, _>>()
        .map_err(|error| {
            ServiceError::invalid_input(format!("{description} is invalid")).with_source(error)
        })?;
    if certificates.is_empty() {
        return Err(ServiceError::invalid_input(format!(
            "{description} contains no certificates"
        )));
    }
    Ok(certificates)
}

fn private_key(path: &Path, description: &'static str) -> Result<PrivateKeyDer<'static>> {
    let pem = read_pem(path, description)?;
    PrivateKeyDer::from_pem_slice(&pem).map_err(|error| match error {
        rustls::pki_types::pem::Error::NoItemsFound => {
            ServiceError::invalid_input(format!("{description} contains no key"))
        }
        error => {
            ServiceError::invalid_input(format!("{description} is invalid")).with_source(error)
        }
    })
}

fn root_store(path: &Path, description: &'static str) -> Result<RootCertStore> {
    let mut roots = RootCertStore::empty();
    for certificate in certificates(path, description)? {
        roots
            .add(certificate)
            .map_err(|error| tls_configuration_error("add RPC certificate authority", error))?;
    }
    Ok(roots)
}

fn required_path<'a>(path: Option<&'a Path>, description: &'static str) -> Result<&'a Path> {
    path.ok_or_else(|| ServiceError::invalid_input(format!("{description} is required")))
}

fn tls_configuration_error(
    operation: &'static str,
    error: impl Into<anyhow::Error>,
) -> ServiceError {
    ServiceError::invalid_input(operation).with_source(error)
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use tokio_util::sync::CancellationToken;

    use crate::config::TlsConfig;

    use super::{RpcIncoming, install_crypto_provider};

    #[test]
    fn ring_crypto_provider_is_installed_explicitly() {
        install_crypto_provider();
        assert!(rustls::crypto::CryptoProvider::get_default().is_some());
    }

    #[tokio::test]
    async fn cancellation_stops_plain_incoming() {
        let shutdown = CancellationToken::new();
        let mut incoming = RpcIncoming::bind(
            "127.0.0.1:0".parse().expect("address"),
            &TlsConfig::default(),
            Duration::from_secs(1),
            shutdown.clone(),
        )
        .await
        .expect("bind incoming");
        shutdown.cancel();
        assert!(
            volo::net::incoming::Incoming::accept(&mut incoming)
                .await
                .expect("accept")
                .is_none()
        );
    }
}
