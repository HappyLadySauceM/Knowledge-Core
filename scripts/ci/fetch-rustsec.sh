#!/usr/bin/env bash

set -euo pipefail

readonly maximum_attempts=3
readonly repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly cargo_home="$(realpath -m -- "${CARGO_HOME:?CARGO_HOME must be set}")"
readonly advisory_db_root="$cargo_home/advisory-dbs"

if [[ "$cargo_home" == / ]]; then
    printf 'CARGO_HOME must not resolve to the filesystem root\n' >&2
    exit 1
fi

reset_advisory_database() {
    if [[ ! -d "$advisory_db_root" ]]; then
        return
    fi

    local database_path
    while IFS= read -r -d '' database_path; do
        rm -rf -- "$database_path"
    done < <(
        find "$advisory_db_root" \
            -mindepth 1 \
            -maxdepth 1 \
            -type d \
            -name 'advisory-db-*' \
            -print0
    )
    rm -f -- "$advisory_db_root/db.lock"
}

cd "$repository_root/services/collaboration"

for attempt in 1 2 3; do
    if timeout --foreground --kill-after=5s 120s cargo deny fetch db; then
        exit 0
    fi

    # cargo-deny reuses a directory left by an interrupted clone, even when
    # that repository has no usable pack or HEAD. Force the retry to reclone.
    reset_advisory_database
    if ((attempt == maximum_attempts)); then
        printf 'Failed to fetch the RustSec advisory database after %d attempts\n' "$maximum_attempts" >&2
        exit 1
    fi

    printf 'Retrying the RustSec advisory database fetch (%d/%d)\n' \
        "$((attempt + 1))" "$maximum_attempts" >&2
    sleep "$((attempt * 5))"
done
