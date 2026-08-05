#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${GITHUB_REF:?GITHUB_REF is required}"
: "${GITOPS_REPOSITORY:?GITOPS_REPOSITORY is required}"
: "${SOURCE_ROOT:?SOURCE_ROOT is required}"
: "${IMAGE_MAP_FILE:?IMAGE_MAP_FILE is required}"

if [[ ! -d "$SOURCE_ROOT/deploy/base" || ! -r "$IMAGE_MAP_FILE" ]]; then
    printf 'source base and image map are required\n' >&2
    exit 2
fi

export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=http.https://github.com/.extraheader
export GIT_CONFIG_VALUE_0="Authorization: Bearer ${GITHUB_TOKEN}"

for attempt in 1 2 3; do
    /usr/local/lib/knowledge-core-ci/verify-ref.sh
    worktree="$(mktemp -d)"
    cleanup() {
        case "$worktree" in
            /tmp/*) rm -rf -- "$worktree" ;;
        esac
    }
    trap cleanup EXIT

    git clone --depth=1 --branch=main \
        "https://github.com/${GITOPS_REPOSITORY}.git" "$worktree/repository"
    target_root="$worktree/repository/Knowledge-Core"
    rm -rf -- "$target_root/base"
    mkdir -p "$target_root"
    cp -R "$SOURCE_ROOT/deploy/base" "$target_root/base"

    overlay="$target_root/overlay/dev"
    if [[ ! -f "$overlay/kustomization.yaml" ]]; then
        printf 'GitOps overlay does not exist: %s\n' "$overlay" >&2
        exit 1
    fi
    pushd "$overlay" >/dev/null
    declare -A updated_images=()
    while IFS='=' read -r name reference; do
        if [[ -z "$name" || ! "$reference" =~ ^[-a-zA-Z0-9._:/]+@sha256:[0-9a-f]{64}$ ]]; then
            printf 'invalid image mapping for %s\n' "$name" >&2
            exit 2
        fi
        image_name="${name#knowledge-core-}"
        if [[ ! "$reference" =~ /${image_name}@sha256:[0-9a-f]{64}$ ]]; then
            printf 'image mapping name does not match its reference: %s\n' "$name" >&2
            exit 2
        fi
        if [[ "$name" == knowledge-core-configctl ]]; then
            continue
        fi
        case "$name" in
            knowledge-core-gateway | knowledge-core-identity | knowledge-core-knowledge | knowledge-core-collaboration) ;;
            *)
                printf 'unexpected deployable image mapping: %s\n' "$name" >&2
                exit 2
                ;;
        esac
        if [[ -n "${updated_images[$name]+present}" ]]; then
            printf 'duplicate deployable image mapping: %s\n' "$name" >&2
            exit 2
        fi
        kustomize edit set image "${name}=${reference}"
        updated_images[$name]=present
    done <"$IMAGE_MAP_FILE"
    for required in \
        knowledge-core-gateway \
        knowledge-core-identity \
        knowledge-core-knowledge \
        knowledge-core-collaboration; do
        if [[ -z "${updated_images[$required]+present}" ]]; then
            printf 'missing deployable image mapping: %s\n' "$required" >&2
            exit 2
        fi
    done
    popd >/dev/null

    git -C "$worktree/repository" add Knowledge-Core
    if git -C "$worktree/repository" diff --cached --quiet; then
        printf 'GitOps is already current for %s\n' "$GITHUB_SHA"
        exit 0
    fi
    git -C "$worktree/repository" \
        -c user.name='knowledge-core-ci[bot]' \
        -c user.email='knowledge-core-ci[bot]@users.noreply.github.com' \
        commit -m "deploy(dev): promote ${GITHUB_SHA}"
    if git -C "$worktree/repository" push origin HEAD:main; then
        exit 0
    fi

    cleanup
    trap - EXIT
    if [[ "$attempt" -lt 3 ]]; then
        sleep "$attempt"
    fi
done

printf 'GitOps promotion lost three compare-and-swap attempts\n' >&2
exit 1
