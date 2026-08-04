use std::{borrow::Cow, collections::HashMap, sync::Arc, time::Duration};

use async_broadcast::{InactiveReceiver, Receiver, Sender};
use etcd_client::{
    Certificate, Client as RawClient, ConnectOptions, GetOptions, Identity, PutOptions, TlsOptions,
    WatchOptions,
};
use futures_util::{StreamExt, TryStreamExt};
use serde::{Deserialize, Serialize};
use tokio::sync::{Mutex, RwLock};
use tokio_util::sync::CancellationToken;
use volo::{
    context::Endpoint,
    discovery::{Change, Discover, Instance},
    loadbalance::error::LoadBalanceError,
    net::Address,
};

use crate::{
    config::{EtcdConfig, TlsConfig},
    endpoint::{resolve_socket_addresses, validate_socket_endpoint},
    error::{Result, ServiceError},
};

const DNS_RESOLUTION_CONCURRENCY: usize = 16;

#[derive(Clone)]
pub struct EtcdClient {
    client: RawClient,
    config: EtcdConfig,
}

impl EtcdClient {
    /// Connects to Etcd and verifies a bounded status request.
    ///
    /// # Errors
    ///
    /// Returns an error for invalid configuration, connection failure, or an unhealthy cluster.
    pub async fn connect(config: &EtcdConfig) -> Result<Self> {
        validate_config(config)?;
        let mut options = ConnectOptions::new()
            .with_connect_timeout(config.connect_timeout)
            .with_timeout(config.request_timeout)
            .with_require_leader(true);
        if let (Some(username), Some(password)) = (&config.username, &config.password) {
            options = options.with_user(username, password);
        }
        if config.tls.enabled {
            options = options.with_tls(etcd_tls(&config.tls)?);
        }
        let client = tokio::time::timeout(
            config.connect_timeout,
            RawClient::connect(&config.endpoints, Some(options)),
        )
        .await
        .map_err(|_| ServiceError::unavailable(anyhow::anyhow!("Etcd connection timed out")))?
        .map_err(|error| {
            ServiceError::unavailable(anyhow::Error::new(error).context("connect to Etcd"))
        })?;
        let result = Self {
            client,
            config: config.clone(),
        };
        result.ping().await?;
        Ok(result)
    }

    /// Checks Etcd health with the configured request timeout.
    ///
    /// # Errors
    ///
    /// Returns an error when Etcd cannot answer before the deadline.
    pub async fn ping(&self) -> Result<()> {
        let mut client = self.client.clone();
        request(
            self.config.request_timeout,
            client.status(),
            "query Etcd status",
        )
        .await?;
        Ok(())
    }

    /// Registers a service instance under a renewable Etcd lease.
    ///
    /// # Errors
    ///
    /// Returns an error when the record is invalid or registration cannot complete atomically.
    pub async fn register(
        &self,
        service_name: &str,
        advertised_address: &str,
        tags: HashMap<String, String>,
        shutdown: CancellationToken,
    ) -> Result<EtcdRegistration> {
        let shutdown = shutdown.child_token();
        EtcdRegistration::start(
            self.client.clone(),
            &self.config,
            service_name,
            advertised_address,
            tags,
            shutdown,
        )
        .await
    }

    /// Starts a watched, fail-closed service discovery snapshot.
    ///
    /// # Errors
    ///
    /// Returns an error when no valid instance exists or the watch cannot be established.
    pub async fn discover(
        &self,
        service_name: &str,
        shutdown: CancellationToken,
    ) -> Result<EtcdDiscovery> {
        let shutdown = shutdown.child_token();
        EtcdDiscovery::start(self.client.clone(), &self.config, service_name, shutdown).await
    }
}

pub struct EtcdRegistration {
    client: RawClient,
    request_timeout: Duration,
    lease_id: i64,
    health: Arc<RwLock<Option<Arc<str>>>>,
    shutdown: CancellationToken,
    task: Mutex<Option<tokio::task::JoinHandle<()>>>,
    closed: Mutex<bool>,
}

impl EtcdRegistration {
    #[allow(clippy::too_many_lines)]
    async fn start(
        mut client: RawClient,
        config: &EtcdConfig,
        service_name: &str,
        advertised_address: &str,
        tags: HashMap<String, String>,
        shutdown: CancellationToken,
    ) -> Result<Self> {
        if shutdown.is_cancelled() {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "Etcd registration startup was canceled"
            )));
        }
        validate_service_name(service_name)?;
        validate_advertised_address(advertised_address)?;
        let prefix = config.prefix.trim_end_matches('/');
        let key = format!("{prefix}/{service_name}/{advertised_address}");
        let value = serde_json::to_string(&InstanceRecord {
            network: "tcp".to_owned(),
            address: advertised_address.to_owned(),
            weight: 10,
            tags: Some(tags),
        })
        .map_err(|error| {
            ServiceError::internal(anyhow::Error::new(error).context("encode Etcd registration"))
        })?;
        let ttl = i64::try_from(config.lease_ttl.as_secs()).map_err(|error| {
            ServiceError::invalid_input("Etcd lease TTL is too large").with_source(error)
        })?;
        let lease = request(
            config.request_timeout,
            client.lease_grant(ttl, None),
            "grant Etcd registration lease",
        )
        .await?;
        let lease_id = lease.id();
        if let Err(error) = request(
            config.request_timeout,
            client.put(
                key.clone(),
                value.clone(),
                Some(PutOptions::new().with_lease(lease_id)),
            ),
            "write Etcd registration",
        )
        .await
        {
            let _ = request(
                config.request_timeout,
                client.lease_revoke(lease_id),
                "revoke failed Etcd registration lease",
            )
            .await;
            return Err(error);
        }
        let keep_alive = request(
            config.request_timeout,
            client.lease_keep_alive(lease_id),
            "start Etcd registration keepalive",
        )
        .await;
        let (mut keeper, mut stream) = match keep_alive {
            Ok(value) => value,
            Err(error) => {
                let _ = request(
                    config.request_timeout,
                    client.lease_revoke(lease_id),
                    "revoke failed Etcd registration lease",
                )
                .await;
                return Err(error);
            }
        };
        let health = Arc::new(RwLock::new(None));
        let task_health = health.clone();
        let task_shutdown = shutdown.child_token();
        let request_timeout = config.request_timeout;
        let mut verification_client = client.clone();
        let verification_key = key.clone();
        let interval_seconds = u64::try_from((ttl / 3).max(1)).map_err(|error| {
            ServiceError::invalid_input("Etcd keepalive interval is invalid").with_source(error)
        })?;
        let interval = Duration::from_secs(interval_seconds);
        let task = tokio::spawn(async move {
            loop {
                tokio::select! {
                    () = task_shutdown.cancelled() => break,
                    () = tokio::time::sleep(interval) => {}
                }
                let sent = tokio::time::timeout(request_timeout, keeper.keep_alive()).await;
                if !matches!(sent, Ok(Ok(()))) {
                    set_health_error(&task_health, "Etcd registration keepalive request failed")
                        .await;
                    break;
                }
                let response = tokio::time::timeout(request_timeout, stream.message()).await;
                match response {
                    Ok(Ok(Some(response))) if response.id() == lease_id && response.ttl() > 0 => {}
                    _ => {
                        set_health_error(&task_health, "Etcd registration keepalive stopped").await;
                        break;
                    }
                }
                match registration_is_owned(
                    &mut verification_client,
                    &verification_key,
                    value.as_bytes(),
                    lease_id,
                    request_timeout,
                )
                .await
                {
                    Ok(true) => {}
                    Ok(false) => {
                        set_health_error(&task_health, "Etcd registration ownership was lost")
                            .await;
                        break;
                    }
                    Err(_) => {
                        set_health_error(&task_health, "Etcd registration ownership check failed")
                            .await;
                        break;
                    }
                }
            }
        });
        Ok(Self {
            client,
            request_timeout: config.request_timeout,
            lease_id,
            health,
            shutdown,
            task: Mutex::new(Some(task)),
            closed: Mutex::new(false),
        })
    }

    /// Reports whether the lease is renewed and the registration key remains owned by this instance.
    ///
    /// # Errors
    ///
    /// Returns an error after keepalive failure, ownership loss, or shutdown.
    pub async fn ready(&self) -> Result<()> {
        if *self.closed.lock().await {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "Etcd registration is stopped"
            )));
        }
        health_result(&self.health, "Etcd registration").await
    }

    /// Stops keepalive and revokes the lease, which removes keys still owned by this instance.
    ///
    /// # Errors
    ///
    /// Returns an error when task shutdown or Etcd cleanup fails.
    pub async fn shutdown(&self) -> Result<()> {
        let mut closed = self.closed.lock().await;
        if *closed {
            return Ok(());
        }
        *closed = true;
        drop(closed);
        self.shutdown.cancel();
        if let Some(mut task) = self.task.lock().await.take() {
            if let Ok(result) = tokio::time::timeout(self.request_timeout, &mut task).await {
                result.map_err(|error| {
                    ServiceError::internal(
                        anyhow::Error::new(error).context("join Etcd keepalive task"),
                    )
                })?;
            } else {
                task.abort();
                let _ = task.await;
                return Err(ServiceError::unavailable(anyhow::anyhow!(
                    "Etcd registration keepalive shutdown timed out"
                )));
            }
        }
        let mut client = self.client.clone();
        request(
            self.request_timeout,
            client.lease_revoke(self.lease_id),
            "revoke Etcd registration lease",
        )
        .await?;
        Ok(())
    }
}

#[derive(Clone)]
pub struct EtcdDiscovery {
    inner: Arc<DiscoveryInner>,
}

struct DiscoveryInner {
    service_name: String,
    instances: RwLock<Vec<Arc<Instance>>>,
    health: Arc<RwLock<Option<Arc<str>>>>,
    sender: Sender<Change<String>>,
    receiver: InactiveReceiver<Change<String>>,
    shutdown: CancellationToken,
    request_timeout: Duration,
    task: Mutex<Option<tokio::task::JoinHandle<()>>>,
    closed: Mutex<bool>,
}

impl EtcdDiscovery {
    async fn start(
        mut client: RawClient,
        config: &EtcdConfig,
        service_name: &str,
        shutdown: CancellationToken,
    ) -> Result<Self> {
        if shutdown.is_cancelled() {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "Etcd discovery startup was canceled"
            )));
        }
        validate_service_name(service_name)?;
        let key = format!("{}/{service_name}/", config.prefix.trim_end_matches('/'));
        let (instances, revision) = snapshot(&mut client, &key, config.request_timeout).await?;
        if instances.is_empty() {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "no Etcd instances are registered"
            )));
        }
        let mut stream = request(
            config.request_timeout,
            client.watch(
                key.clone(),
                Some(
                    WatchOptions::new()
                        .with_prefix()
                        .with_start_revision(revision.saturating_add(1)),
                ),
            ),
            "watch Etcd service instances",
        )
        .await?;
        let (mut sender, receiver) = async_broadcast::broadcast(16);
        sender.set_overflow(true);
        let receiver = receiver.deactivate();
        let health = Arc::new(RwLock::new(None));
        let inner = Arc::new(DiscoveryInner {
            service_name: service_name.to_owned(),
            instances: RwLock::new(instances),
            health: health.clone(),
            sender,
            receiver,
            shutdown,
            request_timeout: config.request_timeout,
            task: Mutex::new(None),
            closed: Mutex::new(false),
        });
        let task_inner = inner.clone();
        let task = tokio::spawn(async move {
            loop {
                let response = tokio::select! {
                    () = task_inner.shutdown.cancelled() => break,
                    response = stream.message() => response,
                };
                match response {
                    Ok(Some(response))
                        if !response.canceled() && response.compact_revision() == 0 => {}
                    _ => {
                        set_health_error(&health, "Etcd service discovery watch stopped").await;
                        break;
                    }
                }
                match snapshot(&mut client, &key, task_inner.request_timeout).await {
                    Ok((next, _)) if !next.is_empty() => {
                        let mut current = task_inner.instances.write().await;
                        let (change, changed) = instance_diff(
                            task_inner.service_name.clone(),
                            current.as_slice(),
                            next.as_slice(),
                        );
                        *current = next;
                        drop(current);
                        if changed {
                            let _ = task_inner.sender.try_broadcast(change);
                        }
                    }
                    Ok(_) => {
                        set_health_error(&health, "Etcd service discovery has no instances").await;
                        break;
                    }
                    Err(_) => {
                        set_health_error(&health, "Etcd service discovery refresh failed").await;
                        break;
                    }
                }
            }
        });
        *inner.task.lock().await = Some(task);
        Ok(Self { inner })
    }

    /// Reports whether the watched discovery snapshot is current and non-empty.
    ///
    /// # Errors
    ///
    /// Returns an error after watch failure, an empty snapshot, or shutdown.
    pub async fn ready(&self) -> Result<()> {
        if *self.inner.closed.lock().await {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "Etcd service discovery is stopped"
            )));
        }
        health_result(&self.inner.health, "Etcd service discovery").await?;
        if self.inner.instances.read().await.is_empty() {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "Etcd service discovery has no instances"
            )));
        }
        Ok(())
    }

    /// Stops and joins the discovery watch once.
    ///
    /// # Errors
    ///
    /// Returns an error when the watch task does not stop within its timeout.
    pub async fn shutdown(&self) -> Result<()> {
        let mut closed = self.inner.closed.lock().await;
        if *closed {
            return Ok(());
        }
        *closed = true;
        drop(closed);
        self.inner.shutdown.cancel();
        self.inner.sender.close();
        if let Some(mut task) = self.inner.task.lock().await.take() {
            if let Ok(result) = tokio::time::timeout(self.inner.request_timeout, &mut task).await {
                result.map_err(|error| {
                    ServiceError::internal(
                        anyhow::Error::new(error).context("join Etcd discovery task"),
                    )
                })?;
            } else {
                task.abort();
                let _ = task.await;
                return Err(ServiceError::unavailable(anyhow::anyhow!(
                    "Etcd discovery shutdown timed out"
                )));
            }
        }
        Ok(())
    }
}

impl Discover for EtcdDiscovery {
    type Key = String;
    type Error = LoadBalanceError;

    async fn discover<'s>(
        &'s self,
        endpoint: &'s Endpoint,
    ) -> std::result::Result<Vec<Arc<Instance>>, Self::Error> {
        if endpoint.service_name_ref() != self.inner.service_name {
            return Err(discovery_error(
                "Etcd discovery service name does not match",
            ));
        }
        if *self.inner.closed.lock().await {
            return Err(discovery_error("Etcd discovery is stopped"));
        }
        if let Some(error) = self.inner.health.read().await.as_ref() {
            return Err(discovery_error(error.as_ref()));
        }
        let instances = self.inner.instances.read().await.clone();
        if instances.is_empty() {
            return Err(discovery_error("Etcd discovery returned no instances"));
        }
        Ok(instances)
    }

    fn key(&self, endpoint: &Endpoint) -> Self::Key {
        endpoint.service_name_ref().to_owned()
    }

    fn watch(&self, keys: Option<&[Self::Key]>) -> Option<Receiver<Change<Self::Key>>> {
        if keys.is_some_and(|keys| !keys.contains(&self.inner.service_name)) {
            return None;
        }
        Some(self.inner.receiver.activate_cloned())
    }
}

#[derive(Debug, Deserialize, Serialize)]
struct InstanceRecord {
    network: String,
    address: String,
    weight: i64,
    tags: Option<HashMap<String, String>>,
}

async fn snapshot(
    client: &mut RawClient,
    key: &str,
    request_timeout: Duration,
) -> Result<(Vec<Arc<Instance>>, i64)> {
    let response = request(
        request_timeout,
        client.get(key, Some(GetOptions::new().with_prefix())),
        "read Etcd service instances",
    )
    .await?;
    let revision = response
        .header()
        .map_or(0, etcd_client::ResponseHeader::revision);
    let mut records = Vec::with_capacity(response.kvs().len());
    for item in response.kvs() {
        let record: InstanceRecord = serde_json::from_slice(item.value()).map_err(|error| {
            ServiceError::unavailable(
                anyhow::Error::new(error).context("decode Etcd service instance"),
            )
        })?;
        if record.network != "tcp" || !(1..=i64::from(u32::MAX)).contains(&record.weight) {
            return Err(ServiceError::unavailable(anyhow::anyhow!(
                "Etcd service instance is invalid"
            )));
        }
        records.push(record);
    }
    let resolved = futures_util::stream::iter(
        records
            .into_iter()
            .map(|record| async move { resolve_instance_record(record, request_timeout).await }),
    )
    .buffer_unordered(DNS_RESOLUTION_CONCURRENCY)
    .try_collect::<Vec<_>>();
    let mut instances = tokio::time::timeout(request_timeout, resolved)
        .await
        .map_err(|_| {
            ServiceError::unavailable(anyhow::anyhow!(
                "Etcd service instance resolution timed out"
            ))
        })??
        .into_iter()
        .flatten()
        .collect::<Vec<_>>();
    instances.sort_by_key(|instance| instance.address.to_string());
    Ok((instances, revision))
}

async fn resolve_instance_record(
    record: InstanceRecord,
    request_timeout: Duration,
) -> Result<Vec<Arc<Instance>>> {
    let addresses = resolve_socket_addresses(&record.address, request_timeout)
        .await
        .map_err(|error| {
            ServiceError::unavailable(
                anyhow::Error::new(error).context("resolve Etcd service instance address"),
            )
        })?;
    let tags = record
        .tags
        .unwrap_or_default()
        .into_iter()
        .map(|(key, value)| (Cow::Owned(key), Cow::Owned(value)))
        .collect::<HashMap<_, _>>();
    let weight = u32::try_from(record.weight).map_err(|error| {
        ServiceError::unavailable(
            anyhow::Error::new(error).context("convert Etcd service instance weight"),
        )
    })?;
    Ok(addresses
        .into_iter()
        .map(|address| {
            Arc::new(Instance {
                address: Address::Ip(address),
                weight,
                tags: tags.clone(),
            })
        })
        .collect())
}

async fn registration_is_owned(
    client: &mut RawClient,
    key: &str,
    value: &[u8],
    lease_id: i64,
    request_timeout: Duration,
) -> Result<bool> {
    let response = request(
        request_timeout,
        client.get(key, None),
        "verify Etcd registration ownership",
    )
    .await?;
    let [registration] = response.kvs() else {
        return Ok(false);
    };
    Ok(registration.key() == key.as_bytes()
        && registration.value() == value
        && registration.lease() == lease_id)
}

fn instance_diff(
    key: String,
    previous: &[Arc<Instance>],
    next: &[Arc<Instance>],
) -> (Change<String>, bool) {
    let previous_by_address = previous
        .iter()
        .map(|instance| (instance.address.clone(), instance.clone()))
        .collect::<HashMap<_, _>>();
    let next_by_address = next
        .iter()
        .map(|instance| (instance.address.clone(), instance.clone()))
        .collect::<HashMap<_, _>>();
    let added = next_by_address
        .iter()
        .filter(|(address, _)| !previous_by_address.contains_key(address))
        .map(|(_, instance)| instance.clone())
        .collect::<Vec<_>>();
    let removed = previous_by_address
        .iter()
        .filter(|(address, _)| !next_by_address.contains_key(address))
        .map(|(_, instance)| instance.clone())
        .collect::<Vec<_>>();
    let updated = next_by_address
        .iter()
        .filter_map(|(address, instance)| {
            previous_by_address
                .get(address)
                .filter(|previous| previous.as_ref() != instance.as_ref())
                .map(|_| instance.clone())
        })
        .collect::<Vec<_>>();
    let changed = !added.is_empty() || !removed.is_empty() || !updated.is_empty();
    (
        Change {
            key,
            all: next.to_vec(),
            added,
            updated,
            removed,
        },
        changed,
    )
}

fn etcd_tls(config: &TlsConfig) -> Result<TlsOptions> {
    let mut options = TlsOptions::new();
    if let Some(ca_file) = config.ca_file.as_deref() {
        options = options.ca_certificate(Certificate::from_pem(super::tls::read_pem(
            ca_file,
            "Etcd CA certificate",
        )?));
    }
    match (config.cert_file.as_deref(), config.key_file.as_deref()) {
        (Some(certificate), Some(key)) => {
            options = options.identity(Identity::from_pem(
                super::tls::read_pem(certificate, "Etcd client certificate")?,
                super::tls::read_pem(key, "Etcd client private key")?,
            ));
        }
        (None, None) => {}
        _ => {
            return Err(ServiceError::invalid_input(
                "Etcd client certificate and private key must be configured together",
            ));
        }
    }
    if let Some(server_name) = &config.server_name {
        options = options.domain_name(server_name.clone());
    }
    Ok(options)
}

fn validate_config(config: &EtcdConfig) -> Result<()> {
    if config.endpoints.is_empty()
        || config.prefix.trim().is_empty()
        || config.prefix == "/"
        || !config.prefix.starts_with('/')
        || config.connect_timeout.is_zero()
        || config.request_timeout.is_zero()
        || config.lease_ttl.is_zero()
    {
        return Err(ServiceError::invalid_input("Etcd configuration is invalid"));
    }
    if config.username.is_some() != config.password.is_some() {
        return Err(ServiceError::invalid_input(
            "Etcd username and password must be configured together",
        ));
    }
    Ok(())
}

fn validate_service_name(service_name: &str) -> Result<()> {
    if service_name.trim() != service_name || service_name.is_empty() || service_name.contains('/')
    {
        return Err(ServiceError::invalid_input("Etcd service name is invalid"));
    }
    Ok(())
}

fn validate_advertised_address(address: &str) -> Result<()> {
    validate_socket_endpoint(address).map_err(|error| {
        ServiceError::invalid_input("Etcd advertised address must be host:port").with_source(error)
    })
}

async fn request<T, E>(
    timeout: Duration,
    future: impl Future<Output = std::result::Result<T, E>>,
    operation: &'static str,
) -> Result<T>
where
    E: Into<anyhow::Error>,
{
    tokio::time::timeout(timeout, future)
        .await
        .map_err(|_| ServiceError::unavailable(anyhow::anyhow!("{operation} timed out")))?
        .map_err(|error| ServiceError::unavailable(error.into().context(operation)))
}

async fn set_health_error(health: &RwLock<Option<Arc<str>>>, message: &'static str) {
    *health.write().await = Some(Arc::from(message));
}

async fn health_result(health: &RwLock<Option<Arc<str>>>, component: &'static str) -> Result<()> {
    if let Some(error) = health.read().await.as_ref() {
        return Err(ServiceError::unavailable(anyhow::anyhow!(
            "{component} is unhealthy: {error}"
        )));
    }
    Ok(())
}

fn discovery_error(message: &str) -> LoadBalanceError {
    LoadBalanceError::Discover(Box::new(io::Error::other(message.to_owned())))
}

use std::{future::Future, io};

#[cfg(test)]
mod tests {
    use std::{collections::HashMap, sync::Arc};

    use volo::{discovery::Instance, net::Address};

    use super::{InstanceRecord, instance_diff, validate_advertised_address};

    #[test]
    fn record_is_compatible_with_go_registry_json() {
        let record: InstanceRecord = serde_json::from_str(
            r#"{"network":"tcp","address":"127.0.0.1:8883","weight":10,"tags":{"version":"dev"}}"#,
        )
        .expect("decode Go record");
        assert_eq!(record.network, "tcp");
        assert_eq!(record.address, "127.0.0.1:8883");
        assert_eq!(record.weight, 10);
        assert_eq!(record.tags.expect("tags")["version"], "dev");
    }

    #[test]
    fn advertised_address_accepts_only_host_port() {
        for address in ["127.0.0.1:8883", "collaboration:8883", "[::1]:8883"] {
            assert!(validate_advertised_address(address).is_ok(), "{address}");
        }
        for address in [
            "127.0.0.1:8883/",
            "127.0.0.1:8883/path",
            "user@127.0.0.1:8883",
            "@127.0.0.1:8883",
            "127.0.0.1:8883?zone=internal",
            "127.0.0.1:8883#fragment",
        ] {
            assert!(validate_advertised_address(address).is_err(), "{address}");
        }
    }

    #[test]
    fn diff_reports_weight_and_tag_updates() {
        let address = Address::Ip("127.0.0.1:8882".parse().expect("address"));
        let previous = Arc::new(Instance {
            address: address.clone(),
            weight: 1,
            tags: HashMap::new(),
        });
        let mut tags = HashMap::new();
        tags.insert("version".into(), "next".into());
        let next = Arc::new(Instance {
            address,
            weight: 2,
            tags,
        });
        let (change, changed) = instance_diff(
            "knowledge".to_owned(),
            &[previous],
            std::slice::from_ref(&next),
        );
        assert!(changed);
        assert_eq!(change.updated, vec![next]);
        assert!(change.added.is_empty());
        assert!(change.removed.is_empty());
    }
}
