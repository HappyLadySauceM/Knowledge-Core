#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${GITHUB_REF:?GITHUB_REF is required}"

case "$GITHUB_REF" in
    refs/heads/dev) reference=heads/dev ;;
    *)
        printf 'unsupported GitHub ref: %s\n' "$GITHUB_REF" >&2
        exit 2
        ;;
esac

current="$(
    curl --fail --silent --show-error \
        --header 'Accept: application/vnd.github+json' \
        --header "Authorization: Bearer ${GITHUB_TOKEN}" \
        --header 'X-GitHub-Api-Version: 2022-11-28' \
        "https://api.github.com/repos/${GITHUB_REPOSITORY}/git/ref/${reference}" \
        | jq -er '.object.sha'
)"

if [[ "$current" != "$GITHUB_SHA" ]]; then
    printf 'source ref is superseded: expected %s, found %s\n' "$GITHUB_SHA" "$current" >&2
    exit 75
fi
