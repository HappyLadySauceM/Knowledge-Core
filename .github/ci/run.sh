#!/usr/bin/env bash
set -euo pipefail

readonly default_goproxy="https://goproxy.cn,direct"
readonly ci_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly repository_root="$(cd "${ci_root}/../.." && pwd -P)"
readonly dockerfile="${ci_root}/Dockerfile"
readonly goproxy="${GOPROXY:-$default_goproxy}"
readonly container_proxy="${KC_CONTAINER_PROXY:-}"
readonly cache_root="${KC_CI_CACHE_ROOT:-${XDG_CACHE_HOME:-$HOME/.cache}/knowledge-core-ci}"

missing=()
for command_name in docker sha256sum; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        missing+=("$command_name")
    fi
done
if (( ${#missing[@]} > 0 )); then
    printf 'missing CI prerequisites: %s\n' "${missing[*]}" >&2
    exit 1
fi

security_options="$(docker info --format '{{json .SecurityOptions}}')"
if [[ "$security_options" != *rootless* ]]; then
    printf 'CI requires a rootless Docker daemon\n' >&2
    exit 1
fi

readonly image_key="$(sha256sum "$dockerfile" | awk '{print substr($1, 1, 16)}')"
readonly image="knowledge-core-ci:${image_key}"

build_args=(--build-arg "GOPROXY=${goproxy}")
container_env=(
    --env "GOPROXY=${goproxy}"
    --env "GOCACHE=/cache/go-build"
    --env "GOMODCACHE=/cache/go-mod"
    --env "GOPATH=/cache/gopath"
    --env "HOME=/tmp/knowledge-core-ci"
)
if [[ -n "$container_proxy" ]]; then
    build_args+=(
        --build-arg "HTTP_PROXY=${container_proxy}"
        --build-arg "HTTPS_PROXY=${container_proxy}"
        --build-arg "NO_PROXY=127.0.0.1,localhost,::1"
    )
    container_env+=(
        --env "http_proxy=${container_proxy}"
        --env "https_proxy=${container_proxy}"
        --env "HTTP_PROXY=${container_proxy}"
        --env "HTTPS_PROXY=${container_proxy}"
        --env "no_proxy=127.0.0.1,localhost,::1"
        --env "NO_PROXY=127.0.0.1,localhost,::1"
    )
fi

if ! docker image inspect "$image" >/dev/null 2>&1; then
    docker build \
        "${build_args[@]}" \
        --file "$dockerfile" \
        --label "knowledge-core.ci-image-key=${image_key}" \
        --tag "$image" \
        "$ci_root"
fi

mkdir -p "$cache_root/go-build" "$cache_root/go-mod" "$cache_root/gopath"

docker run --rm \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --pids-limit 4096 \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=4294967296 \
    --network bridge \
    "${container_env[@]}" \
    --volume "${repository_root}:/workspace:rw" \
    --volume "${cache_root}/go-build:/cache/go-build:rw" \
    --volume "${cache_root}/go-mod:/cache/go-mod:rw" \
    --volume "${cache_root}/gopath:/cache/gopath:rw" \
    --workdir /workspace \
    "$image" \
    bash -euo pipefail -c 'mkdir -p "$HOME"; make ci; make race'
