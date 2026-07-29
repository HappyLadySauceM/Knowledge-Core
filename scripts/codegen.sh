#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repository_root"

check=false
if [[ "${1:-}" == "--check" ]]; then
  check=true
  shift
fi
if (( $# != 0 )); then
  echo "usage: $0 [--check]" >&2
  exit 2
fi

snapshot() {
  find kitex_gen services/gateway/biz -type f -print0 \
    | sort -z \
    | while IFS= read -r -d '' file; do
        hash="$(sha256sum "$file" | awk '{print $1}')"
        printf '%s  %s\n' "$hash" "$file"
      done
}

if $check; then
  before="$(mktemp)"
  after="$(mktemp)"
  trap 'rm -f "$before" "$after"' EXIT
  snapshot >"$before"
fi

module="github.com/HappyLadySauce/Knowledge-Core"
kitex_version="v0.16.2"
hz_version="v0.9.7"
thriftgo_version="0.4.5"

actual_kitex_version="$(kitex --version 2>&1)"
if [[ "$actual_kitex_version" != "$kitex_version" ]]; then
  echo "kitex version $actual_kitex_version does not match required $kitex_version" >&2
  exit 1
fi

actual_hz_version="$(hz --version 2>&1 | grep -Eo 'v[0-9]+\.[0-9]+\.[0-9]+' | head -n 1)"
if [[ "$actual_hz_version" != "$hz_version" ]]; then
  echo "hz version $actual_hz_version does not match required $hz_version" >&2
  exit 1
fi

actual_thriftgo_version="$(thriftgo --version 2>&1 | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -n 1)"
if [[ "$actual_thriftgo_version" != "$thriftgo_version" ]]; then
  echo "thriftgo version $actual_thriftgo_version does not match required $thriftgo_version" >&2
  exit 1
fi

for idl in identity knowledge platform; do
  kitex -module "$module" -I idl/rpc/v1 "idl/rpc/v1/$idl.thrift"
done

hz_args=(
  --module "$module"
  --idl idl/http/v1/gateway.thrift
  --out_dir .
  --handler_dir services/gateway/biz/handler
  --model_dir services/gateway/biz/model
  --sort_router
)

if [[ -f services/gateway/biz/router/register.go ]]; then
  hz update "${hz_args[@]}"
else
  hz new "${hz_args[@]}" \
    --router_dir services/gateway/biz/router \
    --service gateway \
    --exclude_file main.go \
    --exclude_file go.mod \
    --exclude_file .gitignore \
    --exclude_file router_gen.go \
    --exclude_file router.go \
    --exclude_file build.sh \
    --exclude_file script/bootstrap.sh \
    --exclude_file services/gateway/biz/handler/ping.go
fi

gofmt -w kitex_gen services/gateway/biz

if $check; then
  snapshot >"$after"
  if ! cmp -s "$before" "$after"; then
    diff -u "$before" "$after" || true
    echo "generated code is not up to date" >&2
    exit 1
  fi
fi
