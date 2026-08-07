#!/bin/sh
set -eu

: "${IMAGE_MAP_FILE:?IMAGE_MAP_FILE is required}"

ignore_file="${TRIVY_IGNORE_FILE:-/workspace/source/.trivyignore.yaml}"
if [ ! -r "$ignore_file" ]; then
    printf 'Trivy ignore file is not readable: %s\n' "$ignore_file" >&2
    exit 2
fi

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
        --ignorefile="$ignore_file" \
        --show-suppressed \
        "$reference"
done
