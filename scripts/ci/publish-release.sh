#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${RELEASE_SHA_FILE:?RELEASE_SHA_FILE is required}"
: "${VERSION_FILE:?VERSION_FILE is required}"

version="$(<"$VERSION_FILE")"
release_sha="$(<"$RELEASE_SHA_FILE")"
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    printf 'invalid release version: %s\n' "$version" >&2
    exit 2
fi
if [[ ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
    printf 'invalid release commit: %s\n' "$release_sha" >&2
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

main_sha="$(github "${api}/repos/${GITHUB_REPOSITORY}/git/ref/heads/main" | jq -er '.object.sha')"
if [[ "$main_sha" != "$release_sha" ]]; then
    printf 'main is at %s, expected release commit %s\n' "$main_sha" "$release_sha" >&2
    exit 1
fi
dev_sha="$(github "${api}/repos/${GITHUB_REPOSITORY}/git/ref/heads/dev" | jq -er '.object.sha')"
if [[ "$dev_sha" != "$GITHUB_SHA" && "$dev_sha" != "$release_sha" ]]; then
    dev_status="$(github "${api}/repos/${GITHUB_REPOSITORY}/compare/${release_sha}...${dev_sha}" | jq -er '.status')"
    if [[ "$dev_status" != ahead ]]; then
        printf 'dev is at %s and is not a descendant of release commit %s\n' \
            "$dev_sha" "$release_sha" >&2
        exit 1
    fi
fi

status="$(curl --silent --show-error --output /tmp/tag.json --write-out '%{http_code}' \
    "${headers[@]}" "${api}/repos/${GITHUB_REPOSITORY}/git/ref/tags/${version}")"
case "$status" in
    404)
        github \
            --request POST \
            --header 'Content-Type: application/json' \
            --data "$(jq -cn --arg ref "refs/tags/${version}" --arg sha "$release_sha" '{ref:$ref,sha:$sha}')" \
            "${api}/repos/${GITHUB_REPOSITORY}/git/refs" >/dev/null
        ;;
    200)
        tag_sha="$(jq -er '.object.sha' /tmp/tag.json)"
        if [[ "$tag_sha" != "$release_sha" ]]; then
            printf 'Git tag %s points to %s, expected %s\n' "$version" "$tag_sha" "$release_sha" >&2
            exit 1
        fi
        ;;
    *)
        printf 'GitHub returned HTTP %s while checking tag %s\n' "$status" "$version" >&2
        exit 1
        ;;
esac

status="$(curl --silent --show-error --output /tmp/release.json --write-out '%{http_code}' \
    "${headers[@]}" "${api}/repos/${GITHUB_REPOSITORY}/releases/tags/${version}")"
case "$status" in
    404)
        github \
            --request POST \
            --header 'Content-Type: application/json' \
            --data "$(jq -cn --arg tag "$version" --arg sha "$release_sha" \
                '{tag_name:$tag,target_commitish:$sha,name:$tag,generate_release_notes:true,draft:false,prerelease:false}')" \
            "${api}/repos/${GITHUB_REPOSITORY}/releases" >/dev/null
        ;;
    200)
        if [[ "$(jq -er '.tag_name' /tmp/release.json)" != "$version" ]]; then
            printf 'GitHub Release tag mismatch for %s\n' "$version" >&2
            exit 1
        fi
        ;;
    *)
        printf 'GitHub returned HTTP %s while checking release %s\n' "$status" "$version" >&2
        exit 1
        ;;
esac

printf 'Published %s for tested commit %s\n' "$version" "$release_sha"
