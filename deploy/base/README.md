# Application deployment base

This directory is the environment-independent Kubernetes base for the four
Knowledge Core services. It deliberately contains no Namespace, Secret,
environment endpoint, public hostname, or deployable image digest.

CI copies the verified directory into `k3s-home-deploy/Knowledge-Core/base`.
The only supported environment is `deploy/overlay/dev`. Its GitOps copy owns
namespace selection, static environment overrides, the public trust bundle,
SOPS-encrypted Secrets, and immutable image digests.

Each Deployment references only its own
`knowledge-core-<service>-secrets`. That Secret also owns the service-specific
Nacos reader credentials and KEK; there is no shared application Nacos Secret.
`harbor-pull-secrets` remains a separate registry credential.

The NetworkPolicies reach each shared dependency in its platform namespace:
PostgreSQL, Redis, NATS, Nacos, Etcd, MinIO, and ClamAV. The dev namespace must
also carry the three infrastructure client labels declared by the overlay.
