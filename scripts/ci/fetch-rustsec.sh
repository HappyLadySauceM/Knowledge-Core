#!/usr/bin/env bash

set -euo pipefail

readonly maximum_attempts=3
readonly repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

cd "$repository_root/services/collaboration"

for attempt in 1 2 3; do
    if timeout --foreground --kill-after=5s 120s cargo deny fetch db; then
        exit 0
    fi

    if ((attempt == maximum_attempts)); then
        printf 'Failed to fetch the RustSec advisory database after %d attempts\n' "$maximum_attempts" >&2
        exit 1
    fi

    printf 'Retrying the RustSec advisory database fetch (%d/%d)\n' \
        "$((attempt + 1))" "$maximum_attempts" >&2
    sleep "$((attempt * 5))"
done
