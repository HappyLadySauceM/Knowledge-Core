#!/bin/sh
set -eu

: "${SOURCE_ROOT:?SOURCE_ROOT is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${IMAGE_REGISTRY:?IMAGE_REGISTRY is required}"
: "${IMAGE_NAMESPACE:?IMAGE_NAMESPACE is required}"
: "${HTTP_PROXY:?HTTP_PROXY is required}"
: "${HTTPS_PROXY:?HTTPS_PROXY is required}"
: "${NO_PROXY:?NO_PROXY is required}"
: "${GOPROXY:?GOPROXY is required}"

if [ "$SOURCE_ROOT" != "/workspace/source" ] || [ ! -d "$SOURCE_ROOT/.git" ]; then
    printf 'validated source checkout is required at /workspace/source\n' >&2
    exit 2
fi

metadata_root=/workspace/image-metadata
maximum_attempts=3
mkdir -p "$metadata_root"

build_image() {
    name="$1"
    dockerfile="$2"
    reference="${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/${name}:${GITHUB_SHA}"
    metadata_file="${metadata_root}/${name}.json"
    temporary_metadata_file="${metadata_file}.tmp"
    attempt=1

    while [ "$attempt" -le "$maximum_attempts" ]; do
        rm -f -- "$temporary_metadata_file"
        if buildctl-daemonless.sh build \
            --frontend dockerfile.v0 \
            --local "context=${SOURCE_ROOT}" \
            --local "dockerfile=${SOURCE_ROOT}" \
            --opt "filename=${dockerfile}" \
            --opt platform=linux/amd64 \
            --opt "build-arg:HTTP_PROXY=${HTTP_PROXY}" \
            --opt "build-arg:HTTPS_PROXY=${HTTPS_PROXY}" \
            --opt "build-arg:NO_PROXY=${NO_PROXY}" \
            --opt "build-arg:http_proxy=${HTTP_PROXY}" \
            --opt "build-arg:https_proxy=${HTTPS_PROXY}" \
            --opt "build-arg:no_proxy=${NO_PROXY}" \
            --opt "build-arg:GOPROXY=${GOPROXY}" \
            --output "type=image,name=${reference},push=true" \
            --metadata-file "$temporary_metadata_file"; then
            mv -- "$temporary_metadata_file" "$metadata_file"
            return 0
        fi

        rm -f -- "$temporary_metadata_file"
        if [ "$attempt" -eq "$maximum_attempts" ]; then
            printf 'Build and push for %s failed after %d attempts\n' \
                "$name" "$maximum_attempts" >&2
            return 1
        fi

        next_attempt=$((attempt + 1))
        printf 'Retrying build and push for %s (%d/%d)\n' \
            "$name" "$next_attempt" "$maximum_attempts" >&2
        sleep $((attempt * 5))
        attempt="$next_attempt"
    done
}

build_image gateway docker/gateway/dockerfile
build_image identity docker/identity/dockerfile
build_image knowledge docker/knowledge/dockerfile
build_image collaboration docker/collaboration/dockerfile
