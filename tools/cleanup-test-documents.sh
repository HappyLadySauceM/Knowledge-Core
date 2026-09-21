#!/usr/bin/env bash
set -euo pipefail

# The owner list is intentionally fixed.  Do not broaden this list to a title
# pattern: this utility is for the known integration-test accounts only.
readonly TEST_OWNER_IDS="8,9,10,11,17"

: "${DATABASE_URL:?Set DATABASE_URL to the Knowledge PostgreSQL database}"
DRY_RUN="${DRY_RUN:-1}"

if [[ "$DRY_RUN" != "0" && "$DRY_RUN" != "1" ]]; then
  echo "DRY_RUN must be 1 (default) or 0" >&2
  exit 2
fi

echo "Test-document cleanup owners: ${TEST_OWNER_IDS}"
echo "Mode: $([[ "$DRY_RUN" == "1" ]] && echo dry-run || echo execute)"

list_documents() {
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -P pager=off <<SQL
SELECT d.id,
       d.owner_id,
       d.metadata_revision,
       d.deleted_at,
       d.purge_after,
       d.publication_status,
       (pub.document_id IS NOT NULL) AS has_public_snapshot,
       d.title
FROM knowledge.documents AS d
LEFT JOIN knowledge.document_publications AS pub ON pub.document_id = d.id
WHERE d.owner_id IN (${TEST_OWNER_IDS})
ORDER BY d.owner_id, d.created_at, d.id;
SQL
}

list_documents

if [[ "$DRY_RUN" == "1" ]]; then
  echo "Dry-run only; no document was changed. Set DRY_RUN=0 to invoke the permanent-delete workflow."
  exit 0
fi

: "${DELETE_DOCUMENT_COMMAND:?Set DELETE_DOCUMENT_COMMAND to an authenticated soft-delete workflow; it receives DOCUMENT_ID and METADATA_REVISION}"
: "${PURGE_DOCUMENT_COMMAND:?Set PURGE_DOCUMENT_COMMAND to an authenticated permanent-delete workflow; it receives DOCUMENT_ID and METADATA_REVISION}"
: "${PUBLIC_LIST_VERIFY_COMMAND:?Set PUBLIC_LIST_VERIFY_COMMAND to a command that exits 0 only when the public list is empty for owners 8,9,10,11,17}"

mapfile -t document_rows < <(
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -At -F $'\t' <<SQL
SELECT id,
       metadata_revision,
       (deleted_at IS NOT NULL),
       (purge_after IS NOT NULL AND purge_after <= CURRENT_TIMESTAMP)
FROM knowledge.documents
WHERE owner_id IN (${TEST_OWNER_IDS})
ORDER BY owner_id, created_at, id;
SQL
)

for row in "${document_rows[@]}"; do
  IFS=$'\t' read -r DOCUMENT_ID METADATA_REVISION IS_DELETED IS_DUE_FOR_PURGE <<<"$row"
  [[ -n "$DOCUMENT_ID" ]] || continue

  if [[ "$IS_DELETED" != "t" ]]; then
    echo "Moving active document ${DOCUMENT_ID} to trash (If-Match revision ${METADATA_REVISION})"
    DOCUMENT_ID="$DOCUMENT_ID" METADATA_REVISION="$METADATA_REVISION" bash -c "$DELETE_DOCUMENT_COMMAND"
    read -r METADATA_REVISION < <(
      psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc "
        SELECT metadata_revision
        FROM knowledge.documents
        WHERE id = '$DOCUMENT_ID' AND owner_id IN (${TEST_OWNER_IDS}) AND deleted_at IS NOT NULL;
      "
    )
    if [[ -z "$METADATA_REVISION" ]]; then
      echo "Soft-delete did not move ${DOCUMENT_ID} to trash" >&2
      exit 1
    fi
  fi

  if [[ "$IS_DUE_FOR_PURGE" == "t" ]]; then
    echo "Document ${DOCUMENT_ID} is already due for worker purge; skipping a duplicate request"
    continue
  fi

  echo "Requesting permanent deletion for ${DOCUMENT_ID} (If-Match revision ${METADATA_REVISION})"
  DOCUMENT_ID="$DOCUMENT_ID" METADATA_REVISION="$METADATA_REVISION" bash -c "$PURGE_DOCUMENT_COMMAND"
done

echo "Waiting for the asynchronous purge worker to remove the candidates..."
bash -c "$PUBLIC_LIST_VERIFY_COMMAND"

remaining_public_snapshots="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc "
  SELECT count(*)
  FROM knowledge.document_publications
  WHERE owner_id IN (${TEST_OWNER_IDS});
")"
if [[ "$remaining_public_snapshots" != "0" ]]; then
  echo "Public snapshot cleanup is incomplete: ${remaining_public_snapshots} snapshot(s) remain" >&2
  exit 1
fi

remaining_documents="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc "
  SELECT count(*)
  FROM knowledge.documents
  WHERE owner_id IN (${TEST_OWNER_IDS});
")"
if [[ "$remaining_documents" != "0" ]]; then
  echo "Physical document cleanup is incomplete: ${remaining_documents} document(s) remain" >&2
  exit 1
fi

echo "Test-document cleanup completed and the public-list verification passed. Identity accounts were not modified."
