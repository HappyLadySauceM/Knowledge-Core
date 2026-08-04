# Knowledge Core k3s platform

This directory is mirrored to `/opt/k3s/knowledge-core-platform`. The remote
directory is the deployment entrypoint for shared Knowledge Core infrastructure.
It reuses the existing PostgreSQL and Redis instances and adds only NATS,
application Etcd, MinIO, ClamAV, and Nacos.

`apply.sh` refuses to deploy until these Secrets exist:

| Namespace | Secret | Required keys |
| --- | --- | --- |
| `postgresql` | `knowledge-core-postgres-roles` | `test-identity-password`, `test-knowledge-password`, `test-collaboration-password`, `prod-identity-password`, `prod-knowledge-password`, `prod-collaboration-password`, `nacos-password` |
| `redis` | `knowledge-core-redis-users` | `test-password`, `prod-password` |
| `knowledge-core-platform` | `knowledge-core-nats-config` | `nats.conf` |
| `knowledge-core-platform` | `knowledge-core-etcd-credentials` | `root-password`, `test-password`, `prod-password` |
| `knowledge-core-platform` | `knowledge-core-minio-credentials` | `root-user`, `root-password`, `test-access-key`, `test-secret-key`, `prod-access-key`, `prod-secret-key` |
| `config-system` | `knowledge-core-nacos-credentials` | `postgres-password`, `identity-key`, `identity-value`, `token-secret`, `admin-password`, and `test-`/`prod-` passwords for `gateway`, `identity`, `knowledge`, and `collaboration` |

The Nacos PostgreSQL password must match in the `postgresql` and
`config-system` Secrets. Secret manifests are intentionally absent. They must be
created through the SOPS bootstrap flow before running `apply.sh`.

Nacos uses the existing `happyladysauce-ca` ClusterIssuer for HTTPS and gRPC
TLS. The bootstrap Job stores only bcrypt password hashes in the dedicated
`nacos` PostgreSQL database, creates the `test` and `prod` namespaces, and
grants each service account read access only to its own dynamic data ID.
Environment overlays must mount the public CA and set
`KNOWLEDGE_CORE_NACOS_CA_FILE`; Collaboration must also set the same path in
`NACOS_CLIENT_TLS_CA_CERT` because that is the native Rust SDK trust hook.

`knowledge-core-nats-config` must define three NATS accounts: test, prod, and
Argo Events. Application accounts should only export/import their own
environment subjects. Argo Events uses a separate account and JetStream domain.

The Redis StatefulSet integration under `redis/` changes the existing server to
an ACL file stored on its current PVC. It preserves the existing default-user
password as a SHA-256 ACL hash, then the bootstrap Job adds scoped test/prod
users and persists them with `ACL SAVE`. Application configuration must use
`knowledge-core:test:*` and `knowledge-core:prod:*` key prefixes respectively;
logical Redis databases are additional separation and are not an ACL boundary.
