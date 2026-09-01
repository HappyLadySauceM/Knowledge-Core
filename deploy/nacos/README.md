# Nacos Application Configuration

These plaintext files are complete non-sensitive `v1beta1/ApplicationConfig` templates for the
development k3s environment and the existing `<service>.dynamic.yaml` Data IDs. They are
operational inputs, not Kubernetes resources. Service-to-service dependencies use the target
cluster's `*.svc.cluster.local` names; public HTTP/WebSocket URLs and CORS origins use the
external application domain. For another namespace or cluster, copy the files and replace only
the reviewed environment-specific Service FQDNs and public domain before publishing. Local
Compose does not enable Nacos and continues to use `services/*/etc/config.yaml` plus environment
overrides. Secret-backed environment variables remain the highest-priority source.

Validate and encrypt a revision before publishing:

```text
go run ./tools/configctl validate --service gateway --input deploy/nacos/gateway.dynamic.yaml
go run ./tools/configctl encrypt --service gateway --input deploy/nacos/gateway.dynamic.yaml --output /secure/gateway.envelope.json --namespace dev --data-id gateway.dynamic.yaml --key-id <key-id>
go run ./tools/configctl publish --service gateway --input /secure/gateway.envelope.json
```

The KEK and Nacos credentials must come from the operator environment or Secret manager. Publishing
uses a dedicated writer identity scoped to `dev/KNOWLEDGE_CORE/<service>.dynamic.yaml`; application
reader identities and the Nacos administrator are not publishing identities. Increase `revision`
for every content change. Before rolling back to a binary that only understands `v1alpha1`, publish
a compatible `v1alpha1/DynamicConfig` document with a higher revision.
