#!/usr/bin/env bash
set -euo pipefail

namespace="${APPLICATION_NAMESPACE:-knowledge-core-dev}"

retry_fetch() {
    local output="$1"
    shift
    for _ in $(seq 1 30); do
        if "$@" >"$output" 2>/dev/null; then
            return 0
        fi
        sleep 2
    done
    printf 'smoke request failed\n' >&2
    return 1
}

response="$(mktemp)"
trap 'rm -f -- "$response"' EXIT

if [[ -n "${KUBECONFIG:-}" ]]; then
    if [[ ! -r "$KUBECONFIG" ]]; then
        printf 'KUBECONFIG is not readable\n' >&2
        exit 2
    fi
    proxy_get() {
        kubectl --kubeconfig "$KUBECONFIG" get --raw "$1"
    }
    prefix="/api/v1/namespaces/${namespace}/services"
    retry_fetch "$response" proxy_get "${prefix}/http:knowledge-core-gateway:8082/proxy/readyz"
    retry_fetch "$response" proxy_get "${prefix}/http:knowledge-core-identity:8081/proxy/readyz"
    retry_fetch "$response" proxy_get "${prefix}/http:knowledge-core-knowledge:8083/proxy/readyz"
    retry_fetch "$response" proxy_get "${prefix}/http:knowledge-core-collaboration:8084/proxy/health/ready"
    retry_fetch "$response" proxy_get "${prefix}/http:knowledge-core-gateway:8080/proxy/health/ready"
    retry_fetch "$response" proxy_get "${prefix}/http:knowledge-core-gateway:8080/proxy/api/v1/documents?limit=1"
else
    retry_curl() {
        curl --fail --silent --show-error --max-time 5 "$1"
    }
    retry_fetch "$response" retry_curl http://knowledge-core-gateway.knowledge-core-dev.svc.cluster.local:8082/readyz
    retry_fetch "$response" retry_curl http://knowledge-core-identity.knowledge-core-dev.svc.cluster.local:8081/readyz
    retry_fetch "$response" retry_curl http://knowledge-core-knowledge.knowledge-core-dev.svc.cluster.local:8083/readyz
    retry_fetch "$response" retry_curl http://knowledge-core-collaboration.knowledge-core-dev.svc.cluster.local:8084/health/ready
    retry_fetch "$response" retry_curl http://knowledge-core-gateway.knowledge-core-dev.svc.cluster.local:8080/health/ready
    retry_fetch "$response" retry_curl 'http://knowledge-core-gateway.knowledge-core-dev.svc.cluster.local:8080/api/v1/documents?limit=1'
fi

jq -e . "$response" >/dev/null

if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
    bash "$script_root/github-status.sh" \
        success 'Knowledge Core dev smoke passed' knowledge-core/smoke
    if [[ -d /workspace/release ]]; then
        touch /workspace/release/smoke-passed
    fi
fi
