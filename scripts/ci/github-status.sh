#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"

state="${1:?status state is required}"
description="${2:-Knowledge Core CI ${state}}"
context="${3:-knowledge-core/ci}"
case "$state" in
    error | failure | pending | success) ;;
    *)
        printf 'unsupported GitHub commit status: %s\n' "$state" >&2
        exit 2
        ;;
esac
case "$context" in
    knowledge-core/ci | knowledge-core/smoke) ;;
    *)
        printf 'unsupported GitHub commit status context: %s\n' "$context" >&2
        exit 2
        ;;
esac

payload="$(
    jq -cn \
        --arg state "$state" \
        --arg context "$context" \
        --arg description "$description" \
        --arg target_url "${GITHUB_TARGET_URL:-}" \
        '{state:$state,context:$context,description:$description}
         + if $target_url == "" then {} else {target_url:$target_url} end'
)"

curl --fail --silent --show-error \
    --request POST \
    --header 'Accept: application/vnd.github+json' \
    --header "Authorization: Bearer ${GITHUB_TOKEN}" \
    --header 'Content-Type: application/json' \
    --header 'X-GitHub-Api-Version: 2022-11-28' \
    --data "$payload" \
    "https://api.github.com/repos/${GITHUB_REPOSITORY}/statuses/${GITHUB_SHA}" \
    >/dev/null
