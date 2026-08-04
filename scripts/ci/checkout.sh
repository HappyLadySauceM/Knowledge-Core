#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${WORKSPACE:?WORKSPACE is required}"

case "$WORKSPACE" in
    /workspace/source) ;;
    *)
        printf 'unsafe checkout workspace: %s\n' "$WORKSPACE" >&2
        exit 2
        ;;
esac

if [[ -e "$WORKSPACE" ]]; then
    printf 'checkout workspace already exists: %s\n' "$WORKSPACE" >&2
    exit 2
fi

export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=http.https://github.com/.extraheader
export GIT_CONFIG_VALUE_0="Authorization: Bearer ${GITHUB_TOKEN}"
git clone --filter=blob:none --no-checkout \
    "https://github.com/${GITHUB_REPOSITORY}.git" "$WORKSPACE"
git -C "$WORKSPACE" fetch --no-tags origin "$GITHUB_SHA"
git -C "$WORKSPACE" checkout --detach "$GITHUB_SHA"

checked_out="$(git -C "$WORKSPACE" rev-parse HEAD)"
if [[ "$checked_out" != "$GITHUB_SHA" ]]; then
    printf 'checked out %s, expected %s\n' "$checked_out" "$GITHUB_SHA" >&2
    exit 1
fi
