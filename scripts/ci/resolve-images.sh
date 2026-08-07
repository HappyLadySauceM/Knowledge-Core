#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${IMAGE_REGISTRY:?IMAGE_REGISTRY is required}"
: "${IMAGE_NAMESPACE:?IMAGE_NAMESPACE is required}"
: "${DOCKER_CONFIG:?DOCKER_CONFIG is required}"
: "${HARBOR_CA_FILE:?HARBOR_CA_FILE is required}"
: "${OUTPUT_ROOT:?OUTPUT_ROOT is required}"

if [[ ! "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]]; then
    printf 'GITHUB_SHA must be a lowercase 40-character SHA-1 value\n' >&2
    exit 2
fi
if [[ "$HARBOR_CA_FILE" != /* || ! -r "$HARBOR_CA_FILE" ]]; then
    printf 'HARBOR_CA_FILE must be an absolute readable file\n' >&2
    exit 2
fi

config_file="${DOCKER_CONFIG}/config.json"
auth="$({
    jq -er --arg registry "$IMAGE_REGISTRY" \
        '.auths[$registry].auth // .auths["https://" + $registry].auth' \
        "$config_file"
} | base64 -d)"
accept='application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'

mkdir -p "$OUTPUT_ROOT"
temporary="${OUTPUT_ROOT}/images.env.tmp"
: >"$temporary"
trap 'rm -f -- "$temporary"' EXIT

for service in gateway identity knowledge collaboration; do
    repository="${IMAGE_NAMESPACE}/${service}"
    headers="${OUTPUT_ROOT}/${service}.headers"
    curl --fail --silent --show-error \
        --cacert "$HARBOR_CA_FILE" \
        --user "$auth" \
        --dump-header "$headers" \
        --output /dev/null \
        --header "Accept: ${accept}" \
        "https://${IMAGE_REGISTRY}/v2/${repository}/manifests/${GITHUB_SHA}"
    digest="$(awk 'BEGIN{IGNORECASE=1} /^Docker-Content-Digest:/ {gsub("\r", "", $2); print $2}' "$headers")"
    if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
        printf 'Harbor returned an invalid digest for %s:%s\n' "$repository" "$GITHUB_SHA" >&2
        exit 1
    fi

    if [[ -n "${IMAGE_METADATA_ROOT:-}" ]]; then
        metadata_digest="$(jq -er '.["containerimage.digest"]' "${IMAGE_METADATA_ROOT}/${service}.json")"
        if [[ "$metadata_digest" != "$digest" ]]; then
            printf 'pushed digest mismatch for %s: build=%s registry=%s\n' \
                "$service" "$metadata_digest" "$digest" >&2
            exit 1
        fi
    fi
    printf 'knowledge-core-%s=%s/%s@%s\n' \
        "$service" "$IMAGE_REGISTRY" "$repository" "$digest" >>"$temporary"
    rm -f -- "$headers"
done

mv -- "$temporary" "${OUTPUT_ROOT}/images.env"
trap - EXIT
