#!/usr/bin/env bash
set -euo pipefail

version="${1:?golangci-lint version is required}"
output_directory="${2:?output directory is required}"
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

case "$output_directory" in
  /*) ;;
  *) output_directory="$repository_root/$output_directory" ;;
esac

mkdir -p "$output_directory"
GOBIN="$output_directory" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$version"
