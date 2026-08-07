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

commit() {
    github "${api}/repos/${GITHUB_REPOSITORY}/git/commits/$1"
}

ref_sha() {
    github "${api}/repos/${GITHUB_REPOSITORY}/git/ref/heads/$1" | jq -er '.object.sha'
}

validate_merge_commit() {
    local document="$1" base_sha="$2" head_sha="$3" head_tree="$4"
    jq -e \
        --arg base "$base_sha" \
        --arg head "$head_sha" \
        --arg tree "$head_tree" \
        '(.parents | length) == 2
         and .parents[0].sha == $base
         and .parents[1].sha == $head
         and .tree.sha == $tree' \
        <<<"$document" >/dev/null
}

bash "$SOURCE_ROOT/scripts/ci/verify-ref.sh"
main_sha="$(ref_sha main)"
head_commit="$(commit "$GITHUB_SHA")"
head_tree="$(jq -er '.tree.sha' <<<"$head_commit")"
main_commit="$(commit "$main_sha")"
release_sha=''

if [[ "$(jq -r '.parents | length' <<<"$main_commit")" == 2 ]] \
    && [[ "$(jq -r '.parents[1].sha' <<<"$main_commit")" == "$GITHUB_SHA" ]] \
    && [[ "$(jq -r '.tree.sha' <<<"$main_commit")" == "$head_tree" ]]; then
    release_sha="$main_sha"
else
    if [[ "$main_sha" == "$GITHUB_SHA" ]]; then
        printf 'main points directly to %s; merge-commit promotion is required\n' "$GITHUB_SHA" >&2
        exit 1
    fi
    comparison="$(github "${api}/repos/${GITHUB_REPOSITORY}/compare/${main_sha}...${GITHUB_SHA}" | jq -er '.status')"
    if [[ "$comparison" != ahead ]]; then
        printf 'dev cannot be merged from main %s at %s: %s\n' "$main_sha" "$GITHUB_SHA" "$comparison" >&2
        exit 1
    fi

    owner="${GITHUB_REPOSITORY%%/*}"
    pulls="$(github \
        --get \
        --data-urlencode state=open \
        --data-urlencode base=main \
        --data-urlencode "head=${owner}:dev" \
        --data-urlencode per_page=2 \
        "${api}/repos/${GITHUB_REPOSITORY}/pulls")"
    pull_count="$(jq -r 'length' <<<"$pulls")"
    if ((pull_count > 1)); then
        printf 'multiple open dev -> main pull requests exist\n' >&2
        exit 1
    fi
    if ((pull_count == 0)); then
        pull="$(github \
            --request POST \
            --header 'Content-Type: application/json' \
            --data "$(jq -cn \
                --arg title "release: promote dev ${GITHUB_SHA:0:12}" \
                --arg body "Automated after knowledge-core/smoke passed for ${GITHUB_SHA}." \
                '{title:$title,head:"dev",base:"main",body:$body,maintainer_can_modify:false}')" \
            "${api}/repos/${GITHUB_REPOSITORY}/pulls")"
    else
        pull="$(jq -c '.[0]' <<<"$pulls")"
    fi
    if ! jq -e \
        --arg repository "$GITHUB_REPOSITORY" \
        --arg head "$GITHUB_SHA" \
        '.state == "open"
         and .base.ref == "main"
         and .head.ref == "dev"
         and .head.sha == $head
         and .head.repo.full_name == $repository' \
        <<<"$pull" >/dev/null; then
        printf 'dev -> main pull request does not match the tested revision\n' >&2
        exit 1
    fi
    pull_number="$(jq -er '.number' <<<"$pull")"

    merge_payload="$(jq -cn \
        --arg title "Merge pull request #${pull_number} from ${owner}/dev" \
        --arg message "Automated promotion after knowledge-core/smoke passed for ${GITHUB_SHA}." \
        --arg sha "$GITHUB_SHA" \
        '{commit_title:$title,commit_message:$message,sha:$sha,merge_method:"merge"}')"
    merge_response=''
    merge_status=''
    for attempt in $(seq 1 6); do
        response="$(curl --silent --show-error \
            --request PUT \
            "${headers[@]}" \
            --header 'Content-Type: application/json' \
            --data "$merge_payload" \
            --write-out $'\n%{http_code}' \
            "${api}/repos/${GITHUB_REPOSITORY}/pulls/${pull_number}/merge")"
        merge_status="${response##*$'\n'}"
        merge_response="${response%$'\n'*}"
        if [[ "$merge_status" == 200 ]]; then
            break
        fi
        if [[ "$merge_status" != 405 || "$attempt" == 6 ]]; then
            message="$(jq -r '.message // "unknown GitHub error"' <<<"$merge_response")"
            printf 'GitHub refused merge for PR #%s: HTTP %s: %s\n' \
                "$pull_number" "$merge_status" "$message" >&2
            exit 1
        fi
        bash "$SOURCE_ROOT/scripts/ci/verify-ref.sh"
        sleep 2
    done
    if [[ "$(jq -er '.merged' <<<"$merge_response")" != true ]]; then
        printf 'GitHub did not merge PR #%s\n' "$pull_number" >&2
        exit 1
    fi
    release_sha="$(jq -er '.sha' <<<"$merge_response")"
    merged_commit="$(commit "$release_sha")"
    if ! validate_merge_commit "$merged_commit" "$main_sha" "$GITHUB_SHA" "$head_tree"; then
        printf 'merge commit %s does not preserve the expected base, head, and tree\n' "$release_sha" >&2
        exit 1
    fi
fi

if [[ "$(ref_sha main)" != "$release_sha" ]]; then
    printf 'main moved after promotion; expected %s\n' "$release_sha" >&2
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
printf 'main is at merge commit %s; release version is %s\n' "$release_sha" "$selected"
