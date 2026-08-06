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

basic_auth="$(printf 'x-access-token:%s' "$GITHUB_TOKEN" | openssl base64 -A)"
export GIT_TERMINAL_PROMPT=0
export GIT_CONFIG_COUNT=4
export GIT_CONFIG_KEY_0=http.https://github.com/.extraheader
export GIT_CONFIG_VALUE_0="Authorization: Basic ${basic_auth}"
export GIT_CONFIG_KEY_1=http.version
export GIT_CONFIG_VALUE_1=HTTP/1.1
export GIT_CONFIG_KEY_2=http.lowSpeedLimit
export GIT_CONFIG_VALUE_2=1
export GIT_CONFIG_KEY_3=http.lowSpeedTime
export GIT_CONFIG_VALUE_3=30

for attempt in 1 2 3; do
    if timeout 120 git clone --filter=blob:none --no-checkout \
        "https://github.com/${GITHUB_REPOSITORY}.git" "$WORKSPACE"; then
        break
    else
        status="$?"
    fi
    rm -rf -- "$WORKSPACE"
    if [[ "$attempt" -eq 3 ]]; then
        exit "$status"
    fi
    sleep "$attempt"
done

for attempt in 1 2 3; do
    if timeout 120 git -C "$WORKSPACE" fetch --no-tags origin "$GITHUB_SHA"; then
        break
    else
        status="$?"
    fi
    if [[ "$attempt" -eq 3 ]]; then
        exit "$status"
    fi
    sleep "$attempt"
done
git -C "$WORKSPACE" checkout --detach "$GITHUB_SHA"

checked_out="$(git -C "$WORKSPACE" rev-parse HEAD)"
if [[ "$checked_out" != "$GITHUB_SHA" ]]; then
    printf 'checked out %s, expected %s\n' "$checked_out" "$GITHUB_SHA" >&2
    exit 1
fi
