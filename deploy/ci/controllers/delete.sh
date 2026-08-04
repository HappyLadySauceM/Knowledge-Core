#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KUBECONFIG="${KUBECONFIG:-${DIR}/../kubeconfig.yml}"

echo "Deleting Knowledge Core CI controllers from ${DIR}"
kubectl delete -k "${DIR}" --ignore-not-found
