#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_APP_ID:?GITHUB_APP_ID is required}"
: "${GITHUB_APP_INSTALLATION_ID:?GITHUB_APP_INSTALLATION_ID is required}"
: "${GITHUB_APP_PRIVATE_KEY_FILE:?GITHUB_APP_PRIVATE_KEY_FILE is required}"

if [[ ! -r "$GITHUB_APP_PRIVATE_KEY_FILE" ]]; then
    printf 'GitHub App private key is not readable\n' >&2
    exit 1
fi

base64url() {
    openssl base64 -A | tr '+/' '-_' | tr -d '='
}

now="$(date +%s)"
issued_at="$((now - 60))"
expires_at="$((now + 540))"
header="$(printf '%s' '{"alg":"RS256","typ":"JWT"}' | base64url)"
payload="$(
    jq -cn \
        --argjson iat "$issued_at" \
        --argjson exp "$expires_at" \
        --arg iss "$GITHUB_APP_ID" \
        '{iat:$iat,exp:$exp,iss:$iss}' | base64url
)"
unsigned="${header}.${payload}"
signature="$(
    printf '%s' "$unsigned" \
        | openssl dgst -sha256 -sign "$GITHUB_APP_PRIVATE_KEY_FILE" \
        | base64url
)"
jwt="${unsigned}.${signature}"

response="$(
    curl --fail --silent --show-error \
        --request POST \
        --header 'Accept: application/vnd.github+json' \
        --header "Authorization: Bearer ${jwt}" \
        --header 'X-GitHub-Api-Version: 2022-11-28' \
        "https://api.github.com/app/installations/${GITHUB_APP_INSTALLATION_ID}/access_tokens"
)"
jq -er '.token' <<<"$response"
