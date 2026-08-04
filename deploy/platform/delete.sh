#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KUBECONFIG="${KUBECONFIG:-${DIR}/../kubeconfig.yml}"

echo "Refusing broad platform deletion. Remove individual workloads explicitly after backup verification." >&2
exit 2
