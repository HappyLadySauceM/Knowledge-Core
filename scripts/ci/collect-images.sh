#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${IMAGE_REGISTRY:?IMAGE_REGISTRY is required}"
: "${IMAGE_NAMESPACE:?IMAGE_NAMESPACE is required}"

metadata_root=/workspace/image-metadata
output_root=/workspace/release
mkdir -p "$output_root"
images='[]'
: >"$output_root/images.env"

for name in gateway identity knowledge collaboration; do
    metadata="$metadata_root/$name.json"
    digest="$(jq -er '.["containerimage.digest"]' "$metadata")"
    if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
        printf 'invalid digest for %s\n' "$name" >&2
        exit 1
    fi
    reference="${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/${name}@${digest}"
    images="$(
        jq -cn \
            --argjson images "$images" \
            --arg name "$name" \
            --arg ref "$reference" \
            '$images + [{name:$name,ref:$ref}]'
    )"
    printf 'knowledge-core-%s=%s\n' "$name" "$reference" \
        >>"$output_root/images.env"
done

printf '%s\n' "$images" >"$output_root/images.json"
