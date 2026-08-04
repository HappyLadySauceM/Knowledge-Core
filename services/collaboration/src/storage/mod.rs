mod postgres;

use std::time::Duration;

use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use uuid::Uuid;

use crate::{
    domain::{DocumentId, DocumentVersion, Projection, PublicUser, RequestContext, VersionId},
    error::Result,
};

pub use postgres::PostgresStore;

#[derive(Clone, Debug)]
pub struct EventSubjects {
    pub updated: String,
    pub invalidated: String,
}

impl EventSubjects {
    pub fn new(updated: impl Into<String>, invalidated: impl Into<String>) -> Self {
        Self {
            updated: updated.into(),
            invalidated: invalidated.into(),
        }
    }
}

#[derive(Clone, Debug)]
pub struct LoadedDocument {
    pub generation: i64,
    pub sequence: i64,
    pub state: Vec<u8>,
}

#[derive(Clone, Copy, Debug)]
pub struct UpdateLimits {
    pub maximum_update_bytes: usize,
    pub maximum_document_bytes: usize,
}

#[derive(Clone, Debug)]
pub struct CommittedUpdate {
    pub generation: i64,
    pub sequence: i64,
    pub state: Vec<u8>,
    pub projection: Projection,
    pub update: Option<Vec<u8>>,
}

#[derive(Clone, Debug)]
pub struct StoredUpdate {
    pub generation: i64,
    pub sequence: i64,
    pub update: Vec<u8>,
}

#[derive(Clone, Debug)]
pub struct VersionCursor {
    pub created_at: OffsetDateTime,
    pub id: VersionId,
}

#[derive(Clone, Debug)]
pub struct VersionPage {
    pub items: Vec<DocumentVersion>,
    pub has_more: bool,
}

#[derive(Clone, Debug)]
pub struct RestoreVersion {
    pub version: DocumentVersion,
    pub committed: Option<CommittedUpdate>,
}

#[derive(Clone, Copy, Debug)]
pub struct RestorationCandidate<'a> {
    pub target: &'a DocumentVersion,
    pub baseline_generation: i64,
    pub baseline_sequence: i64,
    pub expected_sequence: i64,
    pub update: &'a [u8],
    pub actor: &'a PublicUser,
    pub idempotency_key: Option<&'a str>,
    pub limits: UpdateLimits,
}

#[derive(Clone, Debug)]
pub struct ProjectionJob {
    pub document_id: DocumentId,
    pub generation: i64,
    pub sequence: i64,
    pub state: Vec<u8>,
    pub attempts: i32,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct DocumentEvent {
    pub document_id: DocumentId,
    pub generation: i64,
    pub sequence: i64,
    pub actor_id: Option<i64>,
}

#[derive(Clone, Debug)]
pub struct OutboxEvent {
    pub id: Uuid,
    pub event_key: String,
    pub subject: String,
    pub payload: serde_json::Value,
    pub attempts: i32,
}

#[async_trait]
pub trait DocumentStore: Send + Sync {
    async fn initialize_document(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<()>;

    async fn load_document(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<LoadedDocument>;

    async fn append_update(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        update: &[u8],
        actor: &PublicUser,
        limits: UpdateLimits,
    ) -> Result<CommittedUpdate>;

    async fn commit_restoration(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        candidate: RestorationCandidate<'_>,
    ) -> Result<RestoreVersion>;

    async fn updates_after(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        sequence: i64,
        limit: i64,
    ) -> Result<Vec<StoredUpdate>>;

    async fn current_sequence(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<i64>;
}

#[async_trait]
pub trait VersionStore: Send + Sync {
    async fn create_manual_version(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        actor: &PublicUser,
        label: Option<&str>,
        idempotency_key: Option<&str>,
    ) -> Result<DocumentVersion>;

    async fn list_versions(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        cursor: Option<&VersionCursor>,
        limit: i64,
    ) -> Result<VersionPage>;

    async fn get_version(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        version_id: VersionId,
    ) -> Result<DocumentVersion>;

    async fn purge_document(&self, context: &RequestContext, document_id: DocumentId)
    -> Result<()>;
}

#[async_trait]
pub trait WorkerStore: Send + Sync {
    async fn claim_projection_job(
        &self,
        context: &RequestContext,
        lease: Duration,
    ) -> Result<Option<ProjectionJob>>;

    async fn complete_projection(
        &self,
        context: &RequestContext,
        job: &ProjectionJob,
    ) -> Result<()>;

    async fn retry_projection(
        &self,
        context: &RequestContext,
        job: &ProjectionJob,
        error_key: &str,
    ) -> Result<()>;

    async fn compact_next(
        &self,
        context: &RequestContext,
        update_threshold: i64,
        byte_threshold: i64,
    ) -> Result<bool>;

    async fn create_automatic_version(
        &self,
        context: &RequestContext,
        interval: Duration,
    ) -> Result<bool>;

    async fn claim_outbox(
        &self,
        context: &RequestContext,
        batch_size: i64,
        lease: Duration,
    ) -> Result<Vec<OutboxEvent>>;

    async fn complete_outbox(&self, context: &RequestContext, id: Uuid) -> Result<()>;

    async fn retry_outbox(
        &self,
        context: &RequestContext,
        event: &OutboxEvent,
        error_key: &str,
    ) -> Result<()>;

    async fn cleanup(&self, context: &RequestContext) -> Result<()>;
}
