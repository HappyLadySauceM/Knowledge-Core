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

case "$GITHUB_SHA" in
    *[!0-9a-f]*)
        printf 'GITHUB_SHA must be a lowercase 40-character SHA-1 value\n' >&2
        exit 2
        ;;
esac
if [ "${#GITHUB_SHA}" -ne 40 ]; then
    printf 'GITHUB_SHA must be a lowercase 40-character SHA-1 value\n' >&2
    exit 2
fi

builder="${KC_IMAGE_BUILDER:-buildkit-daemonless}"
metadata_root="${IMAGE_METADATA_ROOT:-/workspace/image-metadata}"
maximum_attempts=3

if [ ! -d "$SOURCE_ROOT/.git" ]; then
    printf 'validated source checkout is required at %s\n' "$SOURCE_ROOT" >&2
    exit 2
fi
case "$builder" in
    buildkit-daemonless)
        if [ "$SOURCE_ROOT" != /workspace/source ]; then
            printf 'Argo BuildKit requires SOURCE_ROOT=/workspace/source\n' >&2
            exit 2
        fi
        command -v buildctl-daemonless.sh >/dev/null 2>&1 || {
            printf 'buildctl-daemonless.sh is required\n' >&2
            exit 2
        }
        ;;
    docker-buildx)
        command -v docker >/dev/null 2>&1 || {
            printf 'docker is required\n' >&2
            exit 2
        }
        docker buildx version >/dev/null
        ;;
    *)
        printf 'unsupported KC_IMAGE_BUILDER: %s\n' "$builder" >&2
        exit 2
        ;;
esac

mkdir -p "$metadata_root"

build_with_buildkit() {
    dockerfile="$1"
    reference="$2"
    metadata_file="$3"

    buildctl-daemonless.sh build \
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
        --metadata-file "$metadata_file"
}

build_with_docker() {
    dockerfile="$1"
    reference="$2"
    metadata_file="$3"

    docker buildx build \
        --platform linux/amd64 \
        --file "${SOURCE_ROOT}/${dockerfile}" \
        --build-arg "HTTP_PROXY=${HTTP_PROXY}" \
        --build-arg "HTTPS_PROXY=${HTTPS_PROXY}" \
        --build-arg "NO_PROXY=${NO_PROXY}" \
        --build-arg "http_proxy=${HTTP_PROXY}" \
        --build-arg "https_proxy=${HTTPS_PROXY}" \
        --build-arg "no_proxy=${NO_PROXY}" \
        --build-arg "GOPROXY=${GOPROXY}" \
        --metadata-file "$metadata_file" \
        --push \
        --tag "$reference" \
        "$SOURCE_ROOT"
}

build_image() {
    name="$1"
    dockerfile="$2"
    reference="${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/${name}:${GITHUB_SHA}"
    metadata_file="${metadata_root}/${name}.json"
    temporary_metadata_file="${metadata_file}.tmp"
    attempt=1

    while [ "$attempt" -le "$maximum_attempts" ]; do
        rm -f -- "$temporary_metadata_file"
        if [ "$builder" = buildkit-daemonless ]; then
            build_with_buildkit "$dockerfile" "$reference" "$temporary_metadata_file" && succeeded=true || succeeded=false
        else
            build_with_docker "$dockerfile" "$reference" "$temporary_metadata_file" && succeeded=true || succeeded=false
        fi
        if [ "$succeeded" = true ]; then
            mv -- "$temporary_metadata_file" "$metadata_file"
            return 0
        fi

        rm -f -- "$temporary_metadata_file"
        if [ "$attempt" -eq "$maximum_attempts" ]; then
            printf 'Build and push for %s failed after %d attempts\n' \
                "$name" "$maximum_attempts" >&2
            return 1
        fi
        attempt=$((attempt + 1))
        printf 'Retrying build and push for %s (%d/%d)\n' \
            "$name" "$attempt" "$maximum_attempts" >&2
        sleep $(((attempt - 1) * 5))
    done
}

build_image gateway docker/gateway/dockerfile
build_image identity docker/identity/dockerfile
build_image knowledge docker/knowledge/dockerfile
build_image collaboration docker/collaboration/dockerfile
