#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITOPS_REPOSITORY:?GITOPS_REPOSITORY is required}"
: "${PROMOTED_REVISION:?PROMOTED_REVISION is required}"
: "${PREVIOUS_REVISION:?PREVIOUS_REVISION is required}"
: "${GITOPS_CHANGED:?GITOPS_CHANGED is required}"

if [[ "$GITOPS_CHANGED" != true ]]; then
    printf 'GitOps promotion created no commit; rollback is unnecessary\n'
    exit 0
fi

basic_auth="$(printf 'x-access-token:%s' "$GITHUB_TOKEN" | openssl base64 -A)"
export GIT_TERMINAL_PROMPT=0
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=http.https://github.com/.extraheader
export GIT_CONFIG_VALUE_0="Authorization: Basic ${basic_auth}"

worktree="$(mktemp -d)"
trap 'case "$worktree" in /tmp/*) rm -rf -- "$worktree" ;; esac' EXIT
timeout 120 git clone --depth=2 --branch=main \
    "https://github.com/${GITOPS_REPOSITORY}.git" "$worktree/repository"
current="$(git -C "$worktree/repository" rev-parse HEAD)"
if [[ "$current" != "$PROMOTED_REVISION" ]]; then
    printf 'GitOps main advanced to %s; refusing to revert promotion %s\n' \
        "$current" "$PROMOTED_REVISION" >&2
    exit 1
fi
parent="$(git -C "$worktree/repository" rev-parse "${PROMOTED_REVISION}^")"
if [[ "$parent" != "$PREVIOUS_REVISION" ]]; then
    printf 'promotion parent %s does not match recorded revision %s\n' \
        "$parent" "$PREVIOUS_REVISION" >&2
    exit 1
fi

git -C "$worktree/repository" \
    -c user.name='knowledge-core-ci[bot]' \
    -c user.email='knowledge-core-ci[bot]@users.noreply.github.com' \
    revert --no-edit "$PROMOTED_REVISION"
rollback_revision="$(git -C "$worktree/repository" rev-parse HEAD)"
timeout 120 git -C "$worktree/repository" push origin HEAD:main

GITOPS_REVISION="$rollback_revision" IMAGE_MAP_FILE= \
    bash /workspace/source/scripts/ci/wait-gitops.sh
