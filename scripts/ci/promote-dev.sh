#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${GITHUB_REF:?GITHUB_REF is required}"

if [[ "$GITHUB_REF" != refs/heads/dev ]]; then
    printf 'source promotion only accepts refs/heads/dev\n' >&2
    exit 2
fi
/usr/local/lib/knowledge-core-ci/verify-ref.sh

owner="${GITHUB_REPOSITORY%%/*}"
repository="${GITHUB_REPOSITORY#*/}"
main_sha="$(
    curl --fail --silent --show-error \
        --header 'Accept: application/vnd.github+json' \
        --header "Authorization: Bearer ${GITHUB_TOKEN}" \
        --header 'X-GitHub-Api-Version: 2022-11-28' \
        "https://api.github.com/repos/${GITHUB_REPOSITORY}/git/ref/heads/main" \
        | jq -er '.object.sha'
)"
if [[ "$main_sha" == "$GITHUB_SHA" ]]; then
    printf 'dev and main already point to %s\n' "$GITHUB_SHA"
    exit 0
fi

pulls="$(
    curl --fail --silent --show-error \
        --get \
        --header 'Accept: application/vnd.github+json' \
        --header "Authorization: Bearer ${GITHUB_TOKEN}" \
        --header 'X-GitHub-Api-Version: 2022-11-28' \
        --data-urlencode state=open \
        --data-urlencode base=main \
        --data-urlencode "head=${owner}:dev" \
        "https://api.github.com/repos/${GITHUB_REPOSITORY}/pulls"
)"
count="$(jq -er 'length' <<<"$pulls")"
if ((count > 1)); then
    printf 'multiple dev-to-main pull requests are open\n' >&2
    exit 1
fi

if ((count == 0)); then
    payload="$(
        jq -cn \
            --arg title 'chore: promote dev to main' \
            --arg head dev \
            --arg base main \
            --arg body 'Automated promotion of the verified dev branch to main.' \
            '{title:$title,head:$head,base:$base,body:$body}'
    )"
    pull="$(
        curl --fail --silent --show-error \
            --request POST \
            --header 'Accept: application/vnd.github+json' \
            --header "Authorization: Bearer ${GITHUB_TOKEN}" \
            --header 'Content-Type: application/json' \
            --header 'X-GitHub-Api-Version: 2022-11-28' \
            --data "$payload" \
            "https://api.github.com/repos/${GITHUB_REPOSITORY}/pulls"
    )"
else
    pull="$(jq -c '.[0]' <<<"$pulls")"
fi

number="$(jq -er '.number' <<<"$pull")"
head_sha="$(jq -er '.head.sha' <<<"$pull")"
if [[ "$head_sha" != "$GITHUB_SHA" ]]; then
    printf 'promotion pull request points to %s, expected %s\n' "$head_sha" "$GITHUB_SHA" >&2
    exit 75
fi

query='query($owner:String!,$repository:String!,$number:Int!){repository(owner:$owner,name:$repository){pullRequest(number:$number){id autoMergeRequest{enabledAt}}}}'
payload="$(jq -cn --arg query "$query" --arg owner "$owner" --arg repository "$repository" --argjson number "$number" '{query:$query,variables:{owner:$owner,repository:$repository,number:$number}}')"
result="$(
    curl --fail --silent --show-error \
        --request POST \
        --header 'Accept: application/vnd.github+json' \
        --header "Authorization: Bearer ${GITHUB_TOKEN}" \
        --header 'Content-Type: application/json' \
        --header 'X-GitHub-Api-Version: 2022-11-28' \
        --data "$payload" \
        https://api.github.com/graphql
)"
pull_id="$(jq -er '.data.repository.pullRequest.id' <<<"$result")"
if [[ "$(jq -r '.data.repository.pullRequest.autoMergeRequest.enabledAt // empty' <<<"$result")" != '' ]]; then
    exit 0
fi

mutation='mutation($pullRequestId:ID!){enablePullRequestAutoMerge(input:{pullRequestId:$pullRequestId,mergeMethod:MERGE}){pullRequest{id}}}'
payload="$(jq -cn --arg query "$mutation" --arg pullRequestId "$pull_id" '{query:$query,variables:{pullRequestId:$pullRequestId}}')"
result="$(
    curl --fail --silent --show-error \
        --request POST \
        --header 'Accept: application/vnd.github+json' \
        --header "Authorization: Bearer ${GITHUB_TOKEN}" \
        --header 'Content-Type: application/json' \
        --header 'X-GitHub-Api-Version: 2022-11-28' \
        --data "$payload" \
        https://api.github.com/graphql
)"
jq -e '.data.enablePullRequestAutoMerge.pullRequest.id and ((.errors // []) | length == 0)' \
    >/dev/null <<<"$result"
