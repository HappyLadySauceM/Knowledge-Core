#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${SOURCE_ROOT:?SOURCE_ROOT is required}"
: "${OUTPUT_ROOT:?OUTPUT_ROOT is required}"

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
    if [[ "$type" == commit ]]; then
        printf '%s\n' "$sha"
    elif [[ "$type" == tag ]]; then
        github "${api}/repos/${GITHUB_REPOSITORY}/git/tags/${sha}" | jq -er '.object.sha'
    else
        printf 'unsupported Git tag object type: %s\n' "$type" >&2
        return 1
    fi
}

bash "$SOURCE_ROOT/scripts/ci/verify-ref.sh"
main_sha="$(github "${api}/repos/${GITHUB_REPOSITORY}/git/ref/heads/main" | jq -er '.object.sha')"
if [[ "$main_sha" != "$GITHUB_SHA" ]]; then
    comparison="$(github "${api}/repos/${GITHUB_REPOSITORY}/compare/${main_sha}...${GITHUB_SHA}" | jq -er '.status')"
    if [[ "$comparison" != ahead ]]; then
        printf 'main cannot fast-forward from %s to %s: %s\n' "$main_sha" "$GITHUB_SHA" "$comparison" >&2
        exit 1
    fi
    github \
        --request PATCH \
        --header 'Content-Type: application/json' \
        --data "$(jq -cn --arg sha "$GITHUB_SHA" '{sha:$sha,force:false}')" \
        "${api}/repos/${GITHUB_REPOSITORY}/git/refs/heads/main" >/dev/null
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
    if [[ "$commit_sha" == "$GITHUB_SHA" ]]; then
        if [[ -n "$selected" && "$selected" != "${ref#refs/tags/}" ]]; then
            printf 'multiple release tags in series %s point to %s\n' "$series" "$GITHUB_SHA" >&2
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
printf 'main is at %s; release version is %s\n' "$GITHUB_SHA" "$selected"
