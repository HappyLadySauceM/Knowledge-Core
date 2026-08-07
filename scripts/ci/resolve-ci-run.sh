#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_API_TOKEN:?GITHUB_API_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${OUTPUT_ROOT:?OUTPUT_ROOT is required}"

case "${GITHUB_EVENT_NAME:-}" in
    workflow_run)
        : "${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH is required}"
        run_id="$(jq -er '.workflow_run.id' "$GITHUB_EVENT_PATH")"
        ;;
    workflow_dispatch)
        run_id="${CI_RUN_ID:?CI_RUN_ID is required for manual recovery}"
        ;;
    *)
        printf 'unsupported promotion event: %s\n' "${GITHUB_EVENT_NAME:-}" >&2
        exit 2
        ;;
esac
if [[ ! "$run_id" =~ ^[1-9][0-9]*$ ]]; then
    printf 'CI run ID must be a positive integer\n' >&2
    exit 2
fi

api=https://api.github.com
headers=(
    --header 'Accept: application/vnd.github+json'
    --header "Authorization: Bearer ${GITHUB_API_TOKEN}"
    --header 'X-GitHub-Api-Version: 2022-11-28'
)
github() {
    curl --fail --silent --show-error "${headers[@]}" "$@"
}

run="$(github "${api}/repos/${GITHUB_REPOSITORY}/actions/runs/${run_id}")"
if ! jq -e --arg repository "$GITHUB_REPOSITORY" \
    '.name == "Dev CI"
     and .path == ".github/workflows/dev-ci.yml"
     and .event == "push"
     and .head_branch == "dev"
     and .head_repository.full_name == $repository
     and .conclusion == "success"' <<<"$run" >/dev/null; then
    printf 'workflow run %s is not a successful trusted Dev CI push\n' "$run_id" >&2
    exit 1
fi
candidate="$(jq -er '.head_sha' <<<"$run")"
if [[ ! "$candidate" =~ ^[0-9a-f]{40}$ ]]; then
    printf 'workflow run returned an invalid head SHA\n' >&2
    exit 1
fi

dev="$(github "${api}/repos/${GITHUB_REPOSITORY}/git/ref/heads/dev" | jq -er '.object.sha')"
main="$(github "${api}/repos/${GITHUB_REPOSITORY}/git/ref/heads/main" | jq -er '.object.sha')"
if [[ "$dev" != "$candidate" ]]; then
    if [[ "$main" != "$candidate" ]]; then
        printf 'Dev CI run %s was superseded by dev %s\n' "$candidate" "$dev" >&2
        exit 75
    fi
    status="$(github "${api}/repos/${GITHUB_REPOSITORY}/compare/${candidate}...${dev}" | jq -er '.status')"
    if [[ "$status" != ahead && "$status" != identical ]]; then
        printf 'dev %s is not a descendant of released candidate %s\n' "$dev" "$candidate" >&2
        exit 1
    fi
fi

mkdir -p "$OUTPUT_ROOT"
printf '%s\n' "$candidate" >"${OUTPUT_ROOT}/candidate-sha"
printf '%s\n' "$run_id" >"${OUTPUT_ROOT}/ci-run-id"
