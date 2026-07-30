#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module="github.com/HappyLadySauce/Knowledge-Core"
kitex_version="v0.16.2"
hz_version="v0.9.7"
thriftgo_version="0.4.5"
owned_manifest="scripts/generated-files.txt"

check=false
if [[ "${1:-}" == "--check" ]]; then
  check=true
  shift
fi
if (( $# != 0 )); then
  echo "usage: $0 [--check]" >&2
  exit 2
fi

owned_files() {
  local root="$1"
  {
    [[ ! -d "$root/kitex_gen" ]] || find "$root/kitex_gen" -type f -print
    [[ ! -d "$root/services/gateway/biz/model" ]] || find "$root/services/gateway/biz/model" -type f -print
    for relative in .hz services/gateway/biz/router/gateway/gateway.go services/gateway/biz/router/register.go; do
      [[ ! -f "$root/$relative" ]] || printf '%s\n' "$root/$relative"
    done
  } | sed "s#^$root/##" | LC_ALL=C sort -u
}

validate_manifest() {
  local root="$1"
  local expected actual
  expected="$(mktemp)"
  actual="$(mktemp)"
  sed '/^[[:space:]]*$/d' "$root/$owned_manifest" | LC_ALL=C sort -u >"$expected"
  owned_files "$root" >"$actual"
  if ! cmp -s "$expected" "$actual"; then
    diff -u "$expected" "$actual" || true
    rm -f -- "$expected" "$actual"
    echo "generated output does not match $owned_manifest" >&2
    return 1
  fi
  rm -f -- "$expected" "$actual"
}

snapshot() {
  local root="$1"
  {
    owned_files "$root"
    [[ ! -d "$root/services/gateway/biz/handler" ]] || find "$root/services/gateway/biz/handler" -type f -print | sed "s#^$root/##"
    for relative in services/gateway/biz/router/gateway/middleware.go services/gateway/biz/router/router.go; do
      [[ ! -f "$root/$relative" ]] || printf '%s\n' "$relative"
    done
  } | LC_ALL=C sort -u | while IFS= read -r relative; do
    printf '%s  %s\n' "$(sha256sum "$root/$relative" | awk '{print $1}')" "$relative"
  done
}

generate() {
  local root="$1"
  cd "$root"

  local actual_kitex_version actual_hz_version actual_thriftgo_version
  actual_kitex_version="$(kitex --version 2>&1)"
  if [[ "$actual_kitex_version" != "$kitex_version" ]]; then
    echo "kitex version $actual_kitex_version does not match required $kitex_version" >&2
    return 1
  fi
  actual_hz_version="$(hz --version 2>&1 | grep -Eo 'v[0-9]+\.[0-9]+\.[0-9]+' | head -n 1)"
  if [[ "$actual_hz_version" != "$hz_version" ]]; then
    echo "hz version $actual_hz_version does not match required $hz_version" >&2
    return 1
  fi
  actual_thriftgo_version="$(thriftgo --version 2>&1 | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -n 1)"
  if [[ "$actual_thriftgo_version" != "$thriftgo_version" ]]; then
    echo "thriftgo version $actual_thriftgo_version does not match required $thriftgo_version" >&2
    return 1
  fi

  local rpc_idl_output
  if ! rpc_idl_output="$(go run ./scripts/idlguard services idl/rpc/v1)"; then
    echo "discover Kitex service IDLs failed" >&2
    return 1
  fi
  local -a rpc_idls=()
  while IFS= read -r idl; do
    [[ -z "$idl" ]] || rpc_idls+=("$idl")
  done <<<"$rpc_idl_output"
  if (( ${#rpc_idls[@]} == 0 )); then
    echo "no Kitex service IDLs found" >&2
    return 1
  fi
  for idl in "${rpc_idls[@]}"; do
    kitex -module "$module" -I idl/rpc/v1 "$idl"
  done

  local hz_args=(
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
}

if ! $check; then
  generate "$repository_root"
  validate_manifest "$repository_root"
  exit 0
fi

validate_manifest "$repository_root"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/knowledge-core-codegen.XXXXXX")"
cleanup() {
  local resolved
  resolved="$(cd "$(dirname "$temporary_root")" && pwd)/$(basename "$temporary_root")"
  if [[ -d "$resolved" && "$(basename "$resolved")" == knowledge-core-codegen.* ]]; then
    rm -rf -- "$resolved"
  fi
}
trap cleanup EXIT

shopt -s dotglob nullglob
for entry in "$repository_root"/*; do
  [[ "$(basename "$entry")" == ".git" ]] || cp -a "$entry" "$temporary_root/"
done
shopt -u dotglob nullglob

rm -rf -- "$temporary_root/kitex_gen" "$temporary_root/services/gateway/biz/model"
generate "$temporary_root"
validate_manifest "$temporary_root"

expected_snapshot="$(mktemp)"
actual_snapshot="$(mktemp)"
snapshot "$repository_root" >"$expected_snapshot"
snapshot "$temporary_root" >"$actual_snapshot"
if ! cmp -s "$expected_snapshot" "$actual_snapshot"; then
  diff -u "$expected_snapshot" "$actual_snapshot" || true
  rm -f -- "$expected_snapshot" "$actual_snapshot"
  echo "generated code is not up to date" >&2
  exit 1
fi
rm -f -- "$expected_snapshot" "$actual_snapshot"
