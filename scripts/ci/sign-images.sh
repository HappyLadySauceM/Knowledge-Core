#!/usr/bin/env bash
set -euo pipefail

: "${IMAGE_MAP_FILE:?IMAGE_MAP_FILE is required}"
: "${DOCKER_CONFIG:?DOCKER_CONFIG is required}"
: "${HARBOR_CA_FILE:?HARBOR_CA_FILE is required}"
: "${COSIGN_PRIVATE_KEY_FILE:?COSIGN_PRIVATE_KEY_FILE is required}"
: "${COSIGN_PUBLIC_KEY_FILE:?COSIGN_PUBLIC_KEY_FILE is required}"
: "${COSIGN_PASSWORD:?COSIGN_PASSWORD is required}"

cosign_image="${COSIGN_IMAGE:-ghcr.io/sigstore/cosign/cosign@sha256:d91bc4e7e95e8d2f549c747a72dc174f90579e410a1695f57f686674f84ce849}"
for path in "$IMAGE_MAP_FILE" "$HARBOR_CA_FILE" "$COSIGN_PRIVATE_KEY_FILE" "$COSIGN_PUBLIC_KEY_FILE" "${DOCKER_CONFIG}/config.json"; do
    if [[ ! -r "$path" ]]; then
        printf 'required signing input is not readable: %s\n' "$path" >&2
        exit 2
    fi
done

run_cosign() {
    docker run --rm \
        --cap-drop ALL \
        --security-opt no-new-privileges:true \
        --pids-limit 128 \
        --read-only \
        --network bridge \
        --env "COSIGN_PASSWORD=${COSIGN_PASSWORD}" \
        --env DOCKER_CONFIG=/secrets/harbor \
        --env SSL_CERT_DIR=/etc/ssl/certs:/etc/knowledge-core/harbor \
        --env "HTTP_PROXY=${HTTP_PROXY:-}" \
        --env "HTTPS_PROXY=${HTTPS_PROXY:-}" \
        --env "NO_PROXY=${NO_PROXY:-}" \
        --volume "${DOCKER_CONFIG}:/secrets/harbor:ro" \
        --volume "${HARBOR_CA_FILE}:/etc/knowledge-core/harbor/ca.crt:ro" \
        --volume "${COSIGN_PRIVATE_KEY_FILE}:/secrets/cosign/cosign.key:ro" \
        --volume "${COSIGN_PUBLIC_KEY_FILE}:/secrets/cosign/cosign.pub:ro" \
        "$cosign_image" "$@"
}

for service in gateway identity knowledge collaboration; do
    reference="$(sed -n "s|^knowledge-core-${service}=||p" "$IMAGE_MAP_FILE")"
    if [[ ! "$reference" =~ @sha256:[0-9a-f]{64}$ ]]; then
        printf 'missing immutable image reference for %s\n' "$service" >&2
        exit 2
    fi
    run_cosign sign --yes --key /secrets/cosign/cosign.key "$reference"
    run_cosign verify --key /secrets/cosign/cosign.pub "$reference" >/dev/null
done
