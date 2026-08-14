use std::{
    env,
    error::Error,
    io,
    str::FromStr,
    time::{Duration, Instant},
};

use async_nats::jetstream::{
    self,
    consumer::{AckPolicy, DeliverPolicy, pull},
};
use bytes::Bytes;
use futures_util::StreamExt as _;
use knowledge_core_collaboration::{
    config::{
        NATS_INVALIDATION_SUBJECT, NATS_PERMISSION_SUBJECT, NATS_UPDATE_SUBJECT, NatsConfig,
        PostgresConfig, TlsConfig,
    },
    domain::{DocumentId, PublicUser, RequestContext, VersionId},
    error::ErrorCode,
    richtext,
    storage::{
        DocumentStore, EventSubjects, PostgresStore, RestorationCandidate, UpdateLimits,
        VersionStore, WorkerStore,
    },
    worker::{EventPublisher, NatsClient},
};
use redis::aio::ConnectionManager;
use sqlx::{
    Row as _,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use tokio::time::timeout;
use url::Url;
use uuid::Uuid;
use yrs::{ReadTxn, Transact, XmlElementPrelim, XmlFragment};

type TestResult<T = ()> = Result<T, Box<dyn Error + Send + Sync>>;

const REQUIRE_REAL_DEPENDENCIES: &str = "COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES";
const REQUIRED_ENVIRONMENT: [&str; 3] = [
    "COLLABORATION_TEST_POSTGRES_URL",
    "COLLABORATION_TEST_POSTGRES_PASSWORD",
    "COLLABORATION_TEST_REDIS_URL",
];

struct RealEnvironment {
    postgres_url: String,
    redis_url: String,
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn repository_contracts_against_real_dependencies() -> TestResult {
    let required = real_dependencies_required()?;
    let Some(environment) = real_environment()? else {
        if required {
            return Err(test_error(
                "COLLABORATION_TEST_POSTGRES_URL, COLLABORATION_TEST_POSTGRES_PASSWORD, and COLLABORATION_TEST_REDIS_URL are required when COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1",
            ));
        }
        eprintln!(
            "SKIP real Collaboration dependency tests: all {} variables are required",
            REQUIRED_ENVIRONMENT.join(", ")
        );
        return Ok(());
    };
    timeout(Duration::from_secs(90), run_contracts(environment))
        .await
        .map_err(|_| test_error("real dependency tests exceeded 90 seconds"))??;
    Ok(())
}

async fn run_contracts(environment: RealEnvironment) -> TestResult {
    postgres_contract(&environment.postgres_url).await?;
    redis_contract(&environment.redis_url).await?;
    Ok(())
}

#[tokio::test]
async fn nats_contract_against_real_jetstream() -> TestResult {
    let required = real_dependencies_required()?;
    let Some(url) = env::var("COLLABORATION_TEST_NATS_URL")
        .ok()
        .filter(|value| !value.trim().is_empty())
    else {
        if required {
            return Err(test_error(
                "COLLABORATION_TEST_NATS_URL is required when COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1",
            ));
        }
        eprintln!("SKIP real Collaboration NATS test: COLLABORATION_TEST_NATS_URL is required");
        return Ok(());
    };
    timeout(Duration::from_secs(30), Box::pin(nats_contract(&url)))
        .await
        .map_err(|_| test_error("real NATS contract exceeded 30 seconds"))??;
    Ok(())
}

async fn postgres_contract(url: &str) -> TestResult {
    unmanaged_schema_migration_contract(url).await?;
    let document_id = DocumentId::new();
    let actor = PublicUser {
        id: 71_001,
        username: "repository-test".to_owned(),
        avatar: String::new(),
    };
    let config = PostgresConfig {
        url: url.to_owned(),
        max_connections: 8,
        connect_timeout: Duration::from_secs(10),
        acquire_timeout: Duration::from_secs(5),
        operation_timeout: Duration::from_secs(20),
        tls: TlsConfig::default(),
    };
    let store = PostgresStore::open(
        &config,
        EventSubjects::new(
            "collaboration.tests.updated",
            "collaboration.tests.invalidated",
        ),
    )
    .await?;
    let migration_inspection = PgPoolOptions::new().max_connections(1).connect(url).await?;
    let migration_versions: Vec<i64> =
        sqlx::query_scalar("SELECT version FROM collaboration.schema_migrations ORDER BY version")
            .fetch_all(&migration_inspection)
            .await?;
    migration_inspection.close().await;
    assert_eq!(migration_versions, vec![1_i64, 2_i64]);
    let context = postgres_request_context("real-postgres-contract");
    store.initialize_document(&context, document_id).await?;
    let initial = store.load_document(&context, document_id).await?;
    assert_eq!(initial.generation, 1);
    assert_eq!(initial.sequence, 0);

    let initial_version =
        version_and_update_contract(&store, &context, document_id, &actor, &initial.state).await?;
    worker_restore_and_purge_contract(&store, &context, document_id, &actor, initial_version, url)
        .await?;
    store.cleanup(&context).await?;
    store.close().await;
    cleanup_postgres(url, document_id).await
}

async fn unmanaged_schema_migration_contract(url: &str) -> TestResult {
    let database = format!("collaboration_unmanaged_{}", Uuid::now_v7().simple());
    let admin = PgPoolOptions::new().max_connections(1).connect(url).await?;
    sqlx::query(sqlx::AssertSqlSafe(format!(
        "CREATE DATABASE \"{database}\""
    )))
    .execute(&admin)
    .await?;
    let options = PgConnectOptions::from_str(url)?.database(&database);
    let isolated = PgPoolOptions::new()
        .max_connections(2)
        .connect_with(options)
        .await?;
    let contract = async {
        sqlx::raw_sql(
            "CREATE SCHEMA collaboration;
             CREATE TABLE collaboration.legacy_documents(id bigint PRIMARY KEY)",
        )
        .execute(&isolated)
        .await?;
        let store = PostgresStore::from_pool(
            isolated.clone(),
            Duration::from_secs(10),
            EventSubjects::new(
                "collaboration.tests.updated",
                "collaboration.tests.invalidated",
            ),
        );
        let error = store
            .migrate()
            .await
            .expect_err("an unmanaged Collaboration schema must be rejected");
        assert_eq!(error.code(), ErrorCode::Conflict);
        let row = sqlx::query(
            "SELECT to_regclass('collaboration.legacy_documents') IS NOT NULL AS present",
        )
        .fetch_one(&isolated)
        .await?;
        assert!(row.try_get::<bool, _>("present")?);
        TestResult::Ok(())
    }
    .await;
    isolated.close().await;
    let drop_result = sqlx::query(sqlx::AssertSqlSafe(format!("DROP DATABASE \"{database}\"")))
        .execute(&admin)
        .await;
    admin.close().await;
    contract?;
    drop_result?;
    Ok(())
}

async fn version_and_update_contract(
    store: &PostgresStore,
    context: &RequestContext,
    document_id: DocumentId,
    actor: &PublicUser,
    initial_state: &[u8],
) -> TestResult<VersionId> {
    let initial_version = store
        .create_manual_version(
            context,
            document_id,
            actor,
            Some("Initial"),
            Some("initial-version"),
        )
        .await?;
    let replayed = store
        .create_manual_version(
            context,
            document_id,
            actor,
            Some("Initial"),
            Some("initial-version"),
        )
        .await?;
    assert_eq!(replayed.id, initial_version.id);
    let conflict = store
        .create_manual_version(
            context,
            document_id,
            actor,
            Some("Different"),
            Some("initial-version"),
        )
        .await
        .expect_err("reusing an idempotency key for another request must fail");
    assert_eq!(conflict.code(), ErrorCode::Conflict);

    let first_update = append_paragraph_update(initial_state)?;
    let second_update = append_paragraph_update(initial_state)?;
    let limits = UpdateLimits {
        maximum_update_bytes: 1 << 20,
        maximum_document_bytes: 16 << 20,
    };
    let (first, second) = tokio::join!(
        store.append_update(context, document_id, &first_update, actor, limits),
        store.append_update(context, document_id, &second_update, actor, limits),
    );
    let mut sequences = [first?.sequence, second?.sequence];
    sequences.sort_unstable();
    assert_eq!(sequences, [1, 2]);
    let duplicate = store
        .append_update(context, document_id, &first_update, actor, limits)
        .await?;
    assert!(duplicate.update.is_none());
    assert_eq!(duplicate.sequence, 2);
    Ok(initial_version.id)
}

async fn worker_restore_and_purge_contract(
    store: &PostgresStore,
    context: &RequestContext,
    document_id: DocumentId,
    actor: &PublicUser,
    initial_version: VersionId,
    url: &str,
) -> TestResult {
    let projection = store
        .claim_projection_job(context, Duration::from_secs(5))
        .await?
        .ok_or_else(|| test_error("projection job was not enqueued"))?;
    assert_eq!(projection.sequence, 2);
    store.complete_projection(context, &projection).await?;
    assert!(store.compact_next(context, 1, i64::MAX).await?);
    assert!(
        store
            .create_automatic_version(context, Duration::from_millis(1))
            .await?
    );

    restoration_contract(store, context, document_id, actor, initial_version, url).await?;

    let events = store
        .claim_outbox(context, 20, Duration::from_secs(5))
        .await?;
    assert!(events.len() >= 3);
    for event in events {
        store.complete_outbox(context, event.id).await?;
    }
    store.purge_document(context, document_id).await?;
    let purged = store.load_document(context, document_id).await?;
    assert_eq!(purged.generation, 3);
    assert_eq!(purged.sequence, 3);
    assert_eq!(
        richtext::projection_from_state(&purged.state)?.content,
        serde_json::json!({"type":"doc","content":[{"type":"paragraph"}]})
    );
    Ok(())
}

async fn restoration_contract(
    store: &PostgresStore,
    context: &RequestContext,
    document_id: DocumentId,
    actor: &PublicUser,
    initial_version: VersionId,
    url: &str,
) -> TestResult {
    let versions = store.list_versions(context, document_id, None, 20).await?;
    assert!(versions.items.len() >= 2);
    deleted_target_contract(store, context, document_id, actor, url).await?;
    let target = store
        .get_version(context, document_id, initial_version)
        .await?;
    let baseline = store.load_document(context, document_id).await?;
    let baseline_document = richtext::document_from_state(&baseline.state)?;
    let (restore_update, _, _) = richtext::restore_state(&baseline_document, &target.state)?;
    drop(baseline_document);
    let limits = UpdateLimits {
        maximum_update_bytes: 1 << 20,
        maximum_document_bytes: 16 << 20,
    };
    let restored = store
        .commit_restoration(
            context,
            document_id,
            RestorationCandidate {
                target: &target,
                baseline_generation: baseline.generation,
                baseline_sequence: baseline.sequence,
                expected_sequence: 2,
                update: &restore_update,
                actor,
                idempotency_key: Some("restore-initial"),
                limits,
            },
        )
        .await?;
    let committed = restored
        .committed
        .as_ref()
        .ok_or_else(|| test_error("restoration did not commit a forward update"))?;
    assert_eq!(committed.generation, 2);
    assert_eq!(committed.sequence, 3);
    let replayed_restore = store
        .commit_restoration(
            context,
            document_id,
            RestorationCandidate {
                target: &target,
                baseline_generation: baseline.generation,
                baseline_sequence: baseline.sequence,
                expected_sequence: 2,
                update: &restore_update,
                actor,
                idempotency_key: Some("restore-initial"),
                limits,
            },
        )
        .await?;
    assert_eq!(replayed_restore.version.id, restored.version.id);
    assert!(replayed_restore.committed.is_none());
    let stale = store
        .commit_restoration(
            context,
            document_id,
            RestorationCandidate {
                target: &target,
                baseline_generation: baseline.generation,
                baseline_sequence: baseline.sequence,
                expected_sequence: 2,
                update: &restore_update,
                actor,
                idempotency_key: None,
                limits,
            },
        )
        .await
        .expect_err("stale restoration baseline must fail");
    assert_eq!(stale.code(), ErrorCode::PreconditionFailed);
    Ok(())
}

async fn deleted_target_contract(
    store: &PostgresStore,
    context: &RequestContext,
    document_id: DocumentId,
    actor: &PublicUser,
    url: &str,
) -> TestResult {
    let target = store
        .create_manual_version(context, document_id, actor, Some("Transient"), None)
        .await?;
    let baseline = store.load_document(context, document_id).await?;
    let document = richtext::document_from_state(&baseline.state)?;
    let (update, _, _) = richtext::restore_state(&document, &target.state)?;
    drop(document);
    let external = PgPoolOptions::new().max_connections(1).connect(url).await?;
    sqlx::query("DELETE FROM collaboration.versions WHERE id = $1")
        .bind(target.id.as_uuid())
        .execute(&external)
        .await?;
    external.close().await;

    let error = store
        .commit_restoration(
            context,
            document_id,
            RestorationCandidate {
                target: &target,
                baseline_generation: baseline.generation,
                baseline_sequence: baseline.sequence,
                expected_sequence: baseline.sequence,
                update: &update,
                actor,
                idempotency_key: None,
                limits: UpdateLimits {
                    maximum_update_bytes: 1 << 20,
                    maximum_document_bytes: 16 << 20,
                },
            },
        )
        .await
        .expect_err("a deleted restoration target must not be committed");
    assert_eq!(error.code(), ErrorCode::NotFound);
    let unchanged = store.load_document(context, document_id).await?;
    assert_eq!(unchanged.generation, baseline.generation);
    assert_eq!(unchanged.sequence, baseline.sequence);
    Ok(())
}

async fn cleanup_postgres(url: &str, document_id: DocumentId) -> TestResult {
    let cleanup = PgPoolOptions::new().max_connections(1).connect(url).await?;
    sqlx::query("DELETE FROM collaboration.outbox WHERE document_id = $1")
        .bind(document_id.as_uuid())
        .execute(&cleanup)
        .await?;
    sqlx::query("DELETE FROM collaboration.documents WHERE document_id = $1")
        .bind(document_id.as_uuid())
        .execute(&cleanup)
        .await?;
    cleanup.close().await;
    Ok(())
}

async fn redis_contract(url: &str) -> TestResult {
    let client = redis::Client::open(url)?;
    let mut connection = ConnectionManager::new(client).await?;
    let key = format!("knowledge-core:collaboration:test:{}", Uuid::now_v7());
    let result: Option<String> = redis::cmd("SET")
        .arg(&key)
        .arg("ticket-payload")
        .arg("NX")
        .arg("PX")
        .arg(5_000_u64)
        .query_async(&mut connection)
        .await?;
    assert_eq!(result.as_deref(), Some("OK"));
    let mut first_connection = connection.clone();
    let mut second_connection = connection.clone();
    let mut first_command = redis::cmd("GETDEL");
    first_command.arg(&key);
    let mut second_command = redis::cmd("GETDEL");
    second_command.arg(&key);
    let (first_consumed, second_consumed): (Option<String>, Option<String>) =
        timeout(Duration::from_secs(5), async {
            tokio::try_join!(
                first_command.query_async(&mut first_connection),
                second_command.query_async(&mut second_connection),
            )
        })
        .await??;
    let consumed = [first_consumed.as_deref(), second_consumed.as_deref()];
    assert_eq!(
        consumed
            .iter()
            .filter(|value| **value == Some("ticket-payload"))
            .count(),
        1
    );
    assert_eq!(consumed.iter().filter(|value| value.is_none()).count(), 1);
    let replay: Option<String> = redis::cmd("GETDEL")
        .arg(&key)
        .query_async(&mut connection)
        .await?;
    assert!(replay.is_none());
    Ok(())
}

async fn nats_contract(url: &str) -> TestResult {
    let client = async_nats::connect(url).await?;
    let context = jetstream::new(client.clone());
    let suffix = Uuid::now_v7().simple().to_string();
    let stream_name = format!("KC_COLLAB_TEST_{suffix}").to_uppercase();
    let permission_stream_name = format!("KC_COLLAB_PERMISSIONS_TEST_{suffix}").to_uppercase();

    let config = NatsConfig {
        servers: vec![url.to_owned()],
        name: "knowledge-core.collaboration.real-test".to_owned(),
        stream: stream_name.clone(),
        permission_stream: permission_stream_name.clone(),
        update_subject: NATS_UPDATE_SUBJECT.to_owned(),
        invalidation_subject: NATS_INVALIDATION_SUBJECT.to_owned(),
        permission_subject: NATS_PERMISSION_SUBJECT.to_owned(),
        connect_timeout: Duration::from_secs(5),
        operation_timeout: Duration::from_secs(5),
        token: None,
        username: None,
        password: None,
        tls: TlsConfig::default(),
    };
    let primary_instance = format!("real-test-primary-{suffix}");
    let peer_instance = format!("real-test-peer-{suffix}");
    let (production, peer) = tokio::try_join!(
        NatsClient::connect(&config, &primary_instance),
        NatsClient::connect(&config, &peer_instance),
    )?;
    verify_stream_contracts(&context, &stream_name, &permission_stream_name, &suffix).await?;
    let mut subscription = production
        .subscribe("contract", NATS_UPDATE_SUBJECT, Duration::from_secs(5))
        .await?;
    production
        .publish(NATS_UPDATE_SUBJECT, b"committed".to_vec())
        .await?;
    let message = timeout(Duration::from_secs(5), subscription.next())
        .await
        .map_err(|_| test_error("production NATS consumer timed out"))?
        .ok_or_else(|| test_error("production NATS consumer stopped"))??;
    assert_eq!(message.payload.as_ref(), b"committed");
    let info = message.info()?;
    assert_eq!(info.stream, stream_name);
    assert!(info.consumer.ends_with("-contract"));
    timeout(Duration::from_secs(5), message.double_ack())
        .await
        .map_err(|_| test_error("production NATS acknowledgement timed out"))??;
    peer.shutdown(Duration::from_secs(5)).await?;
    production.shutdown(Duration::from_secs(5)).await?;
    context.delete_stream(stream_name).await?;
    context.delete_stream(permission_stream_name).await?;
    client.flush().await?;
    Ok(())
}

async fn verify_stream_contracts(
    context: &jetstream::Context,
    document_stream_name: &str,
    permission_stream_name: &str,
    suffix: &str,
) -> TestResult {
    let document_stream = context.get_stream(document_stream_name).await?;
    let document_info = document_stream.get_info().await?;
    let permission_stream = context.get_stream(permission_stream_name).await?;
    let permission_info = permission_stream.get_info().await?;
    assert_eq!(
        document_info.config.subjects,
        vec![
            NATS_INVALIDATION_SUBJECT.to_owned(),
            NATS_UPDATE_SUBJECT.to_owned(),
        ]
    );
    assert_eq!(document_info.config.max_bytes, 1_073_741_824);
    assert_eq!(
        permission_info.config.subjects,
        vec![NATS_PERMISSION_SUBJECT.to_owned()]
    );
    assert_eq!(permission_info.config.max_bytes, -1);
    assert!(
        document_info
            .config
            .subjects
            .iter()
            .all(|subject| !permission_info.config.subjects.contains(subject))
    );

    let mut sequences = Vec::with_capacity(3);
    for revision in 1..=3 {
        let acknowledgement = context
            .publish(
                NATS_PERMISSION_SUBJECT.to_owned(),
                Bytes::from(format!(
                    r#"{{"document_id":"{}","permission_revision":{revision},"deleted":false}}"#,
                    Uuid::now_v7()
                )),
            )
            .await?
            .await?;
        assert_eq!(acknowledgement.stream, permission_stream_name);
        sequences.push(acknowledgement.sequence);
    }
    assert!(permission_stream.delete_message(sequences[1]).await?);

    let durable = format!("real-permission-replay-{suffix}");
    let consumer = permission_stream
        .get_or_create_consumer(
            &durable,
            pull::Config {
                durable_name: Some(durable.clone()),
                name: Some(durable.clone()),
                deliver_policy: DeliverPolicy::All,
                ack_policy: AckPolicy::Explicit,
                filter_subject: NATS_PERMISSION_SUBJECT.to_owned(),
                ..Default::default()
            },
        )
        .await?;
    let target_stream_sequence = permission_stream.get_info().await?.state.last_sequence;
    let mut messages = consumer.messages().await?;
    for expected_sequence in [sequences[0], sequences[2]] {
        let message = timeout(Duration::from_secs(5), messages.next())
            .await
            .map_err(|_| test_error("permission replay message timed out"))?
            .ok_or_else(|| test_error("permission replay consumer stopped"))??;
        assert_eq!(message.info()?.stream_sequence, expected_sequence);
        timeout(Duration::from_secs(5), message.double_ack())
            .await
            .map_err(|_| test_error("permission replay acknowledgement timed out"))??;
    }
    let consumer_info = consumer.get_info().await?;
    assert_eq!(consumer_info.num_pending, 0);
    assert_eq!(consumer_info.num_ack_pending, 0);
    assert!(consumer_info.ack_floor.stream_sequence >= target_stream_sequence);
    assert!(consumer_info.ack_floor.consumer_sequence < target_stream_sequence);
    Ok(())
}

fn append_paragraph_update(state: &[u8]) -> TestResult<Vec<u8>> {
    let document = richtext::document_from_state(state)?;
    let before = document.transact().state_vector();
    let fragment = document.get_or_insert_xml_fragment(richtext::FRAGMENT_NAME);
    fragment.push_back(
        &mut document.transact_mut(),
        XmlElementPrelim::empty("paragraph"),
    );
    Ok(document.transact().encode_state_as_update_v1(&before))
}

fn real_environment() -> TestResult<Option<RealEnvironment>> {
    let values = REQUIRED_ENVIRONMENT
        .map(|name| env::var(name).ok().filter(|value| !value.trim().is_empty()));
    let [Some(postgres_url), Some(postgres_password), Some(redis_url)] = values else {
        return Ok(None);
    };
    Ok(Some(RealEnvironment {
        postgres_url: postgres_url_with_password(&postgres_url, &postgres_password)?,
        redis_url,
    }))
}

fn postgres_url_with_password(base_url: &str, password: &str) -> TestResult<String> {
    let mut url = Url::parse(base_url)?;
    url.set_password(Some(password))
        .map_err(|()| test_error("COLLABORATION_TEST_POSTGRES_URL cannot contain credentials"))?;
    Ok(url.into())
}

#[test]
fn postgres_password_is_encoded_before_sqlx_parses_the_url() -> TestResult {
    let url = postgres_url_with_password(
        "postgres://knowledge_core@127.0.0.1:5432/knowledge_core",
        "reserved:@/?#[]",
    )?;
    PgConnectOptions::from_str(&url)?;
    Ok(())
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

fn postgres_request_context(request_id: &'static str) -> RequestContext {
    let mut context = RequestContext::new(request_id);
    context.deadline = Instant::now().checked_add(Duration::from_mins(1));
    context
}

fn test_error(message: &'static str) -> Box<dyn Error + Send + Sync> {
    Box::new(io::Error::other(message))
}
