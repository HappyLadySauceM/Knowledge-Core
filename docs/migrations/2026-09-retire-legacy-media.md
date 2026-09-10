# Retire the legacy Knowledge media subsystem

This rollout is intentionally destructive. The document-scoped HTTP/RPC attachment API, the `knowledge.attachments` and `knowledge.attachment_scan_jobs` records, and every object in the legacy `knowledge-core` bucket are not migrated to the generic Attachment service.

## Required rollout order

1. Stop Gateway and Knowledge writers from the old release.
2. Permanently remove the legacy bucket. Verify the alias and bucket name before running these commands:

   ```sh
   mc ls legacy/knowledge-core
   mc rm --recursive --force legacy/knowledge-core
   mc rb legacy/knowledge-core
   ```

   `legacy` must be an `mc` alias for the deployment's old object-storage endpoint. These commands are irreversible. Do not target `knowledge-core-attachments`, which is owned by the new Attachment service.

3. Deploy Attachment first and apply `services/attachment/internal/migration/migrations/003_publication_references.sql`.
4. Deploy Knowledge. Migration `005_media_publication_saga.sql` permanently drops the legacy attachment tables and installs the durable publication-reference jobs.
5. Deploy Gateway and Web together. The four old `/api/v1/studio/documents/{document_id}/attachments...` operations are removed; `/api/v1/attachments...` is the only upload API.

Attachment publication-reference RPCs use the service-mesh transport boundary and do not require a second application service token.

If the cluster still has pre-migration images, roll out the new Attachment, Knowledge, Platform, and Identity images first and wait for them to become ready before syncing the SOPS changes that remove the obsolete token keys. This prevents an old image from restarting without the configuration it still expects.

## Publication recovery

Publication returns `202` and normally converges within 30 seconds. A failed job is retried with bounded backoff and parked after eight attempts. The document exposes `publish_failed` or `unpublish_failed` and a stable error key. Publishing or unpublishing the document again creates a higher generation and is the supported operator redrive; Attachment ignores stale generations and treats identical commands idempotently.

For reconciliation, compare `knowledge.documents.publication_generation` with Attachment's internal `GetPublicationReferenceState` RPC. Never edit reference rows by hand. Issue the document publication command again so Knowledge creates a new durable intent.

## Compatibility result

The Thrift compatibility guard is expected to fail for this release: the Knowledge attachment structs/RPCs and the Gateway document-scoped routes are deliberately removed, while document publication status and Attachment publication-reference RPCs are added. Old clients must be upgraded in the same rollout window.
