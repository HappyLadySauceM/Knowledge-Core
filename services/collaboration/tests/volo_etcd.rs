use std::{collections::HashMap, env, error::Error, io, time::Duration};

use etcd_client::Client as RawEtcdClient;
use knowledge_core_collaboration::{
    config::{EtcdConfig, TlsConfig},
    rpc::etcd::{EtcdClient, EtcdRegistration},
};
use tokio::time::timeout;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;
use volo::{context::Endpoint, discovery::Discover};

type TestResult<T = ()> = Result<T, Box<dyn Error + Send + Sync>>;

const REQUIRE_REAL_DEPENDENCIES: &str = "COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES";

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn volo_registry_and_discovery_against_real_etcd() -> TestResult {
    let Some(endpoints) = endpoints()? else {
        eprintln!("SKIP Volo Etcd integration test: COLLABORATION_TEST_ETCD_ENDPOINTS is required");
        return Ok(());
    };
    Box::pin(timeout(Duration::from_secs(45), run_contract(endpoints)))
        .await
        .map_err(|_| test_error("Volo Etcd integration test exceeded 45 seconds"))??;
    Ok(())
}

async fn run_contract(endpoints: Vec<String>) -> TestResult {
    let suffix = Uuid::now_v7().simple().to_string();
    let config = EtcdConfig {
        endpoints: endpoints.clone(),
        prefix: format!("/knowledge-core/tests/rust-volo/{suffix}"),
        username: None,
        password: None,
        connect_timeout: Duration::from_secs(5),
        request_timeout: Duration::from_secs(5),
        lease_ttl: Duration::from_secs(6),
        tls: TlsConfig::default(),
    };
    let client = EtcdClient::connect(&config).await?;
    let cancellation = CancellationToken::new();
    let service_name = "knowledge-core.etcd-integration";
    let first_address = "localhost:38881";
    let second_address = "127.0.0.1:38882";

    let first = client
        .register(
            service_name,
            first_address,
            HashMap::from([("instance".to_owned(), "first".to_owned())]),
            cancellation.child_token(),
        )
        .await?;
    first.ready().await?;
    let discovery = client
        .discover(service_name, cancellation.child_token())
        .await?;
    discovery.ready().await?;
    let endpoint = Endpoint::new(service_name.into());
    let initial = discovery.discover(&endpoint).await?;
    assert!(!initial.is_empty());
    for instance in &initial {
        let address = instance
            .address
            .ip_addr()
            .ok_or_else(|| test_error("Etcd hostname did not resolve to an IP address"))?;
        assert!(address.ip().is_loopback());
        assert_eq!(address.port(), 38881);
        assert_eq!(instance.weight, 10);
        assert_eq!(
            instance.tags.get("instance").map(AsRef::as_ref),
            Some("first")
        );
    }
    let initial_count = initial.len();

    let keys = [service_name.to_owned()];
    let mut changes = discovery
        .watch(Some(&keys))
        .ok_or_else(|| test_error("Etcd discovery did not provide a watch receiver"))?;
    let second = client
        .register(
            service_name,
            second_address,
            HashMap::from([("instance".to_owned(), "second".to_owned())]),
            cancellation.child_token(),
        )
        .await?;
    second.ready().await?;

    let added = timeout(Duration::from_secs(5), changes.recv())
        .await
        .map_err(|_| test_error("Etcd discovery add event timed out"))??;
    assert_eq!(added.key, service_name);
    assert!(
        added
            .added
            .iter()
            .any(|instance| instance.address.to_string() == second_address)
    );
    assert_eq!(added.all.len(), initial_count + 1);

    second.shutdown().await?;
    second.shutdown().await?;
    let removed = timeout(Duration::from_secs(5), changes.recv())
        .await
        .map_err(|_| test_error("Etcd discovery remove event timed out"))??;
    assert!(
        removed
            .removed
            .iter()
            .any(|instance| instance.address.to_string() == second_address)
    );
    assert_eq!(removed.all.len(), initial_count);
    discovery.ready().await?;

    discovery.shutdown().await?;
    discovery.shutdown().await?;
    assert!(discovery.ready().await.is_err());

    assert_registration_fails_after_deletion(
        endpoints,
        format!("{}/{service_name}/{first_address}", config.prefix),
        &first,
    )
    .await?;
    client.ping().await?;
    first.shutdown().await?;
    first.shutdown().await?;
    cancellation.cancel();
    Ok(())
}

async fn assert_registration_fails_after_deletion(
    endpoints: Vec<String>,
    registration_key: String,
    registration: &EtcdRegistration,
) -> TestResult {
    let mut raw_client = RawEtcdClient::connect(endpoints, None).await?;
    let deleted = raw_client.delete(registration_key, None).await?;
    assert_eq!(deleted.deleted(), 1);
    let readiness_error = timeout(Duration::from_secs(5), async {
        let mut checks = tokio::time::interval(Duration::from_millis(25));
        checks.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        loop {
            checks.tick().await;
            if let Err(error) = registration.ready().await {
                break error;
            }
        }
    })
    .await
    .map_err(|_| test_error("Etcd registration stayed ready after its key was deleted"))?;
    assert_eq!(readiness_error.numeric_code(), 40_007);
    assert_eq!(readiness_error.key(), "collaboration.unavailable");
    Ok(())
}

fn endpoints() -> TestResult<Option<Vec<String>>> {
    let required = real_dependencies_required()?;
    let Some(endpoints) = env::var("COLLABORATION_TEST_ETCD_ENDPOINTS")
        .ok()
        .filter(|value| !value.trim().is_empty())
    else {
        if required {
            return Err(test_error(
                "COLLABORATION_TEST_ETCD_ENDPOINTS is required when COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1",
            ));
        }
        return Ok(None);
    };
    let endpoints = endpoints
        .split(',')
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned)
        .collect::<Vec<_>>();
    if required && endpoints.is_empty() {
        return Err(test_error(
            "COLLABORATION_TEST_ETCD_ENDPOINTS is required when COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1",
        ));
    }
    Ok((!endpoints.is_empty()).then_some(endpoints))
}

fn real_dependencies_required() -> TestResult<bool> {
    match env::var(REQUIRE_REAL_DEPENDENCIES) {
        Ok(value) => match value.as_str() {
            "1" | "true" => Ok(true),
            "0" | "false" => Ok(false),
            _ => Err(test_error(
                "COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES must be one of 0, 1, false, or true",
            )),
        },
        Err(env::VarError::NotPresent) => Ok(false),
        Err(env::VarError::NotUnicode(_)) => Err(test_error(
            "COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES must contain valid Unicode",
        )),
    }
}

fn test_error(message: &'static str) -> Box<dyn Error + Send + Sync> {
    Box::new(io::Error::other(message))
}
