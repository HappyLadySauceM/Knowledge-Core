# Upstream controller manifests

These generated manifests are vendored so `/opt/k3s/ci` remains deployable when
GitHub is unavailable. Update the version, URL, digest pin patches, and checksum
together.

| Component | Source | SHA-256 |
| --- | --- | --- |
| Argo Workflows `v4.0.8` | `https://github.com/argoproj/argo-workflows/releases/download/v4.0.8/namespace-install.yaml` | `fddba9dfa09357da9c8ef15bc1536fd96f91d53613282b1bda2aca3bcb9273a4` |
| Argo Events `v1.9.11` | `https://raw.githubusercontent.com/argoproj/argo-events/v1.9.11/manifests/namespace-install.yaml` | `939803fb5e8ad679ffe723841494e86a74b3989290c3a5dd27211eeec502a42d` |
