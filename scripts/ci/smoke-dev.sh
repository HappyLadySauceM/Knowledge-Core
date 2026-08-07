#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"

retry_get() {
    local url="$1"
    local output="${2:-}"
    for _ in $(seq 1 30); do
        if [[ -n "$output" ]]; then
            if curl --fail --silent --show-error --max-time 5 "$url" >"$output"; then
                return 0
            fi
        elif curl --fail --silent --show-error --max-time 5 "$url" >/dev/null; then
            return 0
        fi
        sleep 2
    done
    printf 'smoke request failed: %s\n' "$url" >&2
    return 1
}

retry_get http://knowledge-core-gateway.knowledge-core-dev.svc.cluster.local:8082/readyz
retry_get http://knowledge-core-identity.knowledge-core-dev.svc.cluster.local:8081/readyz
retry_get http://knowledge-core-knowledge.knowledge-core-dev.svc.cluster.local:8083/readyz
retry_get http://knowledge-core-collaboration.knowledge-core-dev.svc.cluster.local:8084/health/ready
retry_get http://knowledge-core-gateway.knowledge-core-dev.svc.cluster.local:8080/health/ready

response="$(mktemp)"
trap 'rm -f -- "$response"' EXIT
retry_get 'http://knowledge-core-gateway.knowledge-core-dev.svc.cluster.local:8080/api/v1/documents?limit=1' "$response"
jq -e . "$response" >/dev/null

bash /workspace/source/scripts/ci/github-status.sh \
    success 'Knowledge Core dev smoke passed' knowledge-core/smoke
touch /workspace/release/smoke-passed
