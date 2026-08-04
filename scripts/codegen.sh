#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module="github.com/HappyLadySauce/Knowledge-Core"
kitex_version="v0.16.2"
hz_version="v0.9.7"
thriftgo_version="0.4.5"
rust_version="1.97.1"
owned_manifest="scripts/generated-files.txt"

check=false
include_hertz=false
generate_go=true
generate_rust_output=true
while (( $# > 0 )); do
  case "$1" in
    --check) check=true ;;
    --hertz) include_hertz=true ;;
    --go-only) generate_rust_output=false ;;
    --rust-only) generate_go=false ;;
    *)
      echo "usage: $0 [--check] [--hertz] [--go-only|--rust-only]" >&2
      exit 2
      ;;
  esac
  shift
done

if ! $generate_go && ! $generate_rust_output; then
  echo "--go-only and --rust-only cannot be combined" >&2
  exit 2
fi

tool_version() {
  local command="$1" pattern="$2" output
  output="$($command --version 2>&1)"
  if [[ "$output" =~ $pattern ]]; then
    printf '%s\n' "${BASH_REMATCH[0]}"
    return
  fi
  echo "could not parse $command version from: $output" >&2
  return 1
}

assert_tool_version() {
  local command="$1" pattern="$2" expected="$3" actual
  actual="$(tool_version "$command" "$pattern")"
  if [[ "$actual" != "$expected" ]]; then
    echo "$command version $actual does not match required $expected" >&2
    return 1
  fi
}

owned_files() {
  local root="$1"
  {
    if [[ -d "$root/kitex_gen" ]]; then
      (cd "$root" && find kitex_gen -type f -print)
    fi
    for relative in \
      services/gateway/biz/model/gateway/gateway.go \
      services/gateway/biz/router/gateway/gateway.go \
      services/gateway/biz/router/register.go \
      services/collaboration/src/generated/mod.rs \
      services/collaboration/src/generated/volo_gen.rs; do
      [[ ! -f "$root/$relative" ]] || printf '%s\n' "$relative"
    done
  } | LC_ALL=C sort -u
}

generate_rust() {
  local root="$1" actual cargo_target rust_workspace
  rust_workspace="$root/services/collaboration"
  cd "$rust_workspace"
  actual="$(rustc --version)"
  if [[ "$actual" != "rustc $rust_version "* ]]; then
    echo "rustc version does not match required $rust_version: $actual" >&2
    return 1
  fi
  cargo_target="${CARGO_TARGET_DIR:-$repository_root/services/collaboration/target/codegen}"
  CARGO_TARGET_DIR="$cargo_target" cargo run --locked -p knowledge-core-rust-codegen -- --root "$root"
  rustfmt --edition 2024 src/generated/mod.rs src/generated/volo_gen.rs
}

validate_manifest() {
  local root="$1" expected actual
  expected="$(mktemp)"
  actual="$(mktemp)"
  sed -e '/^[[:space:]]*$/d' -e '/^[[:space:]]*#/d' "$root/$owned_manifest" |
    LC_ALL=C sort -u >"$expected"
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
  local root="$1" relative
  while IFS= read -r relative; do
    [[ -z "$relative" ]] || printf '%s  %s\n' "$(sha256sum "$root/$relative" | awk '{print $1}')" "$relative"
  done < <(owned_files "$root")
}

clear_rpc_output() {
  local root="$1" target="$1/kitex_gen"
  if [[ "$target" != "$root/kitex_gen" || "$root" != /* ]]; then
    echo "refusing to remove unexpected generated path: $target" >&2
    return 1
  fi
  [[ ! -e "$target" ]] || rm -rf -- "$target"
}

generate_rpc() {
  local root="$1"
  assert_tool_version kitex 'v[0-9]+\.[0-9]+\.[0-9]+' "$kitex_version"
  assert_tool_version thriftgo '[0-9]+\.[0-9]+\.[0-9]+' "$thriftgo_version"
  clear_rpc_output "$root"

  cd "$root"
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
    echo "no service-bearing RPC IDLs found" >&2
    return 1
  fi
  for idl in "${rpc_idls[@]}"; do
    kitex -module "$module" -I idl/rpc/v1 "$idl"
  done
  gofmt -w kitex_gen
}

generate_hertz() {
  local root="$1"
  if [[ ! -f "$root/services/gateway/biz/router/register.go" ]]; then
    echo "Hertz scaffold is not present. Refusing 'hz new' because it would own service source; land the Gateway transport first, then rerun with --hertz." >&2
    return 1
  fi
  assert_tool_version hz 'v[0-9]+\.[0-9]+\.[0-9]+' "$hz_version"
  assert_tool_version thriftgo '[0-9]+\.[0-9]+\.[0-9]+' "$thriftgo_version"

  cd "$root"
  hz update \
    --module "$module" \
    --idl idl/http/v1/gateway.thrift \
    --out_dir . \
    --handler_dir services/gateway/biz/handler \
    --model_dir services/gateway/biz/model \
    --sort_router
  gofmt -w services/gateway/biz
}

if ! $check; then
  if $generate_go; then
    generate_rpc "$repository_root"
    generate_hertz "$repository_root"
  fi
  if $generate_rust_output; then
    generate_rust "$repository_root"
  fi
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

for file in go.mod go.sum; do
  [[ ! -f "$repository_root/$file" ]] || cp -a "$repository_root/$file" "$temporary_root/$file"
done
cp -a "$repository_root/idl" "$temporary_root/idl"
cp -a "$repository_root/scripts" "$temporary_root/scripts"
[[ ! -d "$repository_root/kitex_gen" ]] || cp -a "$repository_root/kitex_gen" "$temporary_root/kitex_gen"
mkdir -p "$temporary_root/services/collaboration"
for file in Cargo.toml Cargo.lock rust-toolchain.toml; do
  cp -a "$repository_root/services/collaboration/$file" "$temporary_root/services/collaboration/$file"
done
cp -a "$repository_root/services/collaboration/tools" "$temporary_root/services/collaboration/tools"
cp -a "$repository_root/services/collaboration/src" "$temporary_root/services/collaboration/src"
mkdir -p "$temporary_root/services/gateway"
cp -a "$repository_root/services/gateway/biz" "$temporary_root/services/gateway/biz"
[[ ! -f "$repository_root/.hz" ]] || cp -a "$repository_root/.hz" "$temporary_root/.hz"

if $generate_go; then
  generate_rpc "$temporary_root"
  generate_hertz "$temporary_root"
fi
if $generate_rust_output; then
  generate_rust "$temporary_root"
fi
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
