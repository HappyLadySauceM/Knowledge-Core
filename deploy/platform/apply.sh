#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KUBECONFIG="${KUBECONFIG:-${DIR}/../kubeconfig.yml}"

kubectl apply -f "${DIR}/namespaces.yaml"

if ! kubectl get clusterissuer happyladysauce-ca >/dev/null 2>&1; then
  printf '%s\n' 'required ClusterIssuer is missing: happyladysauce-ca' >&2
  exit 2
fi

required_secret_specs=(
  "postgresql/knowledge-core-postgres-roles test-identity-password test-knowledge-password test-collaboration-password prod-identity-password prod-knowledge-password prod-collaboration-password nacos-password"
  "redis/knowledge-core-redis-users test-password prod-password"
  "knowledge-core-platform/knowledge-core-nats-config nats.conf"
  "knowledge-core-platform/knowledge-core-etcd-credentials root-password test-password prod-password"
  "knowledge-core-platform/knowledge-core-minio-credentials root-user root-password test-access-key test-secret-key prod-access-key prod-secret-key"
  "config-system/knowledge-core-nacos-credentials postgres-password identity-key identity-value token-secret admin-password test-gateway-password test-identity-password test-knowledge-password test-collaboration-password prod-gateway-password prod-identity-password prod-knowledge-password prod-collaboration-password"
)
for spec in "${required_secret_specs[@]}"; do
  reference="${spec%% *}"
  keys="${spec#* }"
  namespace="${reference%%/*}"
  name="${reference#*/}"
  if ! kubectl get secret "$name" -n "$namespace" >/dev/null 2>&1; then
    printf 'required Secret is missing: %s\n' "$reference" >&2
    exit 2
  fi
  for key in $keys; do
    template="{{if index .data \"${key}\"}}present{{end}}"
    if [ "$(kubectl get secret "$name" -n "$namespace" -o go-template="$template")" != "present" ]; then
      printf 'required Secret key is missing or empty: %s/%s\n' "$reference" "$key" >&2
      exit 2
    fi
  done
done

echo "Applying Knowledge Core platform from ${DIR}"
for job in \
  postgresql/knowledge-core-postgres-bootstrap \
  redis/knowledge-core-redis-bootstrap \
  config-system/knowledge-core-nacos-auth-bootstrap \
  knowledge-core-platform/knowledge-core-etcd-bootstrap \
  knowledge-core-platform/knowledge-core-minio-bootstrap; do
  namespace="${job%%/*}"
  name="${job#*/}"
  kubectl delete job "$name" -n "$namespace" --ignore-not-found --wait=true
done
kubectl kustomize "${DIR}" | kubectl apply --server-side --force-conflicts -f -

kubectl rollout status statefulset/knowledge-core-nats -n knowledge-core-platform --timeout=300s
kubectl rollout status statefulset/knowledge-core-etcd -n knowledge-core-platform --timeout=300s
kubectl rollout status statefulset/knowledge-core-minio -n knowledge-core-platform --timeout=300s
kubectl rollout status statefulset/knowledge-core-clamav -n knowledge-core-platform --timeout=900s
kubectl wait --for=condition=complete job/knowledge-core-postgres-bootstrap -n postgresql --timeout=300s
kubectl wait --for=condition=complete job/knowledge-core-redis-bootstrap -n redis --timeout=180s
kubectl wait --for=condition=complete job/knowledge-core-nacos-auth-bootstrap -n config-system --timeout=240s
kubectl wait --for=condition=complete job/knowledge-core-etcd-bootstrap -n knowledge-core-platform --timeout=180s
kubectl wait --for=condition=complete job/knowledge-core-minio-bootstrap -n knowledge-core-platform --timeout=300s
echo "Done."
