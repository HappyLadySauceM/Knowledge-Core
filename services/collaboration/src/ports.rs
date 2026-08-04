use async_trait::async_trait;

use crate::{
    domain::{Authorization, DocumentId, Projection, RequestContext},
    error::Result,
};

#[async_trait]
pub trait KnowledgePort: Send + Sync {
    async fn authorize(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
    ) -> Result<Authorization>;

    async fn project(
        &self,
        context: &RequestContext,
        document_id: DocumentId,
        sequence: i64,
        projection: &Projection,
    ) -> Result<()>;

    async fn ping(&self, context: &RequestContext) -> Result<()>;
}
