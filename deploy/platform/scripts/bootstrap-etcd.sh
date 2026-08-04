#!/bin/sh
set -eu

: "${ETCD_ROOT_PASSWORD:?ETCD_ROOT_PASSWORD is required}"
: "${KC_TEST_ETCD_PASSWORD:?KC_TEST_ETCD_PASSWORD is required}"
: "${KC_PROD_ETCD_PASSWORD:?KC_PROD_ETCD_PASSWORD is required}"

export ETCDCTL_API=3
export ETCDCTL_ENDPOINTS=http://knowledge-core-etcd.knowledge-core-platform.svc.cluster.local:2379
export ETCDCTL_USER="root:${ETCD_ROOT_PASSWORD}"

ensure_user() {
    user="$1"
    password="$2"
    if ! etcdctl user get "$user" >/dev/null 2>&1; then
        etcdctl user add "${user}:${password}" --interactive=false >/dev/null
    fi
}

ensure_role() {
    role="$1"
    prefix="$2"
    if ! etcdctl role get "$role" >/dev/null 2>&1; then
        etcdctl role add "$role" >/dev/null
    fi
    etcdctl role grant-permission "$role" readwrite "$prefix" --prefix=true >/dev/null
}

ensure_user knowledge-core-test "$KC_TEST_ETCD_PASSWORD"
ensure_user knowledge-core-prod "$KC_PROD_ETCD_PASSWORD"
ensure_role knowledge-core-test /knowledge-core/test/
ensure_role knowledge-core-prod /knowledge-core/prod/
etcdctl user grant-role knowledge-core-test knowledge-core-test >/dev/null
etcdctl user grant-role knowledge-core-prod knowledge-core-prod >/dev/null
