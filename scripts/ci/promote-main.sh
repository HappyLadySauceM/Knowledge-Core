#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${SOURCE_ROOT:?SOURCE_ROOT is required}"
: "${OUTPUT_ROOT:?OUTPUT_ROOT is required}"

if [[ ! "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ || ! -r "$SOURCE_ROOT/VERSION" ]]; then
    printf 'a tested commit and its VERSION file are required\n' >&2
    exit 2
fi

script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
api=https://api.github.com
headers=(
    --header 'Accept: application/vnd.github+json'
    --header "Authorization: Bearer ${GITHUB_TOKEN}"
    --header 'X-GitHub-Api-Version: 2022-11-28'
)
github() {
    curl --fail --silent --show-error "${headers[@]}" "$@"
}
resolve_object() {
    local type="$1" sha="$2"
    case "$type" in
        commit) printf '%s\n' "$sha" ;;
        tag) github "${api}/repos/${GITHUB_REPOSITORY}/git/tags/${sha}" | jq -er '.object.sha' ;;
        *)
            printf 'unsupported Git tag object type: %s\n' "$type" >&2
            return 1
            ;;
    esac
}
ref_sha() {
    github "${api}/repos/${GITHUB_REPOSITORY}/git/ref/heads/$1" | jq -er '.object.sha'
}

bash "$script_root/verify-ref.sh"
main_sha="$(ref_sha main)"
release_sha="$GITHUB_SHA"

if [[ "$main_sha" != "$GITHUB_SHA" ]]; then
    comparison="$(github "${api}/repos/${GITHUB_REPOSITORY}/compare/${main_sha}...${GITHUB_SHA}" | jq -er '.status')"
    if [[ "$comparison" != ahead ]]; then
        printf 'tested dev commit %s is not a fast-forward of main %s: %s\n' \
            "$GITHUB_SHA" "$main_sha" "$comparison" >&2
        exit 1
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

    worktree="$(mktemp -d)"
    cleanup() {
        case "$worktree" in
            /tmp/*) rm -rf -- "$worktree" ;;
        esac
    }
    trap cleanup EXIT
    git -C "$worktree" init --quiet
    git -C "$worktree" remote add origin "https://github.com/${GITHUB_REPOSITORY}.git"
    timeout 120 git -C "$worktree" fetch --quiet --no-tags --depth=1 \
        origin refs/heads/main:refs/remotes/origin/main
    observed_main="$(git -C "$worktree" rev-parse refs/remotes/origin/main)"
    if [[ "$observed_main" != "$main_sha" ]]; then
        printf 'main advanced before compare-and-swap: expected %s, found %s\n' \
            "$main_sha" "$observed_main" >&2
        exit 1
    fi
    timeout 120 git -C "$worktree" fetch --quiet --no-tags --depth=1 origin "$GITHUB_SHA"
    timeout 120 git -C "$worktree" push --porcelain origin \
        "${GITHUB_SHA}:refs/heads/main"
    cleanup
    trap - EXIT
fi

if [[ "$(ref_sha main)" != "$release_sha" ]]; then
    printf 'main did not reach tested commit %s\n' "$release_sha" >&2
    exit 1
fi

series="$(tr -d '\r\n' <"$SOURCE_ROOT/VERSION")"
if [[ ! "$series" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    printf 'VERSION must contain exactly major.minor\n' >&2
    exit 2
fi
prefix="v${series}."
tags="$(github "${api}/repos/${GITHUB_REPOSITORY}/git/matching-refs/tags/${prefix}?per_page=100")"
if [[ "$(jq 'length' <<<"$tags")" -ge 100 ]]; then
    printf 'more than 99 release tags exist for %s; pagination must be handled explicitly\n' "$series" >&2
    exit 1
fi

maximum=-1
selected=''
while IFS=$'\t' read -r ref type object_sha; do
    [[ -n "$ref" ]] || continue
    if [[ ! "$ref" =~ ^refs/tags/v${series//./\.}\.([0-9]+)$ ]]; then
        continue
    fi
    patch="${BASH_REMATCH[1]}"
    if ((10#$patch > maximum)); then
        maximum=$((10#$patch))
    fi
    commit_sha="$(resolve_object "$type" "$object_sha")"
    if [[ "$commit_sha" == "$release_sha" ]]; then
        if [[ -n "$selected" && "$selected" != "${ref#refs/tags/}" ]]; then
            printf 'multiple release tags in series %s point to %s\n' "$series" "$release_sha" >&2
            exit 1
        fi
        selected="${ref#refs/tags/}"
    fi
done < <(jq -r '.[] | [.ref,.object.type,.object.sha] | @tsv' <<<"$tags")

if [[ -z "$selected" ]]; then
    selected="${prefix}$((maximum + 1))"
fi
mkdir -p "$OUTPUT_ROOT"
printf '%s\n' "$selected" >"$OUTPUT_ROOT/version"
printf '%s\n' "$release_sha" >"$OUTPUT_ROOT/release-sha"
printf 'main is at tested commit %s; release version is %s\n' "$release_sha" "$selected"
