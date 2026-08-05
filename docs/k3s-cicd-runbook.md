# Knowledge Core k3s CI/CD 配置交接清单

本文是给集群配置 Agent 的执行清单。状态基线为 2026-08-04，目标集群是
`100.100.100.2` 上的单节点 k3s。本文不包含任何真实密码、私钥、token、kubeconfig
或证书私钥；所有 `<...>` 都必须由 Secret manager 或离线生成流程替换，不能直接提交。

## 1. 结论与状态边界

必须区分以下三种状态：

- **当前运行**：GitHub Actions self-hosted runner 仍是已经验证过的 CI；`dev` 验证成功后自动创建
  `dev -> main` PR，以 merge commit 合并，再安全地把 `dev` fast-forward 到 `main`。
- **已声明但未激活**：仓库已有 Argo Workflows/Events、BuildKit、Trivy、Syft、Cosign 和 GitOps
  推广清单，但集群里只有 controller，尚无 WorkflowTemplate、EventBus、EventSource 或 Sensor。
- **必须实施的目标**：k3s 原生 CI 接管 `dev` 和 SemVer tag；Rust 只在固定的 Rust CI 容器内编译一次，
  production image 只复制该二进制；Argo CD 只消费 GitOps 仓库并负责 CD。

当前不能宣称 GitOps 或 production 已启用。以下阻塞项全部处理前，不得创建 prod Application：

- `/opt/k3s/kustomization.yaml` 不存在，共享基础设施还没有根清单。
- `knowledge-core-platform`、`config-system`、`knowledge-core-test`、`knowledge-core-prod` namespace 不存在。
- GitOps 远端仓库 `HappyLadySauceM/k3s-home-deploy` 仍为空，本地骨架尚未提交。
- test/prod overlay 仍是全零 image digest。
- CI 的 `ci-control`、`ci-go`、`ci-rust` 参数仍是全零 digest。
- k3s 节点没有 `/etc/rancher/k3s/registries.yaml`，尚未证明 containerd 信任 Harbor CA。
- SOPS age key、离线备份、加密 Secret 和 Argo CD KSOPS/CMP 解密能力均不存在。
- source CI webhook、Argo CD webhook 和公网可信 TLS 路由均未配置。
- 当前 `deploy/ci/events.yaml` 的 EventBus 地址错误，必须从
  `nats.knowledge-core-platform.svc.cluster.local` 改为
  `knowledge-core-nats.knowledge-core-platform.svc.cluster.local`。
- 当前 Argo Events exotic JetStream 认证契约没有经过 controller 实测，详见第 7 节。
- production 所需 PostgreSQL、Redis、NATS、Etcd 和 RPC TLS/mTLS 尚未闭环。
- 单节点 `local-path` 没有 HA，且 StorageClass 未启用在线扩容；上线前必须完成备份恢复演练。

Identity 与 Knowledge 的真实 PostgreSQL repository 测试仍是已知缺口，本轮不补，也不能把现有单元测试
描述为真实数据库验证。

## 2. 当前集群清单

| 项目 | 当前值 |
| --- | --- |
| 节点 | `home`，单节点 |
| k3s | `v1.36.2+k3s1` |
| 运行时 | `containerd://2.3.2-k3s2`，不是 Docker |
| 默认 StorageClass | `local-path`，`allowVolumeExpansion` 未开启 |
| 已有 namespace | `argocd`、`cert-manager`、`ci`、`harbor`、`higress-system`、`istio-system`、`monitoring`、`postgresql`、`redis` |
| ClusterIssuer | `happyladysauce-ca` Ready，只适合内部信任链 |
| IngressClass | `higress` |
| CI controller | Argo Workflows `v4.0.8` 与 Argo Events `v1.9.11` controller 正常运行 |
| Argo CD Application | 0 个 |

`happyladysauce-ca` 不能用于 GitHub Cloud webhook。`hooks.happyladysauce.cn` 和
`argocd-hooks.happyladysauce.cn` 必须使用公网 CA 信任的证书。

## 3. 所有权和目录

| 所有者 | 权威位置 | 内容 |
| --- | --- | --- |
| k3s 主机 | `/opt/k3s/<component>` | 共享基础设施 |
| 应用仓库 | `deploy/platform` | `/opt/k3s/knowledge-core-platform` 的受版本控制镜像 |
| 应用仓库 | `deploy/ci/controllers` | `/opt/k3s/ci` 的 controller 镜像 |
| 应用仓库 | `deploy/ci` | Knowledge Core 的 Workflow/Event/RBAC/NetworkPolicy 真源 |
| 应用仓库 | `deploy/base` | 四个服务的环境无关 workload 真源 |
| GitOps 仓库 | `Knowledge-Core/base` | CI 从 `deploy/base` 复制的已验证快照 |
| GitOps 仓库 | `Knowledge-Core/overlay/test`、`prod` | namespace、环境配置、公共 CA、加密 Secret 和不可变 image digest |

应用仓库不能保存真实 Secret；GitOps 仓库只能保存 SOPS 密文；age 私钥只能离线备份并以集群 Secret
注入 Argo CD。Argo CD Application 继续由用户在 UI 中创建，不由本仓库自动创建。

### 3.1 `/opt/k3s/kustomization.yaml`

配置 Agent 必须先创建根清单并把所有共享基础设施纳入资源图：

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - certs
  - postgresql
  - redis
  - harbor
  - higress
  - istio
  - monitoring
  - argocd
  - ci
  - knowledge-core-platform
```

根清单包含多个 `helmCharts` 子清单，渲染时必须显式传 `--enable-helm`。它用于完整性检查和漂移审查，
不替代组件自己的 `apply.sh`。尤其
`/opt/k3s/knowledge-core-platform/apply.sh` 带有 Secret 前置检查、bootstrap Job 删除重建和有界等待，
不能直接用一次无等待的 `kubectl apply -k /opt/k3s` 绕过。

## 4. 基础设施配置

### 4.1 复用服务

| 服务 | namespace | endpoint | 配置要求 |
| --- | --- | --- | --- |
| PostgreSQL | `postgresql` | `postgresql.postgresql.svc.cluster.local:5432` | 复用现有实例，不创建第二套数据库 |
| Redis | `redis` | `redis-master.redis.svc.cluster.local:6379` | 复用现有实例，使用 ACL 用户隔离环境 |
| Harbor | `harbor` | `harbor.happyladysauce.local` | `knowledge-core` project、robot account、CA 和 retention |
| Argo CD | `argocd` | UI 为 `argocd.happyladysauce.local` | UI 保持内网，仅额外公开 webhook path |
| cert-manager | `cert-manager` | `happyladysauce-ca` | 只给内部 Nacos/TLS 证书使用 |
| Higress | `higress-system` | ingress class `higress` | 承载两个公网 webhook host |

### 4.2 Knowledge Core 新增服务

| 服务 | namespace | endpoint | PVC | 当前拓扑 |
| --- | --- | --- | --- | --- |
| NATS JetStream | `knowledge-core-platform` | `knowledge-core-nats:4222`，monitor `8222` | 4 GiB | 1 replica |
| Etcd | `knowledge-core-platform` | `knowledge-core-etcd:2379` | 2 GiB | 1 replica |
| MinIO | `knowledge-core-platform` | API `9000`，console `9001` | 16 GiB | 1 replica |
| ClamAV | `knowledge-core-platform` | `3310` | 2 GiB | 1 replica |
| Nacos 3.2.3 | `config-system` | HTTPS `8848`，TLS gRPC `9848/9849` | 2 GiB | 1 replica |

所有完整 FQDN 使用 `<service>.<namespace>.svc.cluster.local`。上述容量只是当前单节点起始值，不是生产容量
承诺。

### 4.3 PostgreSQL

`deploy/platform/scripts/bootstrap-postgres.sh` 是数据库契约真源：

| 环境 | database | login role | schema |
| --- | --- | --- | --- |
| test | `knowledge_core_test` | `knowledge_core_test_identity` | `identity` |
| test | `knowledge_core_test` | `knowledge_core_test_knowledge` | `knowledge` |
| test | `knowledge_core_test` | `knowledge_core_test_collaboration` | `collaboration` |
| prod | `knowledge_core_prod` | `knowledge_core_prod_identity` | `identity` |
| prod | `knowledge_core_prod` | `knowledge_core_prod_knowledge` | `knowledge` |
| prod | `knowledge_core_prod` | `knowledge_core_prod_collaboration` | `collaboration` |
| shared | `nacos` | `nacos` | Nacos `public` schema |

每个业务 role 只拥有自己的 schema。Nacos 使用 vendored Nacos 3.2.3 PostgreSQL schema，并启用
`pgcrypto` 存储 bcrypt 密码。`postgresql/knowledge-core-postgres-roles` 和
`config-system/knowledge-core-nacos-credentials` 中的 `nacos-password`/`postgres-password` 必须一致。

### 4.4 Redis

必须保留现有 StatefulSet/PVC，并用 `deploy/platform/redis` patch 启用 ACL file：

| ACL user | key pattern | Pub/Sub channel pattern | 禁止项 |
| --- | --- | --- | --- |
| `knowledge-core-test` | `knowledge-core:test:*` | `knowledge-core.test.*` | `@admin`、`FLUSHALL`、`FLUSHDB` |
| `knowledge-core-prod` | `knowledge-core:prod:*` | `knowledge-core.prod.*` | `@admin`、`FLUSHALL`、`FLUSHDB` |

Redis logical DB 不是安全边界。Argo CD 当前 `/opt/k3s/argocd/values.yaml` 还保存了明文 shared Redis
password，这是必须修复的配置债务：创建只存在于 `argocd` namespace 的 Secret，至少包含
`redis-password`；若使用专用 ACL 用户，再增加 `redis-username`，并把 values 改为：

```yaml
externalRedis:
  host: redis-master.redis.svc.cluster.local
  port: 6379
  existingSecret: argocd-redis
```

随后删除 values 中的 `password` 字段并重新部署 Argo CD。不要在命令行参数、shell history 或文档中出现
该密码。

### 4.5 NATS JetStream

`knowledge-core-nats-config/nats.conf` 必须开启 JetStream，并至少定义三个相互隔离的 account：test、prod、
Argo Events。应用当前按环境共用用户，因此它提供的是环境隔离，不是 Knowledge/Collaboration 服务级隔离。

下面是结构模板，不得把占位符提交；建议在 NATS 配置中保存 bcrypt hash，明文只进入对应 Kubernetes
Secret：

```conf
port: 4222
http: 8222

jetstream {
  store_dir: /data/jetstream
  max_file_store: 3GB
}

accounts {
  KC_TEST {
    jetstream: enabled
    users: [
      { user: knowledge-core-test, password: "<test-password-or-bcrypt>" }
    ]
  }
  KC_PROD {
    jetstream: enabled
    users: [
      { user: knowledge-core-prod, password: "<prod-password-or-bcrypt>" }
    ]
  }
  KC_CI {
    jetstream: enabled
    users: [
      { user: knowledge-core-argo-events, password: "<argo-events-password-or-bcrypt>" }
    ]
  }
}
```

业务流固定为 `KNOWLEDGE_CORE_EVENTS` 和 `KNOWLEDGE_CORE_PERMISSIONS`；subject 固定为
`collaboration.documents.updated`、`collaboration.documents.invalidated` 和
`knowledge.permissions.changed`。应用启动时会校验 stream retention、storage、duplicate window 和
message size，不要在平台 bootstrap 中创建语义不同的同名 stream。

### 4.6 Etcd

当前 bootstrap 启用认证并创建：

| user/role | 允许前缀 |
| --- | --- |
| `knowledge-core-test` | `/knowledge-core/test/` |
| `knowledge-core-prod` | `/knowledge-core/prod/` |

当前 listener 是认证后的明文 HTTP。test 可以用于集成验证；prod 不允许激活。prod 前必须由
cert-manager 签发 server 证书，Etcd 改为 HTTPS listener，所有 client 挂载 CA，并更新 endpoint 与 TLS
配置。Collaboration 至少需要设置 `COLLABORATION_ETCD_TLS_ENABLED=true`、
`COLLABORATION_ETCD_TLS_CA_FILE` 和正确的 `COLLABORATION_ETCD_TLS_SERVER_NAME`。

### 4.7 MinIO 与 ClamAV

MinIO bootstrap 创建 `knowledge-core-test`、`knowledge-core-prod` 两个 bucket，以及各自用户和 policy。
每个 policy 只允许本 bucket 的：

- `s3:GetBucketLocation`
- `s3:ListBucket`
- `s3:GetObject`
- `s3:PutObject`
- `s3:DeleteObject`

Knowledge 必须保持 `KNOWLEDGE_OBJECT_STORAGE_AUTO_CREATE_BUCKET=false`。ClamAV 仅通过 ClusterIP `3310`
提供扫描，不对公网暴露；其病毒库 PVC 也必须进入备份和更新监控范围。

### 4.8 Nacos

Nacos 使用已有 PostgreSQL 的 `nacos` database。内部证书由 `happyladysauce-ca` 签发，HTTP 与 gRPC
都启用 TLS；auth 开启，anonymous AI 关闭，UI 关闭。创建两个 namespace：`test`、`prod`。

每个环境创建四个只读身份：

```text
knowledge-core-<environment>-gateway
knowledge-core-<environment>-identity
knowledge-core-<environment>-knowledge
knowledge-core-<environment>-collaboration
```

每个身份只允许读取：

```text
<environment>:KNOWLEDGE_CORE:<service>.dynamic.yaml
```

动态文档只实现日志级别更新。每个服务使用自己的 Nacos reader、key ID 和 32-byte AES-256 KEK；密文
AAD 绑定 namespace/group/data ID。用构建后的 `configctl` 镜像完成 validate、encrypt 和 publish，不能把
明文动态 YAML 或 KEK 提交到任一仓库。

## 5. Secret 和配置对象契约

### 5.1 平台 Secret

运行 `/opt/k3s/knowledge-core-platform/apply.sh` 前，以下对象和每个 key 都必须存在且非空：

| namespace/Secret | required keys |
| --- | --- |
| `postgresql/knowledge-core-postgres-roles` | `test-identity-password`, `test-knowledge-password`, `test-collaboration-password`, `prod-identity-password`, `prod-knowledge-password`, `prod-collaboration-password`, `nacos-password` |
| `redis/knowledge-core-redis-users` | `test-password`, `prod-password` |
| `knowledge-core-platform/knowledge-core-nats-config` | `nats.conf` |
| `knowledge-core-platform/knowledge-core-etcd-credentials` | `root-password`, `test-password`, `prod-password` |
| `knowledge-core-platform/knowledge-core-minio-credentials` | `root-user`, `root-password`, `test-access-key`, `test-secret-key`, `prod-access-key`, `prod-secret-key` |
| `config-system/knowledge-core-nacos-credentials` | `postgres-password`, `identity-key`, `identity-value`, `token-secret`, `admin-password`, `test-gateway-password`, `test-identity-password`, `test-knowledge-password`, `test-collaboration-password`, `prod-gateway-password`, `prod-identity-password`, `prod-knowledge-password`, `prod-collaboration-password` |

### 5.2 CI Secret 与 ConfigMap

| object | keys/用途 |
| --- | --- |
| Secret `knowledge-core-github-app` | `app-id`, `installation-id`, `private-key` |
| Secret `knowledge-core-github-webhook` | `secret`，source repo webhook HMAC |
| Secret `knowledge-core-harbor-push` | `.dockerconfigjson`，仅 BuildKit/scan/sign 推送所需权限 |
| Secret `knowledge-core-harbor-pull` | `.dockerconfigjson`，只读 robot account；绑定 Workflow ServiceAccount |
| Secret `knowledge-core-cosign` | `cosign.key`, `password` |
| Secret `knowledge-core-ci-test-dependencies` | `postgres-password` |
| Secret `knowledge-core-argo-events-nats` | 建议改为 `auth.yaml`，内容见第 7 节 |
| ConfigMap `knowledge-core-harbor-ca` | `ca.crt` |

GitHub App 应安装到 source 和 GitOps 两个私有仓库。最低能力为 metadata read、source contents read、
commit status write、pull request write，以及 GitOps contents write。不要给 administration 权限。

### 5.3 应用 Secret

每个服务 Secret 都必须包含：

```text
KNOWLEDGE_CORE_NACOS_USERNAME
KNOWLEDGE_CORE_NACOS_PASSWORD
KNOWLEDGE_CORE_NACOS_KEY_ID
KNOWLEDGE_CORE_NACOS_KEK
```

附加 key：

| Secret | additional keys |
| --- | --- |
| `knowledge-core-gateway-secrets` | `GATEWAY_AUTH_PUBLIC_KEY`, `GATEWAY_REDIS_PASSWORD`, `GATEWAY_ETCD_PASSWORD` |
| `knowledge-core-identity-secrets` | `IDENTITY_AUTH_PRIVATE_KEY`, `IDENTITY_AUTH_PUBLIC_KEY`, `IDENTITY_POSTGRES_PASSWORD`, `IDENTITY_REDIS_PASSWORD`, `IDENTITY_ETCD_PASSWORD` |
| `knowledge-core-knowledge-secrets` | `KNOWLEDGE_AUTH_PUBLIC_KEY`, `KNOWLEDGE_POSTGRES_PASSWORD`, `KNOWLEDGE_ETCD_PASSWORD`, `KNOWLEDGE_NATS_PASSWORD`, `KNOWLEDGE_OBJECT_STORAGE_ACCESS_KEY`, `KNOWLEDGE_OBJECT_STORAGE_SECRET_KEY` |
| `knowledge-core-collaboration-secrets` | `COLLABORATION_POSTGRES_URL`, `COLLABORATION_REDIS_URL`, `COLLABORATION_NATS_PASSWORD`, `COLLABORATION_ETCD_PASSWORD` |
| `harbor-pull-secrets` | `.dockerconfigjson`，type 为 `kubernetes.io/dockerconfigjson` |

这些对象分别只存在于 `knowledge-core-test` 或 `knowledge-core-prod`。先离线备份 age 私钥，再创建
SOPS 密文；禁止生成未加密的中间 Secret 文件并随后“补加密”。

## 6. 当前 CI 和 47 分钟问题

GitHub run `30896355894` 中，`Run containerized CI` 从 09:27:04 到 10:03:39，耗时 36 分 35 秒；
verify job 约 36 分 51 秒，连同 promotion 约 37 分 12 秒。用户观察到的完整等待约 47 分钟。GitHub
当前把全部检查包在一个 step 中，无法从 Actions UI 精确拆分每个内部阶段耗时。

`.github/ci/run.sh` 当前流程为：

1. 校验 rootless Docker。
2. 按 Dockerfile hash 复用或构建 Go/Rust CI 镜像。
3. Node/Yjs fixture。
4. Go CI、race、生成漂移和供应链检查。
5. 启动一次性 PostgreSQL、Redis、NATS、Etcd。
6. 构建 Go/Rust interoperability fixture 并执行双向 mTLS/Yjs 互操作。
7. 在 Rust CI 容器中执行 `make rust-ci`，其中已经包含 release build。
8. 构建 Collaboration production image 并 smoke。
9. 清理依赖容器和临时镜像。

主要浪费是 Rust release 编译了两次：第一次在 Rust CI 容器执行 `make rust-ci`；第二次在
`docker/collaboration/dockerfile` 的 `rust:1.97.1` build stage 再执行 `cargo build --release`。
k3s WorkflowTemplate 也有同样结构：Rust pod 编译一次，BuildKit 构建 production Dockerfile 又编译一次。
第二个 build context 不能直接复用第一个 `CARGO_TARGET_DIR`，因此不是一个有效缓存设计。

## 7. k3s 原生 CI 流程

### 7.1 事件链路

```text
push dev / push vX.Y.Z
  -> GitHub source webhook
  -> Higress: hooks.happyladysauce.cn/events/knowledge-core
  -> Argo Events EventSource (HMAC)
  -> external NATS JetStream EventBus
  -> Sensor repo/ref filter
  -> Argo Workflow
  -> Harbor immutable images + signatures + SBOM
  -> GitOps main digest commit
  -> Argo CD webhook refresh
  -> test auto sync / prod manual sync
```

Sensor 路由规则：

- `refs/heads/dev` -> `test`
- `refs/tags/vX.Y.Z` -> `prod`，且 tag commit 必须可从 `main` 到达
- 其他 branch、deleted ref、其他 repository 一律拒绝

Workflow 使用固定名 `knowledge-core-ci-<commit SHA>`，重复 delivery 不应创建第二个 workflow。

### 7.2 EventBus 启用前修正

必须修改 `deploy/ci/events.yaml`：

1. URL 改为 `nats://knowledge-core-nats.knowledge-core-platform.svc.cluster.local:4222`。
2. 推荐把 `accessSecret.key` 从含义错误的 `password` 改为 `auth.yaml`。
3. `ci/knowledge-core-argo-events-nats` 使用以下结构，用户名/密码与 `nats.conf` 的 `KC_CI` account 一致：

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: knowledge-core-argo-events-nats
  namespace: ci
type: Opaque
stringData:
  auth.yaml: |
    username: knowledge-core-argo-events
    password: <same-plaintext-password-used-to-authenticate-to-NATS>
```

Argo Events `v1.9.11` 的 CRD 对该字段使用 preserve-unknown-fields，`kubectl explain` 不会展开
`jetstreamExotic`，这不代表字段无效。但该版本 controller 会把 `accessSecret` 对应内容挂载成
`auth.yaml` 并按 basic credential 反序列化，因此不能只依赖 CRD dry-run；必须以 EventBus Ready、
EventSource/Sensor deployment Ready 和真实 delivery 为验收证据。

### 7.3 Workflow DAG

当前清单的 DAG 是：pending status -> checkout -> Node -> Go -> Rust/真实依赖 -> interoperability ->
ref CAS 校验 -> BuildKit -> digest collection -> Trivy -> Syft -> Cosign sign -> Cosign attest -> GitOps
promotion -> test 的 `dev -> main` PR -> final status。

保留以下 fail-closed 语义：

- 全 workflow mutex `knowledge-core-heavy` 防止当前单节点被并发重载。
- 每个 workflow 使用 12 GiB workspace PVC。
- 共享 CI cache 16 GiB，BuildKit cache 16 GiB。
- ResourceQuota 为 requests 6 CPU/10 GiB、limits 12 CPU/20 GiB。
- source ref 在构建 image 前重新查询 GitHub；ref 已推进则退出，不推广旧 SHA。
- Trivy HIGH/CRITICAL、SBOM、签名、attestation 任一步失败都不更新 GitOps。
- GitOps push 使用最多三次 compare-and-swap，不覆盖并发提交。
- test 才创建 `dev -> main` PR；prod 只接受 SemVer tag。

### 7.4 Rust 单次编译目标

必须实施以下改造，不能继续让 production Dockerfile 调用 Cargo：

1. 构建并推送不可变 `harbor.happyladysauce.local/knowledge-core/ci-rust:<toolchain-hash>`。
2. CI 镜像包含 Rust `1.97.1`、rustfmt、Clippy、cargo-deny、protoc、pkg-config、CA roots 和
   `sccache`；仅当 RustDockerfile、toolchain 或工具版本变化时重建。
3. WorkflowTemplate 只引用 `ci-rust@sha256:<digest>`，禁止 mutable tag 和全零 digest。
4. Rust pod 中执行全部检查和唯一一次 `make rust-ci` release compilation。
5. 共享 PVC 只保存 `CARGO_HOME` registry/git cache 和 `SCCACHE_DIR`。`CARGO_TARGET_DIR` 使用
   workflow/SHA scoped 目录，不能让未来并行 workflow 并发写同一个 target。
6. 从 `$CARGO_TARGET_DIR/release/knowledge-core-collaboration` 复制到
   `/workspace/release/collaboration/knowledge-core-collaboration`，校验文件存在、可执行且不是 symlink。
7. Collaboration runtime Dockerfile 只使用固定 digest 的 Debian runtime、安装 CA、创建
   `10001:10001`、复制预编译 binary；文件内不能出现 `FROM rust`、`cargo` 或 source tree COPY。
8. `scripts/ci/build-images.sh` 为 Collaboration 使用 `/workspace` 作为 BuildKit context，只读取
   `release/collaboration`；其他 Go image 仍使用 `/workspace/source`。
9. 在收集 digest 后 smoke **精确的 digest artifact**，再做 Trivy、Syft、Cosign。

目标 runtime Dockerfile 的职责应接近：

```dockerfile
FROM debian:bookworm-slim@sha256:<verified-runtime-digest>

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 knowledge-core \
    && useradd --uid 10001 --gid knowledge-core --no-create-home \
         --shell /usr/sbin/nologin knowledge-core

COPY --chown=10001:10001 release/collaboration/knowledge-core-collaboration \
    /usr/local/bin/knowledge-core-collaboration

USER 10001:10001
ENTRYPOINT ["/usr/local/bin/knowledge-core-collaboration"]
```

验收标准：

- workflow 内只有一次 project `cargo build --release`。
- 构建不读取宿主机 Cargo、target 或本机编译产物。
- warm-cache dev CI 连续 5 次的目标 p95 小于 15 分钟；cold-cache 单独记录，不与 warm SLO 混淆。
- Argo 每个 task 都有独立耗时；Rust check/build、image assembly、scan/sign 不再隐藏在一个大 step。
- 保留 60 分钟 hard deadline，先测量后再收紧；超过 15 分钟发出性能告警而不是静默接受。

Node、Go、Rust 当前串行。Rust 单次编译稳定后，可以把 Node/Go/Rust 改成 checkout 后并行，但必须先让
source mount 只读或使用独立 worktree，证明 generate/check 不会并发修改同一 checkout。

### 7.5 CI toolchain image bootstrap

CI image 来源：

| image | Dockerfile |
| --- | --- |
| `ci-control` | `docker/ci-control/dockerfile` |
| `ci-go` | `.github/ci/Dockerfile`，这是远端 runner 镜像定义，不要为本机环境修改 |
| `ci-rust` | `.github/ci/RustDockerfile` |

第一次存在启动依赖：k3s checkout 需要 `ci-control`，而 `ci-control` 尚未在 Harbor。只允许使用现有
self-hosted runner 的 rootless Docker **一次性构建并推送 `ci-control`**；这是容器镜像构建，不允许在
宿主机编译任何项目代码。得到首个 `ci-control@sha256:<digest>` 后，创建手工 toolchain Workflow：用
`ci-control` checkout 精确 source SHA，再由 k3s rootless BuildKit 构建并推送全部三个 CI image。

后续只使用 k3s toolchain Workflow，按 Dockerfile/tool version hash 打 tag，记录 Harbor 返回 digest，再
更新 WorkflowTemplate。toolchain image 构建不能编译 Knowledge Core project；project 编译只发生在
Workflow 的语言检查 pod 中。该 Workflow 只能手工触发或由上述三个 Dockerfile/toolchain 变更触发，不能
在每次业务 source push 时重建基础镜像。

## 8. Harbor 与 containerd

Harbor project `knowledge-core` 至少包含 CI toolchain image、五个产物 image 和签名/attestation。
创建独立 push robot 与 pull robot；Workflow BuildKit 使用 push credential，kubelet/Application 只使用
pull credential。

k3s 节点必须配置 containerd registry trust。将 Harbor 公共 CA 保存到权限受控的宿主机文件，然后创建：

```yaml
# /etc/rancher/k3s/registries.yaml
mirrors:
  "harbor.happyladysauce.local":
    endpoint:
      - "https://harbor.happyladysauce.local"
configs:
  "harbor.happyladysauce.local":
    tls:
      ca_file: /etc/rancher/k3s/harbor-ca.crt
```

重启 k3s 会中断单节点工作负载，必须安排窗口。重启后用一个引用 Harbor digest 的一次性 pod 验证 pull，
不能用 `insecure_skip_verify` 代替 CA。`knowledge-core-workflow` ServiceAccount 还必须引用
`knowledge-core-harbor-pull`，否则 private `ci-*` image 在 pod 创建前就会 ImagePullBackOff。

Harbor retention 不得删除 GitOps 当前或可回滚 commit 引用的 digest，也不得清理对应 Cosign signature 和
attestation。

## 9. Source CI webhook

GitHub repository：`HappyLadySauceM/Knowledge-Core`。

| GitHub 设置 | 值 |
| --- | --- |
| Payload URL | `https://hooks.happyladysauce.cn/events/knowledge-core` |
| Content type | `application/json` |
| Secret | 与 `ci/knowledge-core-github-webhook:secret` 相同 |
| Events | Just the push event |
| SSL verification | Enable |

`active: false` 表示 Argo Events 不通过 GitHub API 管理 webhook，必须在 GitHub UI 手工创建。配置 Agent
还必须：

1. 配置公网 DNS，不得指向 `.local` 或只在 Tailscale 可达的地址。
2. 为 `hooks.happyladysauce.cn` 配置公网可信证书；当前 Ingress 清单没有 `spec.tls`，必须补齐或证明
   Higress Gateway 已绑定等价证书。
3. Higress 只把 Exact path `/events/knowledge-core` 转到
   `knowledge-core-eventsource-svc.ci.svc.cluster.local:12000`。
4. 保留 NetworkPolicy：只有 `higress-system` namespace 能访问 EventSource `12000`。
5. 分别验证错误 HMAC、错误 repository、非 dev branch、非 SemVer tag、deleted ref 和重复 delivery。
6. 重复 delivery 必须只有一个 `knowledge-core-ci-<SHA>`；不能再次推广同一 SHA。

## 10. GitOps、SOPS 与 Argo CD

### 10.1 初始化 GitOps 仓库

先完成 age key 离线备份、`.sops.yaml` recipient、加密 Secret 和 KSOPS 配置，再把本地
`Knowledge-Core/` 骨架提交到 `HappyLadySauceM/k3s-home-deploy` 的 `main`。不得先推含明文 Secret 的
临时 commit。

CI 只允许：

- 用已验证 source SHA 的 `deploy/base` 替换 `Knowledge-Core/base`。
- 更新选中 overlay 的四个应用 image digest。
- 忽略 `configctl`，它不是常驻 Deployment。
- 直接 push GitOps `main` 时使用 CAS 重试，不 force push。

### 10.2 Argo CD 的 SOPS 解密能力

当前 Argo CD 没有 KSOPS/SOPS Config Management Plugin。仅把 SOPS 文件加入 Kustomization 不会自动
解密，这是创建 Application 前的硬阻塞。

配置 Agent 必须选择并固定一个受审计 digest 的 KSOPS CMP sidecar，并完成：

1. 在 `argocd` namespace 创建独立 `argocd-sops-age` Secret，key 为 `keys.txt`；私钥不进入
   `/opt/k3s` 或 GitOps repo。
2. 把 key 只读挂载到 CMP sidecar 的 SOPS age 路径。
3. CMP 使用 `kustomize build --enable-alpha-plugins --enable-exec`，KSOPS binary 和 plugin 路径固定。
4. 每个 overlay 使用 KSOPS generator 引用 SOPS 密文 Secret，不把密文文件直接当普通 Secret apply。
5. Application source 显式选择该 CMP plugin。
6. 限制 plugin timeout、只读 root filesystem、non-root、drop capabilities；不要给 repo-server 不必要的
   Kubernetes API 权限。
7. 先在 test 验证生成的 Secret object 名称和 key 集合，再允许 automated sync。验证过程不得输出 value。

age 私钥丢失意味着无法恢复 GitOps Secret；私钥泄露意味着历史密文全部需要轮换。因此必须同时验证
离线恢复和轮换流程。

### 10.3 Argo CD repository 与 Application

给 Argo CD 添加 GitOps 仓库的只读 GitHub App 或 read-only deploy key，不需要写权限。建议创建
`knowledge-core` AppProject，将 source 限制为该 GitOps repo，将 destination 限制为
`knowledge-core-test` 和 `knowledge-core-prod`。

用户在 UI 中创建 test Application 时使用：

| 字段 | 值 |
| --- | --- |
| Name | `knowledge-core-test` |
| Project | `knowledge-core` |
| Repository | `HappyLadySauceM/k3s-home-deploy` |
| Revision | `main` |
| Path | `Knowledge-Core/overlay/test` |
| Destination | in-cluster / `knowledge-core-test` |
| Sync | automated、prune、self-heal |
| Source plugin | 已配置的 KSOPS CMP |

prod 使用 `Knowledge-Core/overlay/prod` 和 `knowledge-core-prod`，初始必须关闭 automated sync。只有第 13
节所有 production gate 通过后，才允许人工 sync；是否以后开启自动 sync 另行批准。

## 11. Argo CD webhook

GitHub Cloud 无法访问 `https://argocd.happyladysauce.local`，也不能信任内部 CA。不要公开完整 Argo CD
UI。创建独立公网 host，只开放一个 path：

```text
https://argocd-hooks.happyladysauce.cn/api/webhook
  -> Higress Exact path /api/webhook
  -> argocd-server.argocd.svc.cluster.local:80
```

公网 host 不能配置 `/` catch-all，必须使用公网可信 TLS，并通过 NetworkPolicy 限制 backend ingress 来源
为 `higress-system`。UI host 和 webhook host 使用不同路由策略。

在现有 `argocd-secret` 增加 `webhook.github.secret`，值通过 Secret manager 注入，不得放入
`/opt/k3s/argocd/values.yaml`。当前该 key 不存在。

在 GitOps repository `HappyLadySauceM/k3s-home-deploy` 创建 GitHub webhook：

| GitHub 设置 | 值 |
| --- | --- |
| Payload URL | `https://argocd-hooks.happyladysauce.cn/api/webhook` |
| Content type | `application/json` |
| Secret | 与 `argocd-secret:webhook.github.secret` 相同 |
| Events | Just the push event |
| SSL verification | Enable |

该 webhook 只负责让 Argo CD 立即 refresh，不负责 sync policy。当前无 webhook 时按 120 秒 reconciliation
interval 加最多 60 秒 jitter 轮询，但这不是 webhook 验收的替代。GitOps push 后应在秒级看到 refresh；
GitHub delivery 必须返回 2xx。

## 12. 切换顺序

严格按以下顺序执行，每阶段验收后再继续：

1. 创建 `/opt/k3s/kustomization.yaml`，只做 render/diff，确认没有资源冲突。
2. 修复 Argo CD Redis 明文 password；配置 Harbor CA、containerd trust、robot accounts。
3. 生成 age key、离线备份；配置 Argo CD KSOPS CMP，但暂不创建 Application。
4. 创建平台 Secret，执行 `/opt/k3s/knowledge-core-platform/apply.sh`，验收全部 Job/rollout。
5. 修正 EventBus service URL 和 structured auth；在 NATS 中验收独立 `KC_CI` account。
6. 用 k3s BuildKit 构建 `ci-control`、`ci-go`、`ci-rust`，记录不可变 digest。
7. 完成 Rust 单次编译/runtime-only image 改造并更新 WorkflowTemplate digest。
8. 创建 CI Secret/ConfigMap，应用 `deploy/ci` 的 application-level 资源。
9. 配置 source webhook 公网 DNS/TLS/Higress/GitHub，完成一次手工 redelivery 的 test workflow。
10. 初始化并 push GitOps `main`，配置 Argo CD read-only repo credential 和 GitOps webhook。
11. 用户从 UI 创建 test Application，确认 KSOPS、pull、sync、readiness 和回滚。
12. 连续运行至少 5 次 warm-cache dev CI，验收 p95 和单次 Rust 编译。
13. k3s CI 成为 authoritative gate 后，从 `.github/workflows/dev-to-main.yml` 移除 `dev` push 自动触发，
    保留 `workflow_dispatch` 回退和必要的 `main` 后 dev fast-forward。禁止两套 heavy CI 同时响应 dev push。
14. 完成 TLS/mTLS、备份恢复、容量和故障演练后，才创建或 sync prod Application。

切换期间旧 GitHub Actions 是 authoritative gate；k3s workflow 只能手工或 shadow 验证，不能同时写 GitOps
和自动合并 PR。切换完成后旧入口只能手工触发，避免重复构建和竞态推广。

第 8 步从 Knowledge Core 的干净 `dev` checkout 执行，入口是 `kubectl apply -k deploy/ci`，不是
`/opt/k3s/ci/apply.sh`；后者只管理 Argo Workflows/Events controller。应用前必须先对修正后的
`deploy/ci` 做 client dry-run 和 server dry-run，并确认 Secret/CI image digest 已就绪。

GitHub `main` ruleset 在切换时必须要求 PR、禁止 force push/直接绕过，并把 commit status
`knowledge-core/ci` 设为 required；只允许 merge commit。保留 GitHub auto-merge。`dev` 禁止 force push；
SemVer production tag 只能指向 `main` 可达的 commit。旧 Actions 的 `main` push 同步 job 继续负责在安全
时把 `dev` fast-forward 到 merge commit。

## 13. 验收命令和门禁

以下命令只读取对象名称、状态或 key 名，不输出 Secret value：

```bash
# 根清单和组件清单必须可渲染
kubectl kustomize /opt/k3s --enable-helm >/dev/null
kubectl kustomize /opt/k3s/knowledge-core-platform >/dev/null
kubectl kustomize /opt/k3s/ci >/dev/null
kubectl kustomize deploy/ci >/dev/null
kubectl apply --dry-run=client -k deploy/ci >/dev/null
kubectl apply --dry-run=server -k deploy/ci

# 集群基础状态
kubectl get node -o wide
kubectl get storageclass
kubectl get clusterissuer happyladysauce-ca

# 平台 rollout / bootstrap
kubectl rollout status statefulset/knowledge-core-nats -n knowledge-core-platform --timeout=300s
kubectl rollout status statefulset/knowledge-core-etcd -n knowledge-core-platform --timeout=300s
kubectl rollout status statefulset/knowledge-core-minio -n knowledge-core-platform --timeout=300s
kubectl rollout status statefulset/knowledge-core-clamav -n knowledge-core-platform --timeout=900s
kubectl rollout status deployment/knowledge-core-nacos -n config-system --timeout=600s
kubectl get jobs -n postgresql
kubectl get jobs -n redis
kubectl get jobs -n knowledge-core-platform
kubectl get jobs -n config-system

# CI control plane 和 application resources
kubectl get pods -n ci
kubectl get workflowtemplate,eventbus,eventsource,sensor -n ci
kubectl get eventbus default -n ci -o jsonpath='{.status.conditions}'
kubectl get eventsource knowledge-core -n ci -o jsonpath='{.status.conditions}'
kubectl get sensor knowledge-core -n ci -o jsonpath='{.status.conditions}'
kubectl get workflows -n ci --sort-by=.metadata.creationTimestamp

# 只列 Secret key 名
kubectl get secret knowledge-core-github-app -n ci \
  -o go-template='{{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}'
kubectl get secret argocd-secret -n argocd \
  -o go-template='{{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}'

# Argo CD
kubectl get applications.argoproj.io -n argocd
argocd app get knowledge-core-test --refresh
argocd app history knowledge-core-test
```

端到端验收还必须人工核对：

- GitHub source webhook delivery 是 2xx，错误 HMAC 被拒绝。
- commit status context 为 `knowledge-core/ci`，先 pending，最终 success/failure。
- Harbor 中五个 image 都以 digest 存在；Trivy、SBOM、Cosign signature、attestation 对应同一 digest。
- GitOps commit 只包含 base 快照和目标 overlay digest，不含 Secret 明文或意外环境修改。
- GitOps webhook delivery 是 2xx，Argo CD 秒级 refresh；test 自动 sync、prune、self-heal。
- Collaboration image config 的 UID/GID 是 `10001:10001`，镜像中无 Rust toolchain、Cargo、Node/npm。
- workflow 日志中没有第二次 project `cargo build --release`。

prod 的额外 gate：

- PostgreSQL verified TLS。
- Redis 使用 `rediss` 和 verified CA。
- NATS TLS，应用 stream contract 校验通过。
- Etcd HTTPS、认证和 verified CA。
- Gateway/Knowledge 到 Collaboration、Collaboration 到 Knowledge 的双向 mTLS。
- 外部 WebSocket 为 `wss`，Origin 为非空精确 allowlist。
- PostgreSQL、Redis、NATS、Etcd、MinIO 和 SOPS age key 的备份恢复演练通过。
- 单节点故障、dependency stop/start、滚动升级、回滚和容量测试完成。

## 14. 回滚规则

- CI 任一步失败：不更新 GitOps，不创建/推进 promotion PR；修复后用同一或新 SHA 重跑。
- test 应用回滚：在 GitOps repo revert 到上一组已签名 digest，由 Argo CD sync；禁止直接改 Deployment image。
- prod 回滚：先确认 schema/message contract 可回退，再人工选择已签名 digest commit 并 sync。
- webhook 故障：Argo CD 可退回轮询；source CI webhook 故障时使用手工 Workflow 或旧
  `workflow_dispatch`，不能关闭签名、scan 或 ref CAS 门禁。
- cache 损坏：只删除明确命名的 CI cache PVC 并执行 cold build；不得删除应用数据 PVC。
- 平台数据恢复：使用已演练的 PostgreSQL/Redis/NATS/Etcd/MinIO 备份，不以重建 StatefulSet 代替恢复。

完成本清单只代表 CI/CD 和集群配置闭环。单节点 `local-path` 仍不具备生产 HA，prod 是否上线必须单独
通过可用性、容量、证书轮换、备份恢复和故障演练评审。
