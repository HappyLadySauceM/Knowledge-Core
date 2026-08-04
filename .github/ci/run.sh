#!/usr/bin/env bash
set -euo pipefail

readonly default_goproxy="https://goproxy.cn,direct"
readonly ci_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly repository_root="$(cd "${ci_root}/../.." && pwd -P)"
readonly dockerfile="${ci_root}/Dockerfile"
readonly rust_dockerfile="${ci_root}/RustDockerfile"
readonly node_image="node:24.18.1-bookworm-slim"
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
readonly rust_image_key="$(sha256sum "$rust_dockerfile" | awk '{print substr($1, 1, 16)}')"
readonly rust_image="knowledge-core-rust-ci:${rust_image_key}"

build_args=(--build-arg "GOPROXY=${goproxy}")
proxy_build_args=()
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
    proxy_build_args+=(
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

if ! docker image inspect "$rust_image" >/dev/null 2>&1; then
    docker build \
        "${build_args[@]}" \
        --file "$rust_dockerfile" \
        --label "knowledge-core.rust-ci-image-key=${rust_image_key}" \
        --tag "$rust_image" \
        "$ci_root"
fi

mkdir -p "$cache_root/go-build" "$cache_root/go-mod" "$cache_root/gopath"
mkdir -p "$cache_root/npm"
mkdir -p "$cache_root/cargo-home" "$cache_root/cargo-target"

node_container_env=(
    --env "HOME=/tmp/knowledge-core-node-ci"
    --env "npm_config_cache=/cache/npm"
)
if [[ -n "$container_proxy" ]]; then
    node_container_env+=(
        --env "http_proxy=${container_proxy}"
        --env "https_proxy=${container_proxy}"
        --env "HTTP_PROXY=${container_proxy}"
        --env "HTTPS_PROXY=${container_proxy}"
        --env "no_proxy=127.0.0.1,localhost,::1"
        --env "NO_PROXY=127.0.0.1,localhost,::1"
    )
fi

docker run --rm \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --pids-limit 2048 \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=1073741824 \
    --network bridge \
    "${node_container_env[@]}" \
    --volume "${repository_root}:/workspace:rw" \
    --volume "${cache_root}/npm:/cache/npm:rw" \
    --tmpfs /workspace/services/collaboration/interop/node_modules:rw,exec,nosuid,nodev,mode=1777,size=1073741824 \
    --workdir /workspace/services/collaboration/interop \
    "$node_image" \
    sh -eu -c 'mkdir -p "$HOME"; npm ci; npm run ci'

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
    bash -euo pipefail -c 'mkdir -p "$HOME"; make go-ci; make race'

readonly run_suffix="$(date +%s)-$$"
readonly dependency_network="knowledge-core-ci-${run_suffix}"
readonly postgres_name="knowledge-core-ci-postgres-${run_suffix}"
readonly redis_name="knowledge-core-ci-redis-${run_suffix}"
readonly nats_name="knowledge-core-ci-nats-${run_suffix}"
readonly etcd_name="knowledge-core-ci-etcd-${run_suffix}"
readonly rust_interop_name="knowledge-core-ci-rust-interop-${run_suffix}"
readonly go_interop_name="knowledge-core-ci-go-interop-${run_suffix}"
readonly collaboration_image="knowledge-core-collaboration-ci:${run_suffix}"
readonly interop_root="${cache_root}/interop-${run_suffix}"
readonly postgres_password="$(printf '%s' "${run_suffix}-${RANDOM}" | sha256sum | awk '{print $1}')"

cleanup_dependencies() {
    docker rm --force \
        "$postgres_name" "$redis_name" "$nats_name" "$etcd_name" \
        "$rust_interop_name" "$go_interop_name" >/dev/null 2>&1 || true
    docker network rm "$dependency_network" >/dev/null 2>&1 || true
    docker image rm --force "$collaboration_image" >/dev/null 2>&1 || true
    case "$interop_root" in
        "$cache_root"/interop-*) rm -rf -- "$interop_root" ;;
    esac
}
trap cleanup_dependencies EXIT

wait_healthy() {
    local container_name="$1" status
    for _ in $(seq 1 90); do
        status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container_name")"
        case "$status" in
            healthy) return 0 ;;
            unhealthy|exited|dead)
                docker logs "$container_name" >&2 || true
                return 1
                ;;
        esac
        sleep 1
    done
    docker logs "$container_name" >&2 || true
    printf 'dependency container %s did not become healthy\n' "$container_name" >&2
    return 1
}

wait_etcd() {
    local container_name="$1" status
    for _ in $(seq 1 90); do
        if docker exec "$container_name" /usr/local/bin/etcdctl \
            --endpoints=http://127.0.0.1:2379 endpoint health >/dev/null 2>&1; then
            return 0
        fi
        status="$(docker inspect --format '{{.State.Status}}' "$container_name")"
        case "$status" in
            exited|dead)
                docker logs "$container_name" >&2 || true
                return 1
                ;;
        esac
        sleep 1
    done
    docker logs "$container_name" >&2 || true
    printf 'dependency container %s did not become healthy\n' "$container_name" >&2
    return 1
}

wait_ready_log() {
    local container_name="$1" status
    for _ in $(seq 1 60); do
        if docker logs "$container_name" 2>&1 | grep -q '^READY '; then
            return 0
        fi
        status="$(docker inspect --format '{{.State.Status}}' "$container_name")"
        case "$status" in
            exited|dead)
                docker logs "$container_name" >&2 || true
                return 1
                ;;
        esac
        sleep 1
    done
    docker logs "$container_name" >&2 || true
    printf 'interop fixture %s did not become ready\n' "$container_name" >&2
    return 1
}

docker network create "$dependency_network" >/dev/null
docker run --detach --name "$postgres_name" --network "$dependency_network" \
    --env POSTGRES_USER=knowledge_core \
    --env "POSTGRES_PASSWORD=${postgres_password}" \
    --env POSTGRES_DB=knowledge_core \
    --health-cmd 'pg_isready -U knowledge_core -d knowledge_core' \
    --health-interval 1s --health-timeout 3s --health-retries 60 \
    postgres:16-alpine >/dev/null
docker run --detach --name "$redis_name" --network "$dependency_network" \
    --health-cmd 'redis-cli ping' \
    --health-interval 1s --health-timeout 3s --health-retries 60 \
    redis:7-alpine redis-server --save '' >/dev/null
docker run --detach --name "$nats_name" --network "$dependency_network" \
    --health-cmd 'wget -qO- http://127.0.0.1:8222/healthz | grep -q ok' \
    --health-interval 1s --health-timeout 3s --health-retries 60 \
    nats:2.11-alpine -js -m 8222 >/dev/null
docker run --detach --name "$etcd_name" --network "$dependency_network" \
    quay.io/coreos/etcd:v3.6.0 \
    /usr/local/bin/etcd --name=knowledge-core-ci-etcd --data-dir=/tmp/etcd-data \
    --listen-client-urls=http://0.0.0.0:2379 --advertise-client-urls=http://"$etcd_name":2379 >/dev/null

wait_healthy "$postgres_name"
wait_healthy "$redis_name"
wait_healthy "$nats_name"
wait_etcd "$etcd_name"

rust_container_env=(
    --env HOME=/tmp/knowledge-core-rust-ci
    --env CARGO_HOME=/cache/cargo-home
    --env CARGO_TARGET_DIR=/cache/cargo-target
    --env COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1
    --env "COLLABORATION_TEST_POSTGRES_URL=postgres://knowledge_core:${postgres_password}@${postgres_name}:5432/knowledge_core"
    --env "COLLABORATION_TEST_REDIS_URL=redis://${redis_name}:6379/0"
    --env "COLLABORATION_TEST_NATS_URL=nats://${nats_name}:4222"
    --env "COLLABORATION_TEST_ETCD_ENDPOINTS=http://${etcd_name}:2379"
)
if [[ -n "$container_proxy" ]]; then
    rust_container_env+=(
        --env "http_proxy=${container_proxy}"
        --env "https_proxy=${container_proxy}"
        --env "HTTP_PROXY=${container_proxy}"
        --env "HTTPS_PROXY=${container_proxy}"
        --env "no_proxy=127.0.0.1,localhost,::1,${postgres_name},${redis_name},${nats_name},${etcd_name}"
        --env "NO_PROXY=127.0.0.1,localhost,::1,${postgres_name},${redis_name},${nats_name},${etcd_name}"
    )
fi

mkdir -p "$interop_root"

docker run --rm \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --pids-limit 2048 \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=1073741824 \
    --network "$dependency_network" \
    "${container_env[@]}" \
    --volume "${repository_root}:/workspace:ro" \
    --volume "${cache_root}/go-build:/cache/go-build:rw" \
    --volume "${cache_root}/go-mod:/cache/go-mod:rw" \
    --volume "${cache_root}/gopath:/cache/gopath:rw" \
    --volume "${interop_root}:/interop:rw" \
    --workdir /workspace \
    "$image" \
    bash -euo pipefail -c 'mkdir -p "$HOME"; go build -trimpath -o /interop/go-interop ./tools/interop'

docker run --rm \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --pids-limit 4096 \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=4294967296 \
    --network "$dependency_network" \
    "${rust_container_env[@]}" \
    --volume "${repository_root}:/workspace:ro" \
    --volume "${cache_root}/cargo-home:/cache/cargo-home:rw" \
    --volume "${cache_root}/cargo-target:/cache/cargo-target:rw" \
    --volume "${interop_root}:/interop:rw" \
    --workdir /workspace/services/collaboration \
    "$rust_image" \
    bash -euo pipefail -c 'mkdir -p "$HOME"; cargo build --locked --bin volo_interop_fixture; install -m 0755 "$CARGO_TARGET_DIR/debug/volo_interop_fixture" /interop/rust-interop'

docker run --rm \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --pids-limit 256 \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=67108864 \
    --network "$dependency_network" \
    --env KC_INTEROP_CERT_DIR=/interop/certs \
    --volume "${interop_root}:/interop:rw" \
    "$image" \
    /interop/go-interop certs

interop_metadata_env=(
    --env KC_INTEROP_EXPECT_TOKEN=interop-access-token
    --env KC_INTEROP_EXPECT_REQUEST_ID=interop-request-01
    --env KC_INTEROP_EXPECT_TRACE_ID=4bf92f3577b34da6a3ce929d0e0e4736
    --env KC_INTEROP_TRACE_PARENT=00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
    --env KC_INTEROP_TRACE_STATE=knowledge-core=interop
    --env KC_INTEROP_BAGGAGE=environment=ci
    --env KC_INTEROP_DEADLINE_MS=50
    --env KC_INTEROP_DELAY_MS=2000
)

docker run --detach --name "$rust_interop_name" --network "$dependency_network" --network-alias rust-server \
    --cap-drop ALL --security-opt no-new-privileges:true --pids-limit 512 --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=67108864 \
    "${interop_metadata_env[@]}" \
    --env KC_INTEROP_ADDRESS=0.0.0.0:18883 \
    --env KC_INTEROP_TLS_CA_FILE=/interop/certs/ca.pem \
    --env KC_INTEROP_TLS_CERT_FILE=/interop/certs/server.pem \
    --env KC_INTEROP_TLS_KEY_FILE=/interop/certs/server-key.pem \
    --volume "${interop_root}:/interop:ro" \
    "$rust_image" /interop/rust-interop server >/dev/null
wait_ready_log "$rust_interop_name"

docker run --rm --network "$dependency_network" \
    --cap-drop ALL --security-opt no-new-privileges:true --pids-limit 512 --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=67108864 \
    "${interop_metadata_env[@]}" \
    --env KC_INTEROP_ADDRESS=rust-server:18883 \
    --env KC_INTEROP_TLS_CA_FILE=/interop/certs/ca.pem \
    --env KC_INTEROP_TLS_CERT_FILE=/interop/certs/client.pem \
    --env KC_INTEROP_TLS_KEY_FILE=/interop/certs/client-key.pem \
    --env KC_INTEROP_TLS_SERVER_NAME=rust-server \
    --volume "${interop_root}:/interop:ro" \
    "$image" /interop/go-interop client
docker rm --force "$rust_interop_name" >/dev/null

docker run --detach --name "$go_interop_name" --network "$dependency_network" --network-alias go-server \
    --cap-drop ALL --security-opt no-new-privileges:true --pids-limit 512 --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=67108864 \
    "${interop_metadata_env[@]}" \
    --env KC_INTEROP_ADDRESS=0.0.0.0:18884 \
    --env KC_INTEROP_TLS_CA_FILE=/interop/certs/ca.pem \
    --env KC_INTEROP_TLS_CERT_FILE=/interop/certs/server.pem \
    --env KC_INTEROP_TLS_KEY_FILE=/interop/certs/server-key.pem \
    --volume "${interop_root}:/interop:ro" \
    "$image" /interop/go-interop server >/dev/null
wait_ready_log "$go_interop_name"

docker run --rm --network "$dependency_network" \
    --cap-drop ALL --security-opt no-new-privileges:true --pids-limit 512 --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=67108864 \
    "${interop_metadata_env[@]}" \
    --env KC_INTEROP_ADDRESS=go-server:18884 \
    --env KC_INTEROP_TLS_CA_FILE=/interop/certs/ca.pem \
    --env KC_INTEROP_TLS_CERT_FILE=/interop/certs/client.pem \
    --env KC_INTEROP_TLS_KEY_FILE=/interop/certs/client-key.pem \
    --env KC_INTEROP_TLS_SERVER_NAME=go-server \
    --volume "${interop_root}:/interop:ro" \
    "$rust_image" /interop/rust-interop client
docker rm --force "$go_interop_name" >/dev/null

docker run --rm \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --pids-limit 4096 \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=4294967296 \
    --network "$dependency_network" \
    "${rust_container_env[@]}" \
    --volume "${repository_root}:/workspace:rw" \
    --volume "${cache_root}/cargo-home:/cache/cargo-home:rw" \
    --volume "${cache_root}/cargo-target:/cache/cargo-target:rw" \
    --workdir /workspace \
    "$rust_image" \
    bash -euo pipefail -c 'mkdir -p "$HOME"; make rust-ci'

docker build \
    "${proxy_build_args[@]}" \
    --file "${repository_root}/docker/collaboration/dockerfile" \
    --label "knowledge-core.ci-temporary=true" \
    --tag "$collaboration_image" \
    "$repository_root"

if [[ "$(docker image inspect --format '{{.Config.User}}' "$collaboration_image")" != "10001:10001" ]]; then
    printf 'Collaboration production image must declare user 10001:10001\n' >&2
    exit 1
fi

docker run --rm \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --pids-limit 64 \
    --read-only \
    --network none \
    --entrypoint /bin/sh \
    "$collaboration_image" \
    -euc 'test "$(id -u):$(id -g)" = 10001:10001; test -x /usr/local/bin/knowledge-core-collaboration; ! command -v node; ! command -v npm'

if collaboration_smoke_output="$(docker run --rm \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --pids-limit 64 \
    --read-only \
    --network none \
    --env COLLABORATION_ENVIRONMENT=invalid \
    "$collaboration_image" 2>&1)"; then
    printf 'Collaboration production binary accepted an invalid environment\n' >&2
    exit 1
fi
if [[ "$collaboration_smoke_output" != *"collaboration.invalid_input: COLLABORATION_ENVIRONMENT must be development, production, or test"* ]]; then
    printf 'Collaboration production binary did not fail at configuration validation:\n%s\n' "$collaboration_smoke_output" >&2
    exit 1
fi
