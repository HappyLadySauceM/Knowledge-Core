#!/usr/bin/env bash
set -euo pipefail

: "${GITOPS_REVISION:?GITOPS_REVISION is required}"

application="${ARGOCD_APPLICATION:-knowledge-core-dev}"
namespace="${APPLICATION_NAMESPACE:-knowledge-core-dev}"
image_map_file="${IMAGE_MAP_FILE:-}"
api="https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT_HTTPS}"
token_file=/var/run/secrets/kubernetes.io/serviceaccount/token
ca_file=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
token="$(<"$token_file")"

request() {
    curl --fail --silent --show-error \
        --cacert "$ca_file" \
        --header "Authorization: Bearer ${token}" \
        "$@"
}

request \
    --request PATCH \
    --header 'Content-Type: application/merge-patch+json' \
    --data '{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"hard"}}}' \
    "${api}/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/${application}" \
    >/dev/null

for _ in $(seq 1 120); do
    snapshot="$(request "${api}/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/${application}")"
    revision="$(jq -r '.status.sync.revision // ""' <<<"$snapshot")"
    sync_status="$(jq -r '.status.sync.status // ""' <<<"$snapshot")"
    health_status="$(jq -r '.status.health.status // ""' <<<"$snapshot")"
    if [[ "$revision" == "$GITOPS_REVISION" && "$sync_status" == Synced && "$health_status" == Healthy ]]; then
        if [[ -n "$image_map_file" ]]; then
            while IFS='=' read -r name expected; do
                service="${name#knowledge-core-}"
                deployment="knowledge-core-${service}"
                deployed="$({
                    request "${api}/apis/apps/v1/namespaces/${namespace}/deployments/${deployment}"
                } | jq -er --arg service "$service" '.spec.template.spec.containers[] | select(.name == $service) | .image')"
                if [[ "$deployed" != "$expected" ]]; then
                    printf 'deployment %s uses %s, expected %s\n' "$deployment" "$deployed" "$expected" >&2
                    exit 1
                fi
            done <"$image_map_file"
        fi
        printf 'Argo CD application %s is Synced/Healthy at %s\n' "$application" "$GITOPS_REVISION"
        exit 0
    fi
    sleep 5
done

printf 'Argo CD application %s did not reach Synced/Healthy at %s\n' \
    "$application" "$GITOPS_REVISION" >&2
exit 1
