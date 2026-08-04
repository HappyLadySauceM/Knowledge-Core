# Application deployment base

This directory is the environment-independent Kubernetes base for the four
Knowledge Core services. It deliberately contains no Namespace, Secret,
environment endpoint, public hostname, or deployable image digest.

CI copies the verified directory into `k3s-home-deploy/Knowledge-Core/base`.
The GitOps test/prod overlays own namespace selection, static environment
overrides, the public Nacos CA, SOPS-encrypted Secrets, and immutable image
digests.

Each Deployment references only its own
`knowledge-core-<service>-secrets`. That Secret also owns the service-specific
Nacos reader credentials and KEK; there is no shared application Nacos Secret.
`harbor-pull-secrets` remains a separate registry credential.

The NetworkPolicies allow PostgreSQL and Redis through their existing cluster
namespaces and allow NATS, application Etcd, MinIO, and ClamAV through
`knowledge-core-platform`. Nacos is reached only in `config-system`.
