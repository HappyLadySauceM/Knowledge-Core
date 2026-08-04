#!/usr/bin/env bash
set -euo pipefail

readonly root=/workspace/interop
readonly certs="$root/certs"
readonly go_fixture="$root/go-interop"
readonly rust_fixture="$root/rust-interop"

for path in "$go_fixture" "$rust_fixture" "$certs/ca.pem"; do
    if [[ ! -r "$path" ]]; then
        printf 'interop input is missing: %s\n' "$path" >&2
        exit 2
    fi
done

export KC_INTEROP_EXPECT_TOKEN=interop-access-token
export KC_INTEROP_EXPECT_REQUEST_ID=interop-request-01
export KC_INTEROP_EXPECT_TRACE_ID=4bf92f3577b34da6a3ce929d0e0e4736
export KC_INTEROP_TRACE_PARENT=00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
export KC_INTEROP_TRACE_STATE=knowledge-core=interop
export KC_INTEROP_BAGGAGE=environment=ci
export KC_INTEROP_DEADLINE_MS=50
export KC_INTEROP_DELAY_MS=2000

server_pid=''
cleanup() {
    if [[ -n "$server_pid" ]]; then
        kill "$server_pid" >/dev/null 2>&1 || true
        wait "$server_pid" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

wait_ready() {
    log_file="$1"
    for _ in $(seq 1 100); do
        if grep -q '^READY ' "$log_file" 2>/dev/null; then
            return 0
        fi
        if ! kill -0 "$server_pid" 2>/dev/null; then
            cat "$log_file" >&2
            return 1
        fi
        sleep 0.1
    done
    cat "$log_file" >&2
    return 1
}

KC_INTEROP_ADDRESS=127.0.0.1:18883 \
KC_INTEROP_TLS_CA_FILE="$certs/ca.pem" \
KC_INTEROP_TLS_CERT_FILE="$certs/server.pem" \
KC_INTEROP_TLS_KEY_FILE="$certs/server-key.pem" \
    "$rust_fixture" server >"$root/rust-server.log" 2>&1 &
server_pid="$!"
wait_ready "$root/rust-server.log"
KC_INTEROP_ADDRESS=127.0.0.1:18883 \
KC_INTEROP_TLS_CA_FILE="$certs/ca.pem" \
KC_INTEROP_TLS_CERT_FILE="$certs/client.pem" \
KC_INTEROP_TLS_KEY_FILE="$certs/client-key.pem" \
KC_INTEROP_TLS_SERVER_NAME=rust-server \
    "$go_fixture" client
cleanup
server_pid=''

KC_INTEROP_ADDRESS=127.0.0.1:18884 \
KC_INTEROP_TLS_CA_FILE="$certs/ca.pem" \
KC_INTEROP_TLS_CERT_FILE="$certs/server.pem" \
KC_INTEROP_TLS_KEY_FILE="$certs/server-key.pem" \
    "$go_fixture" server >"$root/go-server.log" 2>&1 &
server_pid="$!"
wait_ready "$root/go-server.log"
KC_INTEROP_ADDRESS=127.0.0.1:18884 \
KC_INTEROP_TLS_CA_FILE="$certs/ca.pem" \
KC_INTEROP_TLS_CERT_FILE="$certs/client.pem" \
KC_INTEROP_TLS_KEY_FILE="$certs/client-key.pem" \
KC_INTEROP_TLS_SERVER_NAME=go-server \
    "$rust_fixture" client
