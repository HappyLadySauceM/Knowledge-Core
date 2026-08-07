#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${RELEASE_SHA_FILE:?RELEASE_SHA_FILE is required}"

release_sha="$(<"$RELEASE_SHA_FILE")"
if [[ ! "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ || ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
    printf 'source and release commits must be lowercase 40-character SHA-1 values\n' >&2
    exit 2
fi

api=https://api.github.com
headers=(
    --header 'Accept: application/vnd.github+json'
    --header "Authorization: Bearer ${GITHUB_TOKEN}"
    --header 'X-GitHub-Api-Version: 2022-11-28'
)
github() {
    curl --fail --silent --show-error "${headers[@]}" "$@"
}
ref_sha() {
    github "${api}/repos/${GITHUB_REPOSITORY}/git/ref/heads/$1" | jq -er '.object.sha'
}

if [[ "$(ref_sha main)" != "$release_sha" ]]; then
    printf 'main moved before dev synchronization; expected %s\n' "$release_sha" >&2
    exit 1
fi
dev_sha="$(ref_sha dev)"
if [[ "$dev_sha" == "$GITHUB_SHA" ]]; then
    github \
        --request PATCH \
        --header 'Content-Type: application/json' \
        --data "$(jq -cn --arg sha "$release_sha" '{sha:$sha,force:false}')" \
        "${api}/repos/${GITHUB_REPOSITORY}/git/refs/heads/dev" >/dev/null
elif [[ "$dev_sha" != "$release_sha" ]]; then
    printf 'dev moved before synchronization; found %s, expected %s or %s\n' \
        "$dev_sha" "$GITHUB_SHA" "$release_sha" >&2
    exit 1
fi
if [[ "$(ref_sha dev)" != "$release_sha" ]]; then
    printf 'dev did not fast-forward to merge commit %s\n' "$release_sha" >&2
    exit 1
fi
printf 'Fast-forwarded dev to release merge commit %s\n' "$release_sha"
