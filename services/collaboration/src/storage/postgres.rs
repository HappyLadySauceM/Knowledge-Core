use std::{
    fmt::Write as _,
    future::Future,
    str::FromStr,
    time::{Duration, Instant},
};

use anyhow::anyhow;
use async_trait::async_trait;
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{
    PgConnection, PgPool, Row,
    postgres::{PgConnectOptions, PgPoolOptions, PgSslMode},
};
use time::OffsetDateTime;
use tokio::time::timeout;
use uuid::Uuid;

use super::{
    CommittedUpdate, DocumentEvent, DocumentStore, EventSubjects, LoadedDocument, OutboxEvent,
    ProjectionJob, RestorationCandidate, RestoreVersion, StoredUpdate, UpdateLimits, VersionCursor,
    VersionPage, VersionStore, WorkerStore,
};
use crate::{
    config::PostgresConfig,
    domain::{DocumentId, DocumentVersion, PublicUser, RequestContext, VersionId, VersionKind},
    error::{ErrorCode, Result, ServiceError},
    richtext,
};

const MIGRATION_LOCK_KEY: i64 = 0x4b43_434f_4c4c_4142;
const IDEMPOTENCY_TTL: Duration = Duration::from_hours(24);
const MAX_PAGE_SIZE: i64 = 100;
const MAX_UPDATE_PAGE_SIZE: i64 = 10_000;

#[derive(Clone, Copy)]
struct Migration {
    version: i64,
    name: &'static str,
    sql: &'static str,
}

const MIGRATIONS: [Migration; 2] = [
    Migration {
        version: 1,
        name: "001_initial",
        sql: include_str!("../../migrations/001_initial.sql"),
    },
    Migration {
        version: 2,
        name: "002_worker_indexes",
        sql: include_str!("../../migrations/002_worker_indexes.sql"),
    },
];

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct OperationBudget {
    maximum_wait: Duration,
    request_deadline_limited: bool,
}

#[derive(Clone)]
pub struct PostgresStore {
    pool: PgPool,
    operation_timeout: Duration,
    subjects: EventSubjects,
}

#[derive(Debug)]
struct Head {
    generation: i64,
    current_sequence: i64,
    last_snapshot_sequence: i64,
    last_version_sequence: i64,
    last_automatic_version_at: Option<OffsetDateTime>,
    last_actor_id: Option<i64>,
    last_actor_username: Option<String>,
    last_actor_avatar: Option<String>,
}

struct NewVersion<'a> {
    document_id: DocumentId,
    generation: i64,
    sequence: i64,
    kind: VersionKind,
    label: Option<&'a str>,
    state: Vec<u8>,
    actor: &'a PublicUser,
    now: OffsetDateTime,
}

impl PostgresStore {
    /// Opens the bounded `PostgreSQL` pool, applies verified migrations, and checks connectivity.
    ///
    /// # Errors
    ///
    /// Returns an error for invalid connection settings, migration drift, timeout, or dependency
    /// failure.
    pub async fn open(config: &PostgresConfig, subjects: EventSubjects) -> Result<Self> {
        let mut options = PgConnectOptions::from_str(&config.url).map_err(|error| {
            ServiceError::invalid_input("COLLABORATION_POSTGRES_URL is invalid").with_source(error)
        })?;
        options = if config.tls.enabled {
            options.ssl_mode(PgSslMode::VerifyFull)
        } else {
            options.ssl_mode(PgSslMode::Disable)
        };
        if let Some(path) = &config.tls.ca_file {
            options = options.ssl_root_cert(path);
        }
        if let Some(path) = &config.tls.cert_file {
            options = options.ssl_client_cert(path);
        }
        if let Some(path) = &config.tls.key_file {
            options = options.ssl_client_key(path);
        }

        let pool = timeout(
            config.connect_timeout,
            PgPoolOptions::new()
                .max_connections(config.max_connections)
                .acquire_timeout(config.acquire_timeout)
                .connect_with(options),
        )
        .await
        .map_err(|_| dependency_timeout("connect PostgreSQL"))?
        .map_err(|error| storage_error(error, "connect PostgreSQL"))?;
        let store = Self {
            pool,
            operation_timeout: config.operation_timeout,
            subjects,
        };
        if let Err(error) = store.migrate().await {
            store.pool.close().await;
            return Err(error);
        }
        if let Err(error) = store.ping().await {
            store.pool.close().await;
            return Err(error);
        }
        Ok(store)
    }

    pub fn from_pool(pool: PgPool, operation_timeout: Duration, subjects: EventSubjects) -> Self {
        Self {
            pool,
            operation_timeout,
            subjects,
        }
    }

    pub async fn close(&self) {
        self.pool.close().await;
    }

    /// Checks that `PostgreSQL` accepts a bounded operation.
    ///
    /// # Errors
    ///
    /// Returns an unavailable or internal error when the query cannot complete.
    pub async fn ping(&self) -> Result<()> {
        self.timed("ping PostgreSQL", async {
            sqlx::query("SELECT 1")
                .execute(&self.pool)
                .await
                .map_err(|error| storage_error(error, "ping PostgreSQL"))?;
            Ok(())
        })
        .await
    }

    /// Applies the embedded schema migration under an advisory transaction lock.
    ///
    /// # Errors
    ///
    /// Returns a conflict for checksum drift or a dependency error when migration cannot commit.
    pub async fn migrate(&self) -> Result<()> {
        self.timed("migrate PostgreSQL", async {
            let mut transaction = self
                .pool
                .begin()
                .await
                .map_err(|error| storage_error(error, "begin migration transaction"))?;
            sqlx::query("SELECT pg_advisory_xact_lock($1)")
                .bind(MIGRATION_LOCK_KEY)
                .execute(&mut *transaction)
                .await
                .map_err(|error| storage_error(error, "lock Collaboration migration"))?;
            sqlx::query("CREATE SCHEMA IF NOT EXISTS collaboration")
                .execute(&mut *transaction)
                .await
                .map_err(|error| storage_error(error, "create Collaboration schema"))?;
            let ledger_exists: bool = sqlx::query_scalar(
                "SELECT EXISTS(
                   SELECT 1 FROM pg_catalog.pg_class AS relation
                   JOIN pg_catalog.pg_namespace AS namespace
                     ON namespace.oid = relation.relnamespace
                   WHERE namespace.nspname = 'collaboration'
                     AND relation.relname = 'schema_migrations'
                     AND relation.relkind IN ('r', 'p')
                 )",
            )
            .fetch_one(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "inspect Collaboration migration ledger"))?;
            if !ledger_exists {
                reject_unmanaged_schema(&mut transaction).await?;
                sqlx::query(
                    "CREATE TABLE collaboration.schema_migrations (
                       version bigint PRIMARY KEY,
                       name text NOT NULL,
                       checksum char(64) NOT NULL,
                       applied_at timestamptz NOT NULL DEFAULT now()
                     )",
                )
                .execute(&mut *transaction)
                .await
                .map_err(|error| storage_error(error, "create migration ledger"))?;
            }

            for migration in MIGRATIONS {
                apply_migration(&mut transaction, migration).await?;
            }
            transaction
                .commit()
                .await
                .map_err(|error| storage_error(error, "commit migration transaction"))?;
            Ok(())
        })
        .await
    }

    async fn timed<T>(
        &self,
        operation: &'static str,
        future: impl Future<Output = Result<T>>,
    ) -> Result<T> {
        timeout(self.operation_timeout, future)
            .await
            .map_err(|error| dependency_timeout_with_source(operation, anyhow!(error)))?
    }

    async fn timed_request<T>(
        &self,
        context: &RequestContext,
        operation: &'static str,
        future: impl Future<Output = Result<T>>,
    ) -> Result<T> {
        let budget = operation_budget(context, self.operation_timeout, operation)?;
        match timeout(budget.maximum_wait, future).await {
            Ok(result) => result,
            Err(error) if budget.request_deadline_limited => {
                Err(request_deadline_error(operation, anyhow!(error)))
            }
            Err(error) => Err(dependency_timeout_with_source(operation, anyhow!(error))),
        }
    }

    async fn initialize_document_inner(&self, document_id: DocumentId) -> Result<()> {
        let mut transaction = self
            .pool
            .begin()
            .await
            .map_err(|error| storage_error(error, "begin initialize document transaction"))?;
        ensure_document(&mut transaction, document_id).await?;
        transaction
            .commit()
            .await
            .map_err(|error| storage_error(error, "commit initialize document transaction"))?;
        Ok(())
    }

    async fn load_document_inner(&self, document_id: DocumentId) -> Result<LoadedDocument> {
        let mut connection = self
            .pool
            .acquire()
            .await
            .map_err(|error| storage_error(error, "acquire document connection"))?;
        let head = load_head(&mut connection, document_id, false).await?;
        let state = load_state(
            &mut connection,
            document_id,
            head.generation,
            head.current_sequence,
        )
        .await?;
        Ok(LoadedDocument {
            generation: head.generation,
            sequence: head.current_sequence,
            state,
        })
    }

    // Keeping the transactional writes contiguous makes the commit-before-apply invariant auditable.
    #[allow(clippy::too_many_lines)]
    async fn append_update_inner(
        &self,
        document_id: DocumentId,
        update: &[u8],
        actor: &PublicUser,
        limits: UpdateLimits,
    ) -> Result<CommittedUpdate> {
        actor.validate()?;
        let mut transaction = self
            .pool
            .begin()
            .await
            .map_err(|error| storage_error(error, "begin append update transaction"))?;
        ensure_document(&mut transaction, document_id).await?;
        let head = load_head(&mut transaction, document_id, true).await?;
        let current_state = load_state(
            &mut transaction,
            document_id,
            head.generation,
            head.current_sequence,
        )
        .await?;
        let candidate = richtext::candidate_from_update(
            &current_state,
            update,
            limits.maximum_update_bytes,
            limits.maximum_document_bytes,
        )?;
        let Some((document, projection)) = candidate else {
            let projection = richtext::projection_from_state(&current_state)?;
            transaction
                .commit()
                .await
                .map_err(|error| storage_error(error, "commit duplicate update transaction"))?;
            return Ok(CommittedUpdate {
                generation: head.generation,
                sequence: head.current_sequence,
                state: current_state,
                projection,
                update: None,
            });
        };
        let state = richtext::full_state(&document);
        drop(document);
        let sequence = head
            .current_sequence
            .checked_add(1)
            .ok_or_else(|| ServiceError::internal(anyhow!("document sequence overflow")))?;
        let now = OffsetDateTime::now_utc();
        sqlx::query(
            "INSERT INTO collaboration.updates(
               document_id, generation, sequence, update, actor_id, created_at
             ) VALUES ($1, $2, $3, $4, $5, $6)",
        )
        .bind(document_id.as_uuid())
        .bind(head.generation)
        .bind(sequence)
        .bind(update)
        .bind(actor.id)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(|error| storage_error(error, "insert document update"))?;
        sqlx::query(
            "UPDATE collaboration.documents
             SET current_sequence = $2, last_actor_id = $3, last_actor_username = $4,
                 last_actor_avatar = $5, updated_at = $6
             WHERE document_id = $1",
        )
        .bind(document_id.as_uuid())
        .bind(sequence)
        .bind(actor.id)
        .bind(&actor.username)
        .bind(&actor.avatar)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(|error| storage_error(error, "advance document head"))?;
        enqueue_projection(
            &mut transaction,
            document_id,
            head.generation,
            sequence,
            now,
        )
        .await?;
        insert_event(
            &mut transaction,
            &self.subjects.updated,
            "updated",
            DocumentEvent {
                document_id,
                generation: head.generation,
                sequence,
                actor_id: Some(actor.id),
            },
            now,
        )
        .await?;
        transaction
            .commit()
            .await
            .map_err(|error| storage_error(error, "commit document update"))?;
        Ok(CommittedUpdate {
            generation: head.generation,
            sequence,
            state,
            projection,
            update: Some(update.to_vec()),
        })
    }

    // Restoration is one linear transaction: candidate validation, baseline, update, version,
    // head, projection, idempotency, and outbox.
    #[allow(clippy::too_many_lines)]
    async fn commit_restoration_inner(
        &self,
        document_id: DocumentId,
        candidate: RestorationCandidate<'_>,
    ) -> Result<RestoreVersion> {
        let mut transaction = self
            .pool
            .begin()
            .await
            .map_err(|error| storage_error(error, "begin restoration transaction"))?;
        let head = load_head(&mut transaction, document_id, true).await?;
        let operation = format!("restore_version:{document_id}");
        let request_hash = request_hash(&json!({
            "document_id": document_id,
            "version_id": candidate.target.id,
            "expected_sequence": candidate.expected_sequence,
        }))?;
        if let Some(version) = idempotent_version(
            &mut transaction,
            document_id,
            candidate.actor.id,
            &operation,
            candidate.idempotency_key,
            &request_hash,
        )
        .await?
        {
            transaction.commit().await.map_err(|error| {
                storage_error(error, "commit idempotent restoration transaction")
            })?;
            return Ok(RestoreVersion {
                version,
                committed: None,
            });
        }
        if head.generation != candidate.baseline_generation
            || head.current_sequence != candidate.baseline_sequence
            || head.current_sequence != candidate.expected_sequence
        {
            return Err(ServiceError::precondition_failed());
        }

        let stored_target = get_version(&mut transaction, document_id, candidate.target.id).await?;
        if stored_target.state != candidate.target.state {
            return Err(ServiceError::internal(anyhow!(
                "restoration target changed after it was loaded"
            )));
        }

        let current_state = load_state(
            &mut transaction,
            document_id,
            head.generation,
            head.current_sequence,
        )
        .await?;
        let target_projection =
            richtext::projection_from_state(&stored_target.state).map_err(|error| {
                ServiceError::internal(
                    anyhow::Error::new(error).context("project restoration target version"),
                )
            })?;
        let validated = richtext::candidate_from_update(
            &current_state,
            candidate.update,
            candidate.limits.maximum_update_bytes,
            candidate.limits.maximum_document_bytes,
        )?;
        let (state, projection, update) = if let Some((document, projection)) = validated {
            let state = richtext::full_state(&document);
            drop(document);
            (state, projection, Some(candidate.update.to_vec()))
        } else {
            let projection = richtext::projection_from_state(&current_state)?;
            (current_state.clone(), projection, None)
        };
        if projection != target_projection {
            return Err(ServiceError::internal(anyhow!(
                "restoration update does not produce the target version projection"
            )));
        }

        let generation = head
            .generation
            .checked_add(1)
            .ok_or_else(|| ServiceError::internal(anyhow!("document generation overflow")))?;
        let sequence = if update.is_some() {
            head.current_sequence
                .checked_add(1)
                .ok_or_else(|| ServiceError::internal(anyhow!("document sequence overflow")))?
        } else {
            head.current_sequence
        };
        let now = OffsetDateTime::now_utc();

        sqlx::query(
            "INSERT INTO collaboration.snapshots(
               document_id, generation, sequence, state, created_at
             ) VALUES ($1, $2, $3, $4, $5)
             ON CONFLICT (document_id, sequence) DO UPDATE SET
               generation = EXCLUDED.generation, state = EXCLUDED.state,
               created_at = EXCLUDED.created_at",
        )
        .bind(document_id.as_uuid())
        .bind(generation)
        .bind(head.current_sequence)
        .bind(&current_state)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(|error| storage_error(error, "insert restoration baseline"))?;
        if let Some(update) = &update {
            sqlx::query(
                "INSERT INTO collaboration.updates(
                   document_id, generation, sequence, update, actor_id, created_at
                 ) VALUES ($1, $2, $3, $4, $5, $6)",
            )
            .bind(document_id.as_uuid())
            .bind(generation)
            .bind(sequence)
            .bind(update)
            .bind(candidate.actor.id)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "insert restoration update"))?;
        }
        let label = format!("Restored from {}", candidate.target.id);
        let version = insert_version(
            &mut transaction,
            NewVersion {
                document_id,
                generation,
                sequence,
                kind: VersionKind::Restoration,
                label: Some(&label),
                state: state.clone(),
                actor: candidate.actor,
                now,
            },
        )
        .await?;
        sqlx::query(
            "UPDATE collaboration.documents
             SET generation = $2, current_sequence = $3,
                 last_snapshot_sequence = $4, last_version_sequence = $3,
                 last_actor_id = $5, last_actor_username = $6,
                 last_actor_avatar = $7, updated_at = $8
             WHERE document_id = $1",
        )
        .bind(document_id.as_uuid())
        .bind(generation)
        .bind(sequence)
        .bind(head.current_sequence)
        .bind(candidate.actor.id)
        .bind(&candidate.actor.username)
        .bind(&candidate.actor.avatar)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(|error| storage_error(error, "advance restored document head"))?;
        enqueue_projection(&mut transaction, document_id, generation, sequence, now).await?;
        insert_event(
            &mut transaction,
            &self.subjects.invalidated,
            "restored",
            DocumentEvent {
                document_id,
                generation,
                sequence,
                actor_id: Some(candidate.actor.id),
            },
            now,
        )
        .await?;
        save_idempotency(
            &mut transaction,
            candidate.actor.id,
            &operation,
            candidate.idempotency_key,
            &request_hash,
            version.id,
        )
        .await?;
        transaction
            .commit()
            .await
            .map_err(|error| storage_error(error, "commit restoration transaction"))?;
        Ok(RestoreVersion {
            version,
            committed: Some(CommittedUpdate {
                generation,
                sequence,
                state,
                projection,
                update,
            }),
        })
    }
}

#[async_trait]
impl DocumentStore for PostgresStore {
    async fn initialize_document(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<()> {
        self.timed_request(
            context,
            "initialize document",
            self.initialize_document_inner(document_id),
        )
        .await
    }

    async fn load_document(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<LoadedDocument> {
        self.timed_request(
            context,
            "load document",
            self.load_document_inner(document_id),
        )
        .await
    }

    async fn append_update(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        update: &[u8],
        actor: &PublicUser,
        limits: UpdateLimits,
    ) -> Result<CommittedUpdate> {
        self.timed_request(
            context,
            "append document update",
            self.append_update_inner(document_id, update, actor, limits),
        )
        .await
    }

    async fn commit_restoration(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        candidate: RestorationCandidate<'_>,
    ) -> Result<RestoreVersion> {
        candidate.actor.validate()?;
        if candidate.target.document_id != document_id {
            return Err(ServiceError::invalid_input(
                "restoration target belongs to another document",
            ));
        }
        if candidate.baseline_generation <= 0
            || candidate.baseline_sequence < 0
            || candidate.expected_sequence < 0
        {
            return Err(ServiceError::invalid_input(
                "restoration precondition is invalid",
            ));
        }
        validate_idempotency_key(candidate.idempotency_key)?;
        self.timed_request(
            context,
            "commit document restoration",
            self.commit_restoration_inner(document_id, candidate),
        )
        .await
    }

    async fn updates_after(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        sequence: i64,
        limit: i64,
    ) -> Result<Vec<StoredUpdate>> {
        if sequence < 0 || !(1..=MAX_UPDATE_PAGE_SIZE).contains(&limit) {
            return Err(ServiceError::invalid_input(
                "update cursor or page size is invalid",
            ));
        }
        self.timed_request(context, "load document updates", async {
            let rows = sqlx::query(
                "SELECT generation, sequence, update FROM collaboration.updates
                 WHERE document_id = $1 AND sequence > $2
                 ORDER BY sequence ASC LIMIT $3",
            )
            .bind(document_id.as_uuid())
            .bind(sequence)
            .bind(limit)
            .fetch_all(&self.pool)
            .await
            .map_err(|error| storage_error(error, "load document updates"))?;
            rows.into_iter()
                .map(|row| {
                    Ok(StoredUpdate {
                        generation: row
                            .try_get("generation")
                            .map_err(|error| storage_error(error, "decode update generation"))?,
                        sequence: row
                            .try_get("sequence")
                            .map_err(|error| storage_error(error, "decode update sequence"))?,
                        update: row
                            .try_get("update")
                            .map_err(|error| storage_error(error, "decode update payload"))?,
                    })
                })
                .collect()
        })
        .await
    }

    async fn current_sequence(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<i64> {
        self.timed_request(context, "load current sequence", async {
            let row = sqlx::query(
                "SELECT current_sequence FROM collaboration.documents WHERE document_id = $1",
            )
            .bind(document_id.as_uuid())
            .fetch_optional(&self.pool)
            .await
            .map_err(|error| storage_error(error, "load current sequence"))?
            .ok_or_else(|| ServiceError::not_found("collaborative document not found"))?;
            row.try_get("current_sequence")
                .map_err(|error| storage_error(error, "decode current sequence"))
        })
        .await
    }
}

#[async_trait]
impl VersionStore for PostgresStore {
    async fn create_manual_version(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        actor: &PublicUser,
        label: Option<&str>,
        idempotency_key: Option<&str>,
    ) -> Result<DocumentVersion> {
        actor.validate()?;
        let label = validate_label(label)?;
        validate_idempotency_key(idempotency_key)?;
        self.timed_request(context, "create manual version", async {
            let mut transaction =
                self.pool.begin().await.map_err(|error| {
                    storage_error(error, "begin create manual version transaction")
                })?;
            ensure_document(&mut transaction, document_id).await?;
            let head = load_head(&mut transaction, document_id, true).await?;
            let operation = format!("create_version:{document_id}");
            let request_hash = request_hash(&json!({
                "document_id": document_id,
                "label": label,
            }))?;
            if let Some(version) = idempotent_version(
                &mut transaction,
                document_id,
                actor.id,
                &operation,
                idempotency_key,
                &request_hash,
            )
            .await?
            {
                transaction.commit().await.map_err(|error| {
                    storage_error(error, "commit idempotent manual version transaction")
                })?;
                return Ok(version);
            }
            let state = load_state(
                &mut transaction,
                document_id,
                head.generation,
                head.current_sequence,
            )
            .await?;
            let version = insert_version(
                &mut transaction,
                NewVersion {
                    document_id,
                    generation: head.generation,
                    sequence: head.current_sequence,
                    kind: VersionKind::Manual,
                    label: label.as_deref(),
                    state,
                    actor,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await?;
            sqlx::query(
                "UPDATE collaboration.documents
                 SET last_version_sequence = GREATEST(last_version_sequence, $2),
                     updated_at = GREATEST(updated_at, $3)
                 WHERE document_id = $1",
            )
            .bind(document_id.as_uuid())
            .bind(head.current_sequence)
            .bind(version.created_at)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "advance manual version watermark"))?;
            save_idempotency(
                &mut transaction,
                actor.id,
                &operation,
                idempotency_key,
                &request_hash,
                version.id,
            )
            .await?;
            transaction.commit().await.map_err(|error| {
                storage_error(error, "commit create manual version transaction")
            })?;
            Ok(version)
        })
        .await
    }

    async fn list_versions(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        cursor: Option<&VersionCursor>,
        limit: i64,
    ) -> Result<VersionPage> {
        if !(1..=MAX_PAGE_SIZE).contains(&limit) {
            return Err(ServiceError::invalid_input(
                "version page size must be between 1 and 100",
            ));
        }
        self.timed_request(context, "list document versions", async {
            let rows = if let Some(cursor) = cursor {
                sqlx::query(
                    "SELECT id, document_id, generation, sequence, kind, label, state,
                            created_by_id, created_by_username, created_by_avatar, created_at
                     FROM collaboration.versions
                     WHERE document_id = $1 AND (created_at, id) < ($2, $3)
                     ORDER BY created_at DESC, id DESC LIMIT $4",
                )
                .bind(document_id.as_uuid())
                .bind(cursor.created_at)
                .bind(cursor.id.as_uuid())
                .bind(limit + 1)
                .fetch_all(&self.pool)
                .await
            } else {
                sqlx::query(
                    "SELECT id, document_id, generation, sequence, kind, label, state,
                            created_by_id, created_by_username, created_by_avatar, created_at
                     FROM collaboration.versions
                     WHERE document_id = $1
                     ORDER BY created_at DESC, id DESC LIMIT $2",
                )
                .bind(document_id.as_uuid())
                .bind(limit + 1)
                .fetch_all(&self.pool)
                .await
            }
            .map_err(|error| storage_error(error, "list document versions"))?;
            let has_more = i64::try_from(rows.len()).is_ok_and(|count| count > limit);
            let items = rows
                .into_iter()
                .take(usize::try_from(limit).map_err(|error| {
                    ServiceError::internal(anyhow!(error).context("convert version page size"))
                })?)
                .map(|row| version_from_row(&row))
                .collect::<Result<Vec<_>>>()?;
            Ok(VersionPage { items, has_more })
        })
        .await
    }

    async fn get_version(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        version_id: VersionId,
    ) -> Result<DocumentVersion> {
        self.timed_request(context, "get document version", async {
            let mut connection = self
                .pool
                .acquire()
                .await
                .map_err(|error| storage_error(error, "acquire version connection"))?;
            get_version(&mut connection, document_id, version_id).await
        })
        .await
    }

    async fn purge_document(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<()> {
        self.timed_request(context, "purge collaborative document", async {
            let mut transaction = self
                .pool
                .begin()
                .await
                .map_err(|error| storage_error(error, "begin purge document transaction"))?;
            let head = load_head(&mut transaction, document_id, true).await?;
            let generation = head
                .generation
                .checked_add(1)
                .ok_or_else(|| ServiceError::internal(anyhow!("document generation overflow")))?;
            let now = OffsetDateTime::now_utc();
            sqlx::query(
                "DELETE FROM collaboration.idempotency_keys
                 WHERE resource_id IN (
                   SELECT id FROM collaboration.versions WHERE document_id = $1
                 )",
            )
            .bind(document_id.as_uuid())
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "delete document idempotency keys"))?;
            sqlx::query("DELETE FROM collaboration.updates WHERE document_id = $1")
                .bind(document_id.as_uuid())
                .execute(&mut *transaction)
                .await
                .map_err(|error| storage_error(error, "delete document updates"))?;
            sqlx::query("DELETE FROM collaboration.snapshots WHERE document_id = $1")
                .bind(document_id.as_uuid())
                .execute(&mut *transaction)
                .await
                .map_err(|error| storage_error(error, "delete document snapshots"))?;
            sqlx::query("DELETE FROM collaboration.versions WHERE document_id = $1")
                .bind(document_id.as_uuid())
                .execute(&mut *transaction)
                .await
                .map_err(|error| storage_error(error, "delete document versions"))?;
            sqlx::query("DELETE FROM collaboration.projection_jobs WHERE document_id = $1")
                .bind(document_id.as_uuid())
                .execute(&mut *transaction)
                .await
                .map_err(|error| storage_error(error, "delete document projection job"))?;
            let state = richtext::initial_state();
            sqlx::query(
                "INSERT INTO collaboration.snapshots(
                   document_id, generation, sequence, state, created_at
                 ) VALUES ($1, $2, $3, $4, $5)",
            )
            .bind(document_id.as_uuid())
            .bind(generation)
            .bind(head.current_sequence)
            .bind(&state)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "insert purged document snapshot"))?;
            sqlx::query(
                "UPDATE collaboration.documents
                 SET generation = $2, last_snapshot_sequence = current_sequence,
                     last_version_sequence = current_sequence,
                     last_automatic_version_at = NULL, last_actor_id = NULL,
                     last_actor_username = NULL, last_actor_avatar = NULL, updated_at = $3
                 WHERE document_id = $1",
            )
            .bind(document_id.as_uuid())
            .bind(generation)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "reset purged document head"))?;
            enqueue_projection(
                &mut transaction,
                document_id,
                generation,
                head.current_sequence,
                now,
            )
            .await?;
            insert_event(
                &mut transaction,
                &self.subjects.invalidated,
                "purged",
                DocumentEvent {
                    document_id,
                    generation,
                    sequence: head.current_sequence,
                    actor_id: None,
                },
                now,
            )
            .await?;
            transaction
                .commit()
                .await
                .map_err(|error| storage_error(error, "commit purge document transaction"))?;
            Ok(())
        })
        .await
    }
}

#[async_trait]
impl WorkerStore for PostgresStore {
    async fn claim_projection_job(
        &self,
        context: &RequestContext,
        lease: Duration,
    ) -> Result<Option<ProjectionJob>> {
        let lease = positive_duration(lease, "projection lease")?;
        self.timed_request(context, "claim projection job", async {
            let candidate = sqlx::query(
                "SELECT document_id FROM collaboration.projection_jobs
                 WHERE next_attempt_at <= now()
                   AND (lease_until IS NULL OR lease_until <= now())
                 ORDER BY next_attempt_at, updated_at, document_id LIMIT 1",
            )
            .fetch_optional(&self.pool)
            .await
            .map_err(|error| storage_error(error, "find projection job"))?;
            let Some(candidate) = candidate else {
                return Ok(None);
            };
            let raw_document_id: Uuid = candidate
                .try_get("document_id")
                .map_err(|error| storage_error(error, "decode projection document id"))?;
            let document_id = DocumentId::parse(&raw_document_id.to_string())?;
            let mut transaction = self
                .pool
                .begin()
                .await
                .map_err(|error| storage_error(error, "begin claim projection transaction"))?;
            let head = load_head(&mut transaction, document_id, true).await?;
            let row = sqlx::query(
                "SELECT attempts FROM collaboration.projection_jobs
                 WHERE document_id = $1 AND next_attempt_at <= now()
                   AND (lease_until IS NULL OR lease_until <= now())
                 FOR UPDATE",
            )
            .bind(document_id.as_uuid())
            .fetch_optional(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "lock projection job"))?;
            let Some(row) = row else {
                transaction
                    .commit()
                    .await
                    .map_err(|error| storage_error(error, "commit empty projection claim"))?;
                return Ok(None);
            };
            let attempts = row
                .try_get("attempts")
                .map_err(|error| storage_error(error, "decode projection attempts"))?;
            let state = load_state(
                &mut transaction,
                document_id,
                head.generation,
                head.current_sequence,
            )
            .await?;
            let lease_until = OffsetDateTime::now_utc() + lease;
            sqlx::query(
                "UPDATE collaboration.projection_jobs
                 SET target_generation = $2, target_sequence = $3,
                     lease_until = $4, updated_at = now()
                 WHERE document_id = $1",
            )
            .bind(document_id.as_uuid())
            .bind(head.generation)
            .bind(head.current_sequence)
            .bind(lease_until)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "lease projection job"))?;
            transaction
                .commit()
                .await
                .map_err(|error| storage_error(error, "commit projection claim transaction"))?;
            Ok(Some(ProjectionJob {
                document_id,
                generation: head.generation,
                sequence: head.current_sequence,
                state,
                attempts,
            }))
        })
        .await
    }

    async fn complete_projection(
        &self,
        context: &RequestContext,
        job: &ProjectionJob,
    ) -> Result<()> {
        self.timed_request(context, "complete projection job", async {
            sqlx::query(
                "DELETE FROM collaboration.projection_jobs
                 WHERE document_id = $1 AND target_generation = $2 AND target_sequence = $3",
            )
            .bind(job.document_id.as_uuid())
            .bind(job.generation)
            .bind(job.sequence)
            .execute(&self.pool)
            .await
            .map_err(|error| storage_error(error, "complete projection job"))?;
            Ok(())
        })
        .await
    }

    async fn retry_projection(
        &self,
        context: &RequestContext,
        job: &ProjectionJob,
        error_key: &str,
    ) -> Result<()> {
        validate_error_key(error_key)?;
        let next_attempt = retry_deadline(job.attempts)?;
        self.timed_request(context, "retry projection job", async {
            sqlx::query(
                "UPDATE collaboration.projection_jobs
                 SET attempts = attempts + 1, next_attempt_at = $4, lease_until = NULL,
                     last_error_key = $5, updated_at = now()
                 WHERE document_id = $1 AND target_generation = $2 AND target_sequence = $3",
            )
            .bind(job.document_id.as_uuid())
            .bind(job.generation)
            .bind(job.sequence)
            .bind(next_attempt)
            .bind(error_key)
            .execute(&self.pool)
            .await
            .map_err(|error| storage_error(error, "retry projection job"))?;
            Ok(())
        })
        .await
    }

    // Candidate revalidation and snapshot replacement deliberately remain in one visible transaction.
    #[allow(clippy::too_many_lines)]
    async fn compact_next(
        &self,
        context: &RequestContext,
        update_threshold: i64,
        byte_threshold: i64,
    ) -> Result<bool> {
        if update_threshold <= 0 || byte_threshold <= 0 {
            return Err(ServiceError::invalid_input(
                "snapshot thresholds must be positive",
            ));
        }
        self.timed_request(context, "compact document snapshot", async {
            let candidate = sqlx::query(
                "SELECT document.document_id
                 FROM collaboration.documents AS document
                 CROSS JOIN LATERAL (
                   SELECT count(*) AS update_count,
                          COALESCE(sum(octet_length(stored_update.update)), 0)::bigint AS update_bytes
                   FROM collaboration.updates AS stored_update
                   WHERE stored_update.document_id = document.document_id
                     AND stored_update.generation = document.generation
                     AND stored_update.sequence > document.last_snapshot_sequence
                 ) AS pending
                 WHERE pending.update_count >= $1 OR pending.update_bytes >= $2
                 ORDER BY document.updated_at, document.document_id LIMIT 1",
            )
            .bind(update_threshold)
            .bind(byte_threshold)
            .fetch_optional(&self.pool)
            .await
            .map_err(|error| storage_error(error, "find snapshot candidate"))?;
            let Some(candidate) = candidate else {
                return Ok(false);
            };
            let raw_document_id: Uuid = candidate
                .try_get("document_id")
                .map_err(|error| storage_error(error, "decode snapshot document id"))?;
            let document_id = DocumentId::parse(&raw_document_id.to_string())?;
            let mut transaction = self
                .pool
                .begin()
                .await
                .map_err(|error| storage_error(error, "begin snapshot transaction"))?;
            let head = load_head(&mut transaction, document_id, true).await?;
            let totals = sqlx::query(
                "SELECT count(*) AS update_count,
                        COALESCE(sum(octet_length(update)), 0)::bigint AS update_bytes
                 FROM collaboration.updates
                 WHERE document_id = $1 AND generation = $2 AND sequence > $3",
            )
            .bind(document_id.as_uuid())
            .bind(head.generation)
            .bind(head.last_snapshot_sequence)
            .fetch_one(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "recheck snapshot threshold"))?;
            let update_count: i64 = totals
                .try_get("update_count")
                .map_err(|error| storage_error(error, "decode snapshot update count"))?;
            let update_bytes: i64 = totals
                .try_get("update_bytes")
                .map_err(|error| storage_error(error, "decode snapshot update bytes"))?;
            if update_count < update_threshold && update_bytes < byte_threshold {
                transaction
                    .commit()
                    .await
                    .map_err(|error| storage_error(error, "commit skipped snapshot transaction"))?;
                return Ok(false);
            }
            let state = load_state(
                &mut transaction,
                document_id,
                head.generation,
                head.current_sequence,
            )
            .await?;
            let now = OffsetDateTime::now_utc();
            sqlx::query(
                "INSERT INTO collaboration.snapshots(
                   document_id, generation, sequence, state, created_at
                 ) VALUES ($1, $2, $3, $4, $5)
                 ON CONFLICT (document_id, sequence) DO UPDATE SET
                   generation = EXCLUDED.generation, state = EXCLUDED.state,
                   created_at = EXCLUDED.created_at",
            )
            .bind(document_id.as_uuid())
            .bind(head.generation)
            .bind(head.current_sequence)
            .bind(state)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "write document snapshot"))?;
            sqlx::query(
                "UPDATE collaboration.documents SET last_snapshot_sequence = $2
                 WHERE document_id = $1",
            )
            .bind(document_id.as_uuid())
            .bind(head.current_sequence)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "advance snapshot watermark"))?;
            sqlx::query(
                "DELETE FROM collaboration.updates
                 WHERE document_id = $1 AND generation = $2 AND sequence <= $3",
            )
            .bind(document_id.as_uuid())
            .bind(head.generation)
            .bind(head.current_sequence)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "prune compacted updates"))?;
            transaction
                .commit()
                .await
                .map_err(|error| storage_error(error, "commit snapshot transaction"))?;
            Ok(true)
        })
        .await
    }

    async fn create_automatic_version(
        &self,
        context: &RequestContext,
        interval: Duration,
    ) -> Result<bool> {
        let interval = positive_duration(interval, "automatic version interval")?;
        self.timed_request(context, "create automatic version", async {
            let threshold = OffsetDateTime::now_utc() - interval;
            let candidate = sqlx::query(
                "SELECT document_id FROM collaboration.documents
                 WHERE current_sequence > last_version_sequence
                   AND last_actor_id IS NOT NULL
                   AND (last_automatic_version_at IS NULL OR last_automatic_version_at <= $1)
                 ORDER BY COALESCE(last_automatic_version_at, created_at), document_id LIMIT 1",
            )
            .bind(threshold)
            .fetch_optional(&self.pool)
            .await
            .map_err(|error| storage_error(error, "find automatic version candidate"))?;
            let Some(candidate) = candidate else {
                return Ok(false);
            };
            let raw_document_id: Uuid = candidate
                .try_get("document_id")
                .map_err(|error| storage_error(error, "decode automatic version document id"))?;
            let document_id = DocumentId::parse(&raw_document_id.to_string())?;
            let mut transaction = self
                .pool
                .begin()
                .await
                .map_err(|error| storage_error(error, "begin automatic version transaction"))?;
            let head = load_head(&mut transaction, document_id, true).await?;
            if head.current_sequence <= head.last_version_sequence
                || head
                    .last_automatic_version_at
                    .is_some_and(|last| last > threshold)
            {
                transaction.commit().await.map_err(|error| {
                    storage_error(error, "commit skipped automatic version transaction")
                })?;
                return Ok(false);
            }
            let actor = PublicUser {
                id: head.last_actor_id.ok_or_else(|| {
                    ServiceError::internal(anyhow!("automatic version actor is missing"))
                })?,
                username: head.last_actor_username.ok_or_else(|| {
                    ServiceError::internal(anyhow!("automatic version actor name is missing"))
                })?,
                avatar: head.last_actor_avatar.ok_or_else(|| {
                    ServiceError::internal(anyhow!("automatic version actor avatar is missing"))
                })?,
            };
            actor.validate()?;
            let state = load_state(
                &mut transaction,
                document_id,
                head.generation,
                head.current_sequence,
            )
            .await?;
            let now = OffsetDateTime::now_utc();
            insert_version(
                &mut transaction,
                NewVersion {
                    document_id,
                    generation: head.generation,
                    sequence: head.current_sequence,
                    kind: VersionKind::Automatic,
                    label: None,
                    state,
                    actor: &actor,
                    now,
                },
            )
            .await?;
            sqlx::query(
                "UPDATE collaboration.documents
                 SET last_version_sequence = $2, last_automatic_version_at = $3,
                     updated_at = GREATEST(updated_at, $3)
                 WHERE document_id = $1",
            )
            .bind(document_id.as_uuid())
            .bind(head.current_sequence)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(|error| storage_error(error, "advance automatic version watermark"))?;
            transaction
                .commit()
                .await
                .map_err(|error| storage_error(error, "commit automatic version transaction"))?;
            Ok(true)
        })
        .await
    }

    async fn claim_outbox(
        &self,
        context: &RequestContext,
        batch_size: i64,
        lease: Duration,
    ) -> Result<Vec<OutboxEvent>> {
        if !(1..=1_000).contains(&batch_size) {
            return Err(ServiceError::invalid_input(
                "outbox batch size must be between 1 and 1000",
            ));
        }
        let lease_until = OffsetDateTime::now_utc() + positive_duration(lease, "outbox lease")?;
        self.timed_request(context, "claim outbox events", async {
            let rows = sqlx::query(
                "WITH selected AS (
                   SELECT id FROM collaboration.outbox
                   WHERE published_at IS NULL AND next_attempt_at <= now()
                     AND (lease_until IS NULL OR lease_until <= now())
                   ORDER BY next_attempt_at, created_at, id
                   FOR UPDATE SKIP LOCKED LIMIT $1
                 )
                 UPDATE collaboration.outbox AS event
                 SET lease_until = $2
                 FROM selected WHERE event.id = selected.id
                 RETURNING event.id, event.event_key, event.subject, event.payload, event.attempts",
            )
            .bind(batch_size)
            .bind(lease_until)
            .fetch_all(&self.pool)
            .await
            .map_err(|error| storage_error(error, "claim outbox events"))?;
            rows.into_iter()
                .map(|row| {
                    Ok(OutboxEvent {
                        id: row
                            .try_get("id")
                            .map_err(|error| storage_error(error, "decode outbox id"))?,
                        event_key: row
                            .try_get("event_key")
                            .map_err(|error| storage_error(error, "decode outbox event key"))?,
                        subject: row
                            .try_get("subject")
                            .map_err(|error| storage_error(error, "decode outbox subject"))?,
                        payload: row
                            .try_get("payload")
                            .map_err(|error| storage_error(error, "decode outbox payload"))?,
                        attempts: row
                            .try_get("attempts")
                            .map_err(|error| storage_error(error, "decode outbox attempts"))?,
                    })
                })
                .collect()
        })
        .await
    }

    async fn complete_outbox(&self, context: &RequestContext, id: Uuid) -> Result<()> {
        self.timed_request(context, "complete outbox event", async {
            sqlx::query(
                "UPDATE collaboration.outbox
                 SET published_at = now(), lease_until = NULL, last_error_key = ''
                 WHERE id = $1 AND published_at IS NULL",
            )
            .bind(id)
            .execute(&self.pool)
            .await
            .map_err(|error| storage_error(error, "complete outbox event"))?;
            Ok(())
        })
        .await
    }

    async fn retry_outbox(
        &self,
        context: &RequestContext,
        event: &OutboxEvent,
        error_key: &str,
    ) -> Result<()> {
        validate_error_key(error_key)?;
        let next_attempt = retry_deadline(event.attempts)?;
        self.timed_request(context, "retry outbox event", async {
            sqlx::query(
                "UPDATE collaboration.outbox
                 SET attempts = attempts + 1, next_attempt_at = $2,
                     lease_until = NULL, last_error_key = $3
                 WHERE id = $1 AND published_at IS NULL",
            )
            .bind(event.id)
            .bind(next_attempt)
            .bind(error_key)
            .execute(&self.pool)
            .await
            .map_err(|error| storage_error(error, "retry outbox event"))?;
            Ok(())
        })
        .await
    }

    async fn cleanup(&self, context: &RequestContext) -> Result<()> {
        self.timed_request(context, "cleanup Collaboration storage", async {
            let cutoff = OffsetDateTime::now_utc() - time::Duration::days(1);
            sqlx::query("DELETE FROM collaboration.idempotency_keys WHERE expires_at <= now()")
                .execute(&self.pool)
                .await
                .map_err(|error| storage_error(error, "delete expired idempotency keys"))?;
            sqlx::query(
                "DELETE FROM collaboration.outbox
                 WHERE published_at IS NOT NULL AND published_at <= $1",
            )
            .bind(cutoff)
            .execute(&self.pool)
            .await
            .map_err(|error| storage_error(error, "delete published outbox events"))?;
            Ok(())
        })
        .await
    }
}

async fn reject_unmanaged_schema(connection: &mut PgConnection) -> Result<()> {
    let relation = sqlx::query_scalar::<_, String>(
        "SELECT relation.relname
         FROM pg_catalog.pg_class AS relation
         JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
         WHERE namespace.nspname = 'collaboration'
           AND relation.relkind IN ('r', 'p', 'v', 'm', 'S', 'f', 'i')
           AND NOT (
             relation.relname = 'schema_migrations' AND relation.relkind IN ('r', 'p')
           )
           AND NOT (
             relation.relkind = 'i' AND EXISTS (
               SELECT 1
               FROM pg_catalog.pg_index AS migration_index
               JOIN pg_catalog.pg_class AS migration_table
                 ON migration_table.oid = migration_index.indrelid
               JOIN pg_catalog.pg_namespace AS migration_namespace
                 ON migration_namespace.oid = migration_table.relnamespace
               WHERE migration_index.indexrelid = relation.oid
                 AND migration_namespace.nspname = 'collaboration'
                 AND migration_table.relname = 'schema_migrations'
             )
           )
         ORDER BY relation.relname
         LIMIT 1",
    )
    .fetch_optional(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "inspect Collaboration schema ownership"))?;
    if relation.is_some() {
        return Err(ServiceError::conflict(
            "Collaboration schema contains unmanaged relations",
        ));
    }
    Ok(())
}

async fn apply_migration(connection: &mut PgConnection, migration: Migration) -> Result<()> {
    let checksum = migration_checksum(migration.sql);
    let applied = sqlx::query(
        "SELECT name, checksum FROM collaboration.schema_migrations WHERE version = $1",
    )
    .bind(migration.version)
    .fetch_optional(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "read migration ledger"))?;
    if let Some(row) = applied {
        let name: String = row
            .try_get("name")
            .map_err(|error| storage_error(error, "decode migration name"))?;
        let stored: String = row
            .try_get("checksum")
            .map_err(|error| storage_error(error, "decode migration checksum"))?;
        if name != migration.name || stored.trim_end() != checksum {
            return Err(ServiceError::conflict(
                "Collaboration database migration checksum does not match",
            ));
        }
        return Ok(());
    }
    if migration.version == 1 {
        reject_unmanaged_schema(connection).await?;
    }
    sqlx::raw_sql(migration.sql)
        .execute(&mut *connection)
        .await
        .map_err(|error| storage_error(error, "apply Collaboration migration"))?;
    sqlx::query(
        "INSERT INTO collaboration.schema_migrations(version, name, checksum)
         VALUES ($1, $2, $3)",
    )
    .bind(migration.version)
    .bind(migration.name)
    .bind(checksum)
    .execute(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "record Collaboration migration"))?;
    Ok(())
}

fn positive_duration(value: Duration, name: &'static str) -> Result<time::Duration> {
    if value.is_zero() {
        return Err(ServiceError::invalid_input(format!(
            "{name} must be greater than zero"
        )));
    }
    time::Duration::try_from(value).map_err(|error| {
        ServiceError::invalid_input(format!("{name} is too large")).with_source(error)
    })
}

fn retry_deadline(attempts: i32) -> Result<OffsetDateTime> {
    let exponent = u32::try_from(attempts.clamp(0, 8))
        .map_err(|error| ServiceError::internal(anyhow!(error).context("convert retry attempt")))?;
    let seconds = 1_i64
        .checked_shl(exponent)
        .ok_or_else(|| ServiceError::internal(anyhow!("retry delay overflow")))?;
    Ok(OffsetDateTime::now_utc() + time::Duration::seconds(seconds))
}

fn validate_error_key(value: &str) -> Result<()> {
    if value.is_empty()
        || value.len() > 64
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err(ServiceError::invalid_input("worker error key is invalid"));
    }
    Ok(())
}

async fn insert_version(
    connection: &mut PgConnection,
    version: NewVersion<'_>,
) -> Result<DocumentVersion> {
    let id = VersionId::new();
    sqlx::query(
        "INSERT INTO collaboration.versions(
           id, document_id, generation, sequence, kind, label, state,
           created_by_id, created_by_username, created_by_avatar, created_at
         ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)",
    )
    .bind(id.as_uuid())
    .bind(version.document_id.as_uuid())
    .bind(version.generation)
    .bind(version.sequence)
    .bind(version.kind.as_str())
    .bind(version.label)
    .bind(&version.state)
    .bind(version.actor.id)
    .bind(&version.actor.username)
    .bind(&version.actor.avatar)
    .bind(version.now)
    .execute(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "insert document version"))?;
    Ok(DocumentVersion {
        id,
        document_id: version.document_id,
        sequence: version.sequence,
        kind: version.kind,
        label: version.label.map(ToOwned::to_owned),
        state: version.state,
        created_by: version.actor.clone(),
        created_at: version.now,
    })
}

async fn get_version(
    connection: &mut PgConnection,
    document_id: DocumentId,
    version_id: VersionId,
) -> Result<DocumentVersion> {
    let row = sqlx::query(
        "SELECT id, document_id, generation, sequence, kind, label, state,
                created_by_id, created_by_username, created_by_avatar, created_at
         FROM collaboration.versions WHERE document_id = $1 AND id = $2",
    )
    .bind(document_id.as_uuid())
    .bind(version_id.as_uuid())
    .fetch_optional(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "get document version"))?
    .ok_or_else(|| ServiceError::not_found("document version not found"))?;
    version_from_row(&row)
}

fn version_from_row(row: &sqlx::postgres::PgRow) -> Result<DocumentVersion> {
    let id: Uuid = row
        .try_get("id")
        .map_err(|error| storage_error(error, "decode version id"))?;
    let document_id: Uuid = row
        .try_get("document_id")
        .map_err(|error| storage_error(error, "decode version document id"))?;
    let kind: String = row
        .try_get("kind")
        .map_err(|error| storage_error(error, "decode version kind"))?;
    document_version_from_row(row, id, document_id, &kind)
}

fn document_version_from_row(
    row: &sqlx::postgres::PgRow,
    id: Uuid,
    document_id: Uuid,
    kind: &str,
) -> Result<DocumentVersion> {
    Ok(DocumentVersion {
        id: VersionId::parse(&id.to_string())?,
        document_id: DocumentId::parse(&document_id.to_string())?,
        sequence: row
            .try_get("sequence")
            .map_err(|error| storage_error(error, "decode version sequence"))?,
        kind: VersionKind::from_str(kind)?,
        label: row
            .try_get("label")
            .map_err(|error| storage_error(error, "decode version label"))?,
        state: row
            .try_get("state")
            .map_err(|error| storage_error(error, "decode version state"))?,
        created_by: PublicUser {
            id: row
                .try_get("created_by_id")
                .map_err(|error| storage_error(error, "decode version actor id"))?,
            username: row
                .try_get("created_by_username")
                .map_err(|error| storage_error(error, "decode version actor username"))?,
            avatar: row
                .try_get("created_by_avatar")
                .map_err(|error| storage_error(error, "decode version actor avatar"))?,
        },
        created_at: row
            .try_get("created_at")
            .map_err(|error| storage_error(error, "decode version creation time"))?,
    })
}

async fn idempotent_version(
    connection: &mut PgConnection,
    document_id: DocumentId,
    actor_id: i64,
    operation: &str,
    key: Option<&str>,
    request_hash: &str,
) -> Result<Option<DocumentVersion>> {
    let Some(key) = key else {
        return Ok(None);
    };
    sqlx::query(
        "DELETE FROM collaboration.idempotency_keys
         WHERE actor_id = $1 AND operation = $2 AND key = $3 AND expires_at <= now()",
    )
    .bind(actor_id)
    .bind(operation)
    .bind(key)
    .execute(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "replace expired idempotency key"))?;
    let row = sqlx::query(
        "SELECT request_hash, resource_id FROM collaboration.idempotency_keys
         WHERE actor_id = $1 AND operation = $2 AND key = $3",
    )
    .bind(actor_id)
    .bind(operation)
    .bind(key)
    .fetch_optional(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "load idempotency key"))?;
    let Some(row) = row else {
        return Ok(None);
    };
    let stored_hash: String = row
        .try_get("request_hash")
        .map_err(|error| storage_error(error, "decode idempotency request hash"))?;
    if stored_hash.trim_end() != request_hash {
        return Err(ServiceError::conflict(
            "idempotency key was already used for another request",
        ));
    }
    let resource_id: Uuid = row
        .try_get("resource_id")
        .map_err(|error| storage_error(error, "decode idempotency resource id"))?;
    get_version(
        connection,
        document_id,
        VersionId::parse(&resource_id.to_string())?,
    )
    .await
    .map(Some)
}

async fn save_idempotency(
    connection: &mut PgConnection,
    actor_id: i64,
    operation: &str,
    key: Option<&str>,
    request_hash: &str,
    version_id: VersionId,
) -> Result<()> {
    let Some(key) = key else {
        return Ok(());
    };
    let expires_at = OffsetDateTime::now_utc()
        + time::Duration::try_from(IDEMPOTENCY_TTL).map_err(|error| {
            ServiceError::internal(anyhow!(error).context("convert idempotency TTL"))
        })?;
    let response = json!({ "version_id": version_id });
    sqlx::query(
        "INSERT INTO collaboration.idempotency_keys(
           actor_id, operation, key, request_hash, resource_id, response, expires_at
         ) VALUES ($1, $2, $3, $4, $5, $6, $7)",
    )
    .bind(actor_id)
    .bind(operation)
    .bind(key)
    .bind(request_hash)
    .bind(version_id.as_uuid())
    .bind(response)
    .bind(expires_at)
    .execute(&mut *connection)
    .await
    .map_err(|error| {
        if has_database_code(&error, "23505") {
            ServiceError::conflict("idempotency key was concurrently consumed")
        } else {
            storage_error(error, "save idempotency key")
        }
    })?;
    Ok(())
}

fn request_hash(value: &serde_json::Value) -> Result<String> {
    let bytes = serde_json::to_vec(value)
        .map_err(|error| ServiceError::internal(anyhow!(error).context("encode request hash")))?;
    Ok(sha256_hex(&bytes))
}

fn validate_label(value: Option<&str>) -> Result<Option<String>> {
    value
        .map(|value| {
            let value = value.trim();
            if value.is_empty() || value.chars().count() > 200 {
                return Err(ServiceError::invalid_input(
                    "version label must contain between 1 and 200 characters",
                ));
            }
            Ok(value.to_owned())
        })
        .transpose()
}

fn validate_idempotency_key(value: Option<&str>) -> Result<()> {
    if value.is_some_and(|value| {
        value.is_empty()
            || value.len() > 128
            || value.trim() != value
            || !value.bytes().all(|byte| (b'!'..=b'~').contains(&byte))
    }) {
        return Err(ServiceError::invalid_input("idempotency key is invalid"));
    }
    Ok(())
}

fn has_database_code(error: &sqlx::Error, expected: &str) -> bool {
    error
        .as_database_error()
        .and_then(sqlx::error::DatabaseError::code)
        .is_some_and(|code| code == expected)
}

async fn ensure_document(connection: &mut PgConnection, document_id: DocumentId) -> Result<()> {
    let now = OffsetDateTime::now_utc();
    let result = sqlx::query(
        "INSERT INTO collaboration.documents(document_id, created_at, updated_at)
         VALUES ($1, $2, $2) ON CONFLICT (document_id) DO NOTHING",
    )
    .bind(document_id.as_uuid())
    .bind(now)
    .execute(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "initialize document head"))?;
    if result.rows_affected() == 1 {
        let state = richtext::initial_state();
        sqlx::query(
            "INSERT INTO collaboration.snapshots(
               document_id, generation, sequence, state, created_at
             ) VALUES ($1, 1, 0, $2, $3)",
        )
        .bind(document_id.as_uuid())
        .bind(state)
        .bind(now)
        .execute(&mut *connection)
        .await
        .map_err(|error| storage_error(error, "initialize document snapshot"))?;
    }
    Ok(())
}

async fn load_head(
    connection: &mut PgConnection,
    document_id: DocumentId,
    lock: bool,
) -> Result<Head> {
    let query = if lock {
        "SELECT generation, current_sequence, last_snapshot_sequence, last_version_sequence,
                last_automatic_version_at, last_actor_id, last_actor_username, last_actor_avatar
         FROM collaboration.documents WHERE document_id = $1 FOR UPDATE"
    } else {
        "SELECT generation, current_sequence, last_snapshot_sequence, last_version_sequence,
                last_automatic_version_at, last_actor_id, last_actor_username, last_actor_avatar
         FROM collaboration.documents WHERE document_id = $1"
    };
    let row = sqlx::query(query)
        .bind(document_id.as_uuid())
        .fetch_optional(&mut *connection)
        .await
        .map_err(|error| storage_error(error, "load document head"))?
        .ok_or_else(|| ServiceError::not_found("collaborative document not found"))?;
    Ok(Head {
        generation: row
            .try_get("generation")
            .map_err(|error| storage_error(error, "decode document generation"))?,
        current_sequence: row
            .try_get("current_sequence")
            .map_err(|error| storage_error(error, "decode current sequence"))?,
        last_snapshot_sequence: row
            .try_get("last_snapshot_sequence")
            .map_err(|error| storage_error(error, "decode snapshot sequence"))?,
        last_version_sequence: row
            .try_get("last_version_sequence")
            .map_err(|error| storage_error(error, "decode version sequence"))?,
        last_automatic_version_at: row
            .try_get("last_automatic_version_at")
            .map_err(|error| storage_error(error, "decode automatic version time"))?,
        last_actor_id: row
            .try_get("last_actor_id")
            .map_err(|error| storage_error(error, "decode actor id"))?,
        last_actor_username: row
            .try_get("last_actor_username")
            .map_err(|error| storage_error(error, "decode actor username"))?,
        last_actor_avatar: row
            .try_get("last_actor_avatar")
            .map_err(|error| storage_error(error, "decode actor avatar"))?,
    })
}

async fn load_state(
    connection: &mut PgConnection,
    document_id: DocumentId,
    generation: i64,
    sequence: i64,
) -> Result<Vec<u8>> {
    let snapshot = sqlx::query(
        "SELECT sequence, state FROM collaboration.snapshots
         WHERE document_id = $1 AND generation = $2 AND sequence <= $3
         ORDER BY sequence DESC LIMIT 1",
    )
    .bind(document_id.as_uuid())
    .bind(generation)
    .bind(sequence)
    .fetch_optional(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "load document snapshot"))?
    .ok_or_else(|| ServiceError::internal(anyhow!("document snapshot is missing")))?;
    let snapshot_sequence: i64 = snapshot
        .try_get("sequence")
        .map_err(|error| storage_error(error, "decode document snapshot sequence"))?;
    let state: Vec<u8> = snapshot
        .try_get("state")
        .map_err(|error| storage_error(error, "decode document snapshot state"))?;
    let rows = sqlx::query(
        "SELECT update FROM collaboration.updates
         WHERE document_id = $1 AND generation = $2 AND sequence > $3 AND sequence <= $4
         ORDER BY sequence ASC",
    )
    .bind(document_id.as_uuid())
    .bind(generation)
    .bind(snapshot_sequence)
    .bind(sequence)
    .fetch_all(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "load document update range"))?;
    let updates = rows
        .into_iter()
        .map(|row| {
            row.try_get("update")
                .map_err(|error| storage_error(error, "decode document update"))
        })
        .collect::<Result<Vec<Vec<u8>>>>()?;
    richtext::merge_updates(&state, &updates)
}

async fn enqueue_projection(
    connection: &mut PgConnection,
    document_id: DocumentId,
    generation: i64,
    sequence: i64,
    now: OffsetDateTime,
) -> Result<()> {
    sqlx::query(
        "INSERT INTO collaboration.projection_jobs(
           document_id, target_generation, target_sequence, attempts, next_attempt_at,
           lease_until, last_error_key, created_at, updated_at
         ) VALUES ($1, $2, $3, 0, $4, NULL, '', $4, $4)
         ON CONFLICT (document_id) DO UPDATE SET
           target_generation = EXCLUDED.target_generation,
           target_sequence = EXCLUDED.target_sequence,
           attempts = 0, next_attempt_at = EXCLUDED.next_attempt_at,
           lease_until = NULL, last_error_key = '', updated_at = EXCLUDED.updated_at",
    )
    .bind(document_id.as_uuid())
    .bind(generation)
    .bind(sequence)
    .bind(now)
    .execute(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "enqueue projection"))?;
    Ok(())
}

async fn insert_event(
    connection: &mut PgConnection,
    subject: &str,
    kind: &str,
    event: DocumentEvent,
    now: OffsetDateTime,
) -> Result<()> {
    let id = Uuid::now_v7();
    let event_key = format!(
        "document:{}:{kind}:{}:{}",
        event.document_id, event.generation, event.sequence
    );
    let document_id = event.document_id;
    let generation = event.generation;
    let sequence = event.sequence;
    let payload = serde_json::to_value(event)
        .map_err(|error| ServiceError::internal(anyhow!(error).context("encode outbox event")))?;
    sqlx::query(
        "INSERT INTO collaboration.outbox(
           id, event_key, subject, document_id, generation, sequence, payload,
           attempts, next_attempt_at, created_at
         ) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, $8)
         ON CONFLICT (event_key) DO NOTHING",
    )
    .bind(id)
    .bind(event_key)
    .bind(subject)
    .bind(document_id.as_uuid())
    .bind(generation)
    .bind(sequence)
    .bind(payload)
    .bind(now)
    .execute(&mut *connection)
    .await
    .map_err(|error| storage_error(error, "insert outbox event"))?;
    Ok(())
}

fn migration_checksum(sql: &str) -> String {
    sha256_hex(sql.as_bytes())
}

fn sha256_hex(value: &[u8]) -> String {
    let mut output = String::with_capacity(64);
    for byte in Sha256::digest(value) {
        write!(&mut output, "{byte:02x}").expect("writing to a String cannot fail");
    }
    output
}

fn operation_budget(
    context: &RequestContext,
    operation_timeout: Duration,
    operation: &'static str,
) -> Result<OperationBudget> {
    let Some(deadline) = context.deadline else {
        return Ok(OperationBudget {
            maximum_wait: operation_timeout,
            request_deadline_limited: false,
        });
    };
    let remaining = deadline
        .checked_duration_since(Instant::now())
        .filter(|remaining| !remaining.is_zero())
        .ok_or_else(|| {
            request_deadline_error(
                operation,
                anyhow!("request deadline elapsed before PostgreSQL operation"),
            )
        })?;
    Ok(OperationBudget {
        maximum_wait: remaining.min(operation_timeout),
        request_deadline_limited: remaining <= operation_timeout,
    })
}

fn dependency_timeout(operation: &'static str) -> ServiceError {
    ServiceError::unavailable(anyhow!("{operation} timed out"))
}

fn dependency_timeout_with_source(operation: &'static str, source: anyhow::Error) -> ServiceError {
    ServiceError::unavailable(source.context(operation))
}

fn request_deadline_error(operation: &'static str, source: anyhow::Error) -> ServiceError {
    ServiceError::new(
        ErrorCode::Unavailable,
        "collaboration.deadline_exceeded",
        "request deadline exceeded",
    )
    .with_source(source.context(operation))
}

fn storage_error(error: sqlx::Error, operation: &'static str) -> ServiceError {
    match error {
        sqlx::Error::Io(_)
        | sqlx::Error::Tls(_)
        | sqlx::Error::PoolTimedOut
        | sqlx::Error::PoolClosed
        | sqlx::Error::WorkerCrashed => {
            ServiceError::unavailable(anyhow!(error).context(operation))
        }
        _ => ServiceError::internal(anyhow!(error).context(operation)),
    }
}

#[cfg(test)]
mod tests {
    use std::{error::Error as _, time::Duration};

    use super::{MIGRATIONS, migration_checksum, operation_budget, request_deadline_error};
    use crate::{
        domain::RequestContext,
        error::{ErrorCode, ServiceError},
    };

    #[test]
    fn migrations_are_strictly_ordered_and_nonempty() {
        assert!(
            MIGRATIONS
                .windows(2)
                .all(|pair| pair[0].version < pair[1].version)
        );
        assert!(MIGRATIONS.iter().all(|migration| {
            !migration.name.is_empty()
                && !migration.sql.trim().is_empty()
                && migration_checksum(migration.sql).len() == 64
        }));
    }

    #[test]
    fn request_budget_rejects_an_expired_deadline_with_a_stable_cause() {
        let mut context = RequestContext::new("expired-request");
        context.deadline = Some(std::time::Instant::now());

        let error = operation_budget(&context, Duration::from_secs(5), "load document")
            .expect_err("an expired request must not reach PostgreSQL");
        assert_eq!(error.code(), ErrorCode::Unavailable);
        assert_eq!(error.key(), "collaboration.deadline_exceeded");
        assert_eq!(error.detail(), "request deadline exceeded");
        assert!(error.source().is_some());
    }

    #[test]
    fn request_budget_uses_the_shorter_request_deadline() {
        let mut context = RequestContext::new("short-request");
        context.deadline = std::time::Instant::now().checked_add(Duration::from_millis(50));

        let budget = operation_budget(&context, Duration::from_secs(5), "load document")
            .expect("request budget");
        assert!(budget.request_deadline_limited);
        assert!(!budget.maximum_wait.is_zero());
        assert!(budget.maximum_wait <= Duration::from_millis(50));
    }

    #[test]
    fn request_budget_uses_the_shorter_operation_timeout() {
        let mut context = RequestContext::new("long-request");
        context.deadline = std::time::Instant::now().checked_add(Duration::from_secs(5));

        let budget = operation_budget(&context, Duration::from_millis(25), "load document")
            .expect("request budget");
        assert!(!budget.request_deadline_limited);
        assert_eq!(budget.maximum_wait, Duration::from_millis(25));
    }

    #[test]
    fn deadline_mapping_preserves_the_source_chain() {
        let error = request_deadline_error("load document", anyhow::anyhow!("timer elapsed"));
        assert_deadline_error(&error);
        let source = error.source().expect("deadline source");
        assert_eq!(source.to_string(), "load document");
        assert!(source.source().is_some());
    }

    fn assert_deadline_error(error: &ServiceError) {
        assert_eq!(error.code(), ErrorCode::Unavailable);
        assert_eq!(error.key(), "collaboration.deadline_exceeded");
        assert_eq!(error.detail(), "request deadline exceeded");
    }
}
