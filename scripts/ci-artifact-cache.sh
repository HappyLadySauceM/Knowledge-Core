#!/usr/bin/env bash
# Optional same-node transfer for CI artifacts. GitHub Artifacts remains the
# authoritative fallback when producer and consumer land on different runners.
set -euo pipefail

usage() { echo "usage: $0 publish|restore ARTIFACT_NAME PATH" >&2; exit 64; }
[[ $# == 3 ]] || usage
command="$1" name="$2" destination="$3"
[[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || { echo "unsafe artifact name: $name" >&2; exit 64; }
[[ -n "${GITHUB_RUN_ID:-}" && -n "${GITHUB_SHA:-}" ]] || { echo "GitHub run metadata is required" >&2; exit 64; }

root="${KC_CI_ARTIFACT_CACHE_ROOT:-/cache/knowledge-core/artifacts}"
entry="$root/${GITHUB_RUN_ID}/${GITHUB_RUN_ATTEMPT:-1}/${GITHUB_SHA}/$name"

publish() {
  [[ -e "$destination" ]] || { echo "artifact source is missing: $destination" >&2; exit 1; }
  tmp="${entry}.tmp.$$"
  rm -rf "$tmp"
  install -d -m 0755 "$tmp"
  tar -C "$(dirname "$destination")" -cf "$tmp/payload.tar" "$(basename "$destination")"
  sha256sum "$tmp/payload.tar" | awk '{print $1 "  payload.tar"}' > "$tmp/payload.sha256"
  printf 'run_id=%s\nrun_attempt=%s\nsha=%s\nname=%s\n' "$GITHUB_RUN_ID" "${GITHUB_RUN_ATTEMPT:-1}" "$GITHUB_SHA" "$name" > "$tmp/metadata"
  install -d -m 0755 "$(dirname "$entry")"
  rm -rf "$entry"
  mv "$tmp" "$entry"
  echo "local artifact cache published: $name"
}

restore() {
  [[ -f "$entry/metadata" && -f "$entry/payload.tar" && -f "$entry/payload.sha256" ]] || { echo "local artifact cache miss: $name"; exit 2; }
  expected="run_id=$GITHUB_RUN_ID
run_attempt=${GITHUB_RUN_ATTEMPT:-1}
sha=$GITHUB_SHA
name=$name"
  [[ "$(cat "$entry/metadata")" == "$expected" ]] || { echo "local artifact cache metadata mismatch: $name" >&2; exit 2; }
  (cd "$entry" && sha256sum -c payload.sha256 --status --strict) || { echo "local artifact cache checksum mismatch: $name" >&2; exit 2; }
  stage="$(mktemp -d "${RUNNER_TEMP:-/tmp}/kc-artifact.XXXXXX")"
  trap 'rm -rf "$stage"' EXIT
  tar -C "$stage" -xf "$entry/payload.tar"
  item="$stage/$(basename "$destination")"
  [[ -e "$item" && ! -e "$destination" ]] || { echo "local artifact cache payload is unsafe or destination exists: $name" >&2; exit 2; }
  install -d -m 0755 "$(dirname "$destination")"
  mv "$item" "$destination"
  echo "local artifact cache hit: $name"
}

case "$command" in
  publish) publish ;;
  restore) restore ;;
  *) usage ;;
esac
