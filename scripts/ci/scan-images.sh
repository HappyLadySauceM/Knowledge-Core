#!/bin/sh
set -eu

: "${IMAGE_MAP_FILE:?IMAGE_MAP_FILE is required}"

for service in gateway identity knowledge collaboration; do
    name="knowledge-core-${service}"
    reference="$(sed -n "s|^${name}=||p" "$IMAGE_MAP_FILE")"
    if [ -z "$reference" ]; then
        printf 'missing image reference for %s\n' "$name" >&2
        exit 2
    fi
    trivy image \
        --exit-code=1 \
        --ignore-unfixed \
        --severity=HIGH,CRITICAL \
        --scanners=vuln \
        --ignorefile=/workspace/source/.trivyignore.yaml \
        --show-suppressed \
        "$reference"
done
