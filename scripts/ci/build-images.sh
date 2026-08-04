#!/bin/sh
set -eu

: "${SOURCE_ROOT:?SOURCE_ROOT is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${IMAGE_REGISTRY:?IMAGE_REGISTRY is required}"
: "${IMAGE_NAMESPACE:?IMAGE_NAMESPACE is required}"

if [ "$SOURCE_ROOT" != "/workspace/source" ] || [ ! -d "$SOURCE_ROOT/.git" ]; then
    printf 'validated source checkout is required at /workspace/source\n' >&2
    exit 2
fi

metadata_root=/workspace/image-metadata
mkdir -p "$metadata_root"

build_image() {
    name="$1"
    dockerfile="$2"
    reference="${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/${name}:${GITHUB_SHA}"
    buildctl-daemonless.sh build \
        --frontend dockerfile.v0 \
        --local "context=${SOURCE_ROOT}" \
        --local "dockerfile=${SOURCE_ROOT}" \
        --opt "filename=${dockerfile}" \
        --opt platform=linux/amd64 \
        --output "type=image,name=${reference},push=true" \
        --metadata-file "${metadata_root}/${name}.json"
}

build_image gateway docker/gateway/dockerfile
build_image identity docker/identity/dockerfile
build_image knowledge docker/knowledge/dockerfile
build_image collaboration docker/collaboration/dockerfile
build_image configctl docker/configctl/dockerfile
