#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KUBECONFIG="${KUBECONFIG:-${DIR}/../kubeconfig.yml}"

echo "Applying Knowledge Core CI controllers from ${DIR}"
kubectl kustomize "${DIR}" | kubectl apply --server-side --force-conflicts -f -

kubectl rollout status deployment/workflow-controller -n ci --timeout=300s
kubectl rollout status deployment/argo-server -n ci --timeout=300s
kubectl rollout status deployment/controller-manager -n ci --timeout=300s
echo "Done."
