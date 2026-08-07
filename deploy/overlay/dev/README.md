# Development overlay

This is the only supported application environment. It targets namespace
`knowledge-core-dev` and the cluster-wide PostgreSQL, Redis, NATS, Nacos,
Etcd, MinIO, and ClamAV services.

Before Argo CD sync, GitOps must provide:

- `harbor-pull-secrets` and immutable digests for all four application images.
- `knowledge-core-trust-bundle` with keys `internal-ca.crt` and `nats-ca.crt`.
- `knowledge-core-{gateway,identity,knowledge,collaboration}-secrets` with the
  service-specific credentials required by the base deployments and
  `docs/framework-design.md`.

No Secret value or environment-specific CA is stored in this source overlay.
