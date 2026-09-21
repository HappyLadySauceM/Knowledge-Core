# Operational tools

## `cleanup-test-documents.sh`

This utility targets only documents owned by the fixed integration-test owner
IDs `8, 9, 10, 11, 17`. It never deletes Identity accounts and it never uses a
title pattern. The default is a read-only dry run:

```sh
DATABASE_URL=postgres://... ./tools/cleanup-test-documents.sh
```

To execute the cleanup, set `DRY_RUN=0` and provide two authenticated operator
commands:

```sh
DRY_RUN=0 \
  DELETE_DOCUMENT_COMMAND='curl --fail --request DELETE \
    --header "Authorization: Bearer $OPERATOR_TOKEN" \
    --header "If-Match: $METADATA_REVISION" \
    "$GATEWAY_URL/api/v1/studio/documents/$DOCUMENT_ID"' \
  PURGE_DOCUMENT_COMMAND='curl --fail --request DELETE \
    --header "Authorization: Bearer $OPERATOR_TOKEN" \
    --header "If-Match: $METADATA_REVISION" \
    --header "Idempotency-Key: test-cleanup-$DOCUMENT_ID" \
  --header "X-Confirm-Permanent-Delete: true" \
    "$GATEWAY_URL/api/v1/studio/trash/$DOCUMENT_ID"' \
  PUBLIC_LIST_VERIFY_COMMAND='curl --fail --silent "$PUBLIC_URL/api/v1/documents?limit=100" | jq -e '\''[.items[] | select(.owner.id == "8" or .owner.id == "9" or .owner.id == "10" or .owner.id == "11" or .owner.id == "17")] | length == 0'\''' \
  DATABASE_URL=postgres://... \
  ./tools/cleanup-test-documents.sh
```

`PURGE_DOCUMENT_COMMAND` receives `DOCUMENT_ID` and `METADATA_REVISION` in its
environment and must call the permanent-delete endpoint, rather than deleting
database rows directly. Active documents are first passed to
`DELETE_DOCUMENT_COMMAND`, then the script reads the new revision before
requesting permanent deletion. Entries whose purge deadline has already elapsed
are left for the normal worker retry path. `PUBLIC_LIST_VERIFY_COMMAND` must
exit successfully only after the public list is empty. The script checks that
publication snapshots and Knowledge documents are eventually gone after the
asynchronous cleanup worker finishes.
