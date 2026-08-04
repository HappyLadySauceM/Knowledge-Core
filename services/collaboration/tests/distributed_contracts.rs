use std::{env, error::Error, io, sync::Arc, time::Duration};

use async_nats::jetstream;
use async_trait::async_trait;
use futures_util::StreamExt as _;
use knowledge_core_collaboration::{
    actor::{ActorLimits, ActorRegistry, ActorSession, ConnectionEvent},
    config::{
        ActorConfig, NATS_INVALIDATION_SUBJECT, NATS_PERMISSION_SUBJECT, NATS_UPDATE_SUBJECT,
        NatsConfig, PublicConfig, TlsConfig,
    },
    domain::{Access, DocumentId, PublicUser, RequestContext},
    error::{Result, ServiceError},
    richtext,
    storage::{
        CommittedUpdate, DocumentStore, LoadedDocument, RestorationCandidate, RestoreVersion,
        StoredUpdate, UpdateLimits,
    },
    telemetry::Metrics,
    worker::{EventPublisher as _, NatsClient},
};
use tokio::sync::Mutex;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;
use yrs::{
    Doc, ReadTxn, Transact, Update, XmlElementPrelim, XmlFragment,
    encoding::read::Cursor,
    sync::{Message, MessageReader, SyncMessage},
    updates::{
        decoder::{Decode as _, DecoderV1},
        encoder::Encode as _,
    },
};

type TestResult<T = ()> = std::result::Result<T, Box<dyn Error + Send + Sync>>;

const REQUIRE_REAL_DEPENDENCIES: &str = "COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES";
static NATS_FIXTURE_LOCK: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn real_jetstream_fans_out_to_two_instance_durables() -> TestResult {
    let _fixture_guard = NATS_FIXTURE_LOCK.lock().await;
    let Some(url) = nats_url()? else {
        eprintln!("SKIP distributed NATS fanout test: COLLABORATION_TEST_NATS_URL is required");
        return Ok(());
    };
    let fixture = NatsFixture::new(&url, "fanout").await?;
    let result = tokio::time::timeout(
        Duration::from_secs(20),
        Box::pin(fanout_contract(&fixture.config)),
    )
    .await
    .map_err(|_| test_error("distributed NATS fanout contract exceeded 20 seconds"))?;
    fixture.finish(result).await
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn real_jetstream_redelivers_unacked_message_to_stable_instance() -> TestResult {
    let _fixture_guard = NATS_FIXTURE_LOCK.lock().await;
    let Some(url) = nats_url()? else {
        eprintln!("SKIP durable NATS redelivery test: COLLABORATION_TEST_NATS_URL is required");
        return Ok(());
    };
    let fixture = NatsFixture::new(&url, "redelivery").await?;
    let result = tokio::time::timeout(
        Duration::from_secs(30),
        redelivery_contract(&fixture.config),
    )
    .await
    .map_err(|_| test_error("durable NATS redelivery contract exceeded 30 seconds"))?;
    fixture.finish(result).await
}

async fn fanout_contract(config: &NatsConfig) -> TestResult {
    let suffix = Uuid::now_v7().simple().to_string();
    let first_instance = format!("fanout-first-{suffix}");
    let second_instance = format!("fanout-second-{suffix}");
    let publisher_instance = format!("fanout-publisher-{suffix}");
    let (first, second, publisher) = tokio::try_join!(
        NatsClient::connect(config, &first_instance),
        NatsClient::connect(config, &second_instance),
        NatsClient::connect(config, &publisher_instance),
    )?;
    let result = async {
        let mut first_messages = first
            .subscribe("fanout", &config.update_subject, Duration::from_secs(2))
            .await?;
        let mut second_messages = second
            .subscribe("fanout", &config.update_subject, Duration::from_secs(2))
            .await?;
        publisher
            .publish(&config.update_subject, b"committed-update".to_vec())
            .await?;

        let first_message = next_message(&mut first_messages, "first fanout consumer").await?;
        let second_message = next_message(&mut second_messages, "second fanout consumer").await?;
        if first_message.payload.as_ref() != b"committed-update"
            || second_message.payload.as_ref() != b"committed-update"
        {
            return Err(test_error(
                "fanout consumers received an unexpected payload",
            ));
        }
        let first_consumer = first_message.info()?.consumer.to_owned();
        let second_consumer = second_message.info()?.consumer.to_owned();
        if first_consumer == second_consumer {
            return Err(test_error(
                "unique Collaboration instances shared one durable consumer",
            ));
        }
        acknowledge(first_message, "first fanout consumer").await?;
        acknowledge(second_message, "second fanout consumer").await
    }
    .await;

    let publisher_shutdown = publisher.shutdown(Duration::from_secs(3)).await;
    let second_shutdown = second.shutdown(Duration::from_secs(3)).await;
    let first_shutdown = first.shutdown(Duration::from_secs(3)).await;
    result?;
    publisher_shutdown?;
    second_shutdown?;
    first_shutdown?;
    Ok(())
}

async fn redelivery_contract(config: &NatsConfig) -> TestResult {
    let suffix = Uuid::now_v7().simple().to_string();
    let stable_instance = format!("stable-redelivery-{suffix}");
    let publisher = NatsClient::connect(config, &format!("redelivery-publisher-{suffix}")).await?;
    let first = NatsClient::connect(config, &stable_instance).await?;
    let mut first_messages = first
        .subscribe(
            "redelivery",
            &config.update_subject,
            Duration::from_millis(100),
        )
        .await?;
    publisher
        .publish(&config.update_subject, b"unacked-update".to_vec())
        .await?;
    let first_message = next_message(&mut first_messages, "initial durable consumer").await?;
    let first_info = first_message.info()?;
    if first_info.delivered != 1 {
        return Err(test_error("initial durable delivery count was not one"));
    }
    let durable_name = first_info.consumer.to_owned();
    drop(first_message);
    drop(first_messages);
    first.shutdown(Duration::from_secs(3)).await?;

    let reconnected = NatsClient::connect(config, &stable_instance).await?;
    let mut resumed_messages = reconnected
        .subscribe(
            "redelivery",
            &config.update_subject,
            Duration::from_millis(100),
        )
        .await?;
    let redelivered = next_message_with_timeout(
        &mut resumed_messages,
        "reconnected durable consumer",
        Duration::from_secs(10),
    )
    .await?;
    let redelivery_info = redelivered.info()?;
    if redelivered.payload.as_ref() != b"unacked-update"
        || redelivery_info.consumer != durable_name
        || redelivery_info.delivered < 2
    {
        return Err(test_error(
            "stable durable consumer did not receive the unacked message as a redelivery",
        ));
    }
    acknowledge(redelivered, "reconnected durable consumer").await?;
    drop(resumed_messages);
    reconnected.shutdown(Duration::from_secs(3)).await?;
    publisher.shutdown(Duration::from_secs(3)).await?;
    Ok(())
}

async fn next_message(
    messages: &mut jetstream::consumer::pull::Stream,
    consumer: &'static str,
) -> TestResult<jetstream::Message> {
    next_message_with_timeout(messages, consumer, Duration::from_secs(5)).await
}

async fn next_message_with_timeout(
    messages: &mut jetstream::consumer::pull::Stream,
    consumer: &'static str,
    maximum_wait: Duration,
) -> TestResult<jetstream::Message> {
    tokio::time::timeout(maximum_wait, messages.next())
        .await
        .map_err(|_| test_error(format!("{consumer} timed out")))?
        .ok_or_else(|| test_error(format!("{consumer} stopped")))?
        .map_err(Into::into)
}

async fn acknowledge(message: jetstream::Message, consumer: &'static str) -> TestResult {
    tokio::time::timeout(Duration::from_secs(5), message.double_ack())
        .await
        .map_err(|_| test_error(format!("{consumer} acknowledgement timed out")))??;
    Ok(())
}

struct NatsFixture {
    client: async_nats::Client,
    context: jetstream::Context,
    stream: String,
    permission_stream: String,
    config: NatsConfig,
}

impl NatsFixture {
    async fn new(url: &str, purpose: &str) -> TestResult<Self> {
        let client = async_nats::connect(url).await?;
        let context = jetstream::new(client.clone());
        let suffix = Uuid::now_v7().simple().to_string();
        let stream = format!("KC_DISTRIBUTED_{purpose}_{suffix}").to_uppercase();
        let permission_stream =
            format!("KC_DISTRIBUTED_PERMISSIONS_{purpose}_{suffix}").to_uppercase();
        let config = NatsConfig {
            servers: vec![url.to_owned()],
            name: format!("knowledge-core.collaboration.{purpose}-test"),
            stream: stream.clone(),
            permission_stream: permission_stream.clone(),
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
        Ok(Self {
            client,
            context,
            stream,
            permission_stream,
            config,
        })
    }

    async fn finish(self, contract: TestResult) -> TestResult {
        let delete_documents = self.context.delete_stream(&self.stream).await;
        let delete_permissions = self.context.delete_stream(&self.permission_stream).await;
        let flush = self.client.flush().await;
        contract?;
        delete_documents?;
        delete_permissions?;
        flush?;
        Ok(())
    }
}

#[tokio::test]
async fn actor_reconnect_replays_persisted_state_and_deduplicates_update() {
    let initial_state = richtext::initial_state();
    let store = Arc::new(ReplayStore::new(initial_state.clone()));
    let registry = replay_registry(store.clone());
    let document_id = DocumentId::new();
    let mut writer = registry
        .connect(
            &request_context(),
            document_id,
            actor_session(Access::Editor, 1),
        )
        .await
        .expect("writer connection");
    receive_binary(&mut writer, "writer initial sync").await;

    let update = append_paragraph_update(&initial_state);
    let frame = Message::Sync(SyncMessage::Update(update)).encode_v1();
    writer.send(frame.clone()).await.expect("committed update");
    assert_eq!(store.sequence().await, 1);
    writer.send(frame).await.expect("idempotent update replay");
    assert_eq!(store.sequence().await, 1);
    let expected = store.projection().await;

    drop(writer);
    wait_until_no_actors(&registry).await;

    let mut reconnected = registry
        .connect(
            &request_context(),
            document_id,
            actor_session(Access::Viewer, 2),
        )
        .await
        .expect("reconnected observer");
    receive_binary(&mut reconnected, "observer initial sync").await;

    let client = Doc::new();
    let state_vector = client.transact().state_vector();
    reconnected
        .send(Message::Sync(SyncMessage::SyncStep1(state_vector)).encode_v1())
        .await
        .expect("client sync step one");
    let response = receive_binary(&mut reconnected, "server sync step two").await;
    apply_sync_response(&client, &response);
    assert_eq!(
        richtext::projection_from_state(&richtext::full_state(&client))
            .expect("reconnected client projection"),
        expected
    );

    drop(reconnected);
    registry
        .shutdown(Duration::from_secs(1))
        .await
        .expect("registry shutdown");
}

struct ReplayStore {
    current: Mutex<LoadedDocument>,
    updates: Mutex<Vec<StoredUpdate>>,
}

impl ReplayStore {
    fn new(initial_state: Vec<u8>) -> Self {
        Self {
            current: Mutex::new(LoadedDocument {
                generation: 1,
                sequence: 0,
                state: initial_state,
            }),
            updates: Mutex::new(Vec::new()),
        }
    }

    async fn sequence(&self) -> i64 {
        self.current.lock().await.sequence
    }

    async fn projection(&self) -> knowledge_core_collaboration::domain::Projection {
        richtext::projection_from_state(&self.current.lock().await.state)
            .expect("persisted projection")
    }
}

#[async_trait]
impl DocumentStore for ReplayStore {
    async fn initialize_document(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
    ) -> Result<()> {
        Ok(())
    }

    async fn load_document(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
    ) -> Result<LoadedDocument> {
        Ok(self.current.lock().await.clone())
    }

    async fn append_update(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
        update: &[u8],
        _actor: &PublicUser,
        limits: UpdateLimits,
    ) -> Result<CommittedUpdate> {
        let mut current = self.current.lock().await;
        let candidate = richtext::candidate_from_update(
            &current.state,
            update,
            limits.maximum_update_bytes,
            limits.maximum_document_bytes,
        )?;
        let Some((document, projection)) = candidate else {
            return Ok(CommittedUpdate {
                generation: current.generation,
                sequence: current.sequence,
                state: current.state.clone(),
                projection: richtext::projection_from_state(&current.state)?,
                update: None,
            });
        };
        let generation = current.generation;
        let sequence = current.sequence + 1;
        let state = richtext::full_state(&document);
        *current = LoadedDocument {
            generation,
            sequence,
            state: state.clone(),
        };
        drop(current);
        self.updates.lock().await.push(StoredUpdate {
            generation,
            sequence,
            update: update.to_vec(),
        });
        Ok(CommittedUpdate {
            generation,
            sequence,
            state,
            projection,
            update: Some(update.to_vec()),
        })
    }

    async fn commit_restoration(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
        _candidate: RestorationCandidate<'_>,
    ) -> Result<RestoreVersion> {
        Err(ServiceError::internal(anyhow::anyhow!(
            "restoration is outside the reconnect test contract"
        )))
    }

    async fn updates_after(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
        sequence: i64,
        limit: i64,
    ) -> Result<Vec<StoredUpdate>> {
        Ok(self
            .updates
            .lock()
            .await
            .iter()
            .filter(|update| update.sequence > sequence)
            .take(usize::try_from(limit).unwrap_or(usize::MAX))
            .cloned()
            .collect())
    }

    async fn current_sequence(
        &self,
        _context: &RequestContext,
        _document_id: DocumentId,
    ) -> Result<i64> {
        Ok(self.sequence().await)
    }
}

fn replay_registry(store: Arc<ReplayStore>) -> ActorRegistry {
    let actor = ActorConfig {
        command_capacity: 8,
        outbound_capacity: 8,
        idle_timeout: Duration::from_millis(20),
        command_timeout: Duration::from_secs(1),
        awareness_messages_per_second: 20,
        updates_per_second: 50,
    };
    let public = PublicConfig {
        address: "127.0.0.1:0".parse().expect("public address"),
        allowed_origins: vec!["http://localhost:3000".to_owned()],
        max_frame_bytes: 1024 * 1024,
        max_update_bytes: 1024 * 1024,
        max_document_bytes: 4 * 1024 * 1024,
        max_awareness_bytes: 64 * 1024,
        max_connections: 32,
        max_connections_per_document: 8,
        handshakes_per_second: 100,
        handshake_burst: 100,
        handshake_timeout: Duration::from_secs(1),
    };
    let limits = ActorLimits::from_config(&actor, &public).expect("actor limits");
    ActorRegistry::new(
        store,
        limits,
        Metrics::new().expect("metrics"),
        CancellationToken::new(),
    )
}

fn actor_session(access: Access, id: i64) -> ActorSession {
    ActorSession {
        actor: PublicUser {
            id,
            username: format!("distributed-user-{id}"),
            avatar: String::new(),
        },
        access,
        permission_revision: 1,
        expires_at: time::OffsetDateTime::now_utc() + time::Duration::hours(1),
    }
}

fn request_context() -> RequestContext {
    let mut context = RequestContext::new(Uuid::now_v7().simple().to_string());
    context.deadline = std::time::Instant::now().checked_add(Duration::from_secs(1));
    context
}

async fn receive_binary(
    connection: &mut knowledge_core_collaboration::actor::ActorConnection,
    operation: &'static str,
) -> Vec<u8> {
    match tokio::time::timeout(Duration::from_secs(1), connection.recv())
        .await
        .expect(operation)
    {
        ConnectionEvent::Binary(payload) => payload,
        ConnectionEvent::Close(close) => panic!("{operation} closed with {}", close.reason),
    }
}

async fn wait_until_no_actors(registry: &ActorRegistry) {
    tokio::time::timeout(Duration::from_secs(1), async {
        while registry.active_documents().await != 0 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("actor idle eviction");
}

fn append_paragraph_update(state: &[u8]) -> Vec<u8> {
    let document = richtext::document_from_state(state).expect("initial document");
    let before = document.transact().state_vector();
    let fragment = document.get_or_insert_xml_fragment(richtext::FRAGMENT_NAME);
    fragment.push_back(
        &mut document.transact_mut(),
        XmlElementPrelim::empty("paragraph"),
    );
    document.transact().encode_state_as_update_v1(&before)
}

fn apply_sync_response(client: &Doc, payload: &[u8]) {
    let mut decoder = DecoderV1::new(Cursor::new(payload));
    let messages = MessageReader::new(&mut decoder)
        .collect::<std::result::Result<Vec<_>, _>>()
        .expect("server y-sync response");
    let mut applied = false;
    for message in messages {
        if let Message::Sync(SyncMessage::SyncStep2(update) | SyncMessage::Update(update)) = message
        {
            client
                .transact_mut()
                .apply_update(Update::decode_v1(&update).expect("server update-v1"))
                .expect("apply server update");
            applied = true;
        }
    }
    assert!(applied, "server response did not contain a sync update");
}

fn nats_url() -> TestResult<Option<String>> {
    let required = real_dependencies_required()?;
    let url = env::var("COLLABORATION_TEST_NATS_URL")
        .ok()
        .filter(|value| !value.trim().is_empty());
    if required && url.is_none() {
        return Err(test_error(
            "COLLABORATION_TEST_NATS_URL is required when COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1",
        ));
    }
    Ok(url)
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

fn test_error(message: impl Into<String>) -> Box<dyn Error + Send + Sync> {
    Box::new(io::Error::other(message.into()))
}
