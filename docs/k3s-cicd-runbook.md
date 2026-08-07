# Knowledge Core k3s CI/CD 与环境配置清单

本文是 Knowledge Core 在家庭 k3s 集群中的部署与交付契约。集群是单节点
`home`，使用 containerd 和 `local-path`；它适合开发环境，不具备生产 HA。
应用只保留一个 `dev` 环境，不再创建 test/prod overlay。

## 1. 所有权和目录

| 所有者 | 路径 | 职责 |
| --- | --- | --- |
| `k3s-home-deploy` | `k3s/`，服务器 `/opt/k3s` | 集群公共基础设施、Higress 路由、Argo 控制器 |
| Knowledge-Core | `deploy/ci` | 项目 EventSource、Sensor、WorkflowTemplate、CI RBAC/缓存 |
| Knowledge-Core | `deploy/base` | 四个服务的环境无关 Kubernetes 工作负载真源 |
| Knowledge-Core | `deploy/overlay/dev` | 可审查的 dev 配置模板，不含 Secret 和镜像 digest |
| `k3s-home-deploy` | `Knowledge-Core/base` | CI 同步的 base 快照 |
| `k3s-home-deploy` | `Knowledge-Core/overlay/dev` | Argo CD 消费的唯一环境、SOPS Secret 和不可变镜像 digest |

公共基础设施不得复制回项目仓库，项目 CI 也不得放进 `/opt/k3s/ci`。
`.github/ci/Dockerfile` 是 self-hosted runner 的 Go 工具镜像，不能当作 k3s
节点的宿主环境配置。

## 2. 当前拓扑

公共服务由 `/opt/k3s/kustomization.yaml` 聚合，项目应用由 Argo CD 独立同步。

| 服务 | namespace | 集群内地址 | 持久化/隔离 |
| --- | --- | --- | --- |
| PostgreSQL | `postgresql` | `postgresql.postgresql.svc.cluster.local:5432` | 复用现有实例，项目独立 database/role |
| Redis | `redis` | `redis-master.redis.svc.cluster.local:6379` | 复用现有实例，项目独立 ACL/前缀 |
| Nacos 3.2.3 | `nacos` | HTTPS `nacos.nacos.svc.cluster.local:8848` | PostgreSQL backend，namespace `dev` |
| NATS JetStream | `nats` | TLS `nats://nats.nats.svc.cluster.local:4222` | `KC_ADMIN`、`KC_DEV`、`KC_CI` account |
| Etcd | `etcd` | `https://etcd.etcd.svc.cluster.local:2379` | 4 GiB retained PVC，TLS + auth + prefix role |
| MinIO | `minio` | `http://minio.minio.svc.cluster.local:9000` | 50 GiB retained PVC，独立 bucket/policy/user |
| ClamAV | `clamav` | `clamav.clamav.svc.cluster.local:3310` | 4 GiB retained signature PVC，无项目凭据 |
| Harbor | `harbor` | `harbor.happyladysauce.local` | project `knowledge-core` + scoped robots |

已验证的内网 UI：

- `https://nacos.happyladysauce.local/` 重定向到 `/nacos/`。
- `https://minio.happyladysauce.local/` 只路由 MinIO Console `:9001`。
- Nacos 上游使用内部 CA 验证，不允许 `insecureSkipVerify`。

## 3. 部署公共基础设施

配置 Agent 在 `k3s-home-deploy` 同步到 `/opt/k3s` 后执行：

```bash
cd /opt/k3s
bash scripts/verify-kustomize.sh
bash coredns/apply.sh
bash postgresql/apply.sh
bash redis/apply.sh
bash nacos/apply.sh
bash nats/apply.sh
bash etcd/apply.sh
bash minio/apply.sh
bash clamav/apply.sh
bash routes/apply.sh
```

组件脚本负责生成或恢复平台 Secret，并带有有界 rollout/wait。Etcd、MinIO、
ClamAV 的 `delete.sh` 默认保留 PVC；只有明确设置 `PURGE_DATA=1` 才允许清数据。
Nacos 复用 PostgreSQL 并由 `nacos-db-bootstrap` 创建 `nacos` database/role，不能再
部署独立 PostgreSQL。

项目 namespace 必须带有以下标签，公共 NetworkPolicy 才允许访问：

```yaml
infrastructure.happyladysauce.local/etcd-client: "true"
infrastructure.happyladysauce.local/minio-client: "true"
infrastructure.happyladysauce.local/clamav-client: "true"
```

`deploy/overlay/dev/namespace.yaml` 已声明这些标签。

## 4. dev 资源隔离契约

配置 Agent 必须创建项目级资源，应用中不得使用平台 root/admin 凭据。

| 依赖 | dev 契约 |
| --- | --- |
| PostgreSQL | database `knowledge_core_dev`；roles `knowledge_core_dev_identity`、`knowledge_core_dev_knowledge`、`knowledge_core_dev_collaboration`；三个 role 对该 database 有 `CONNECT,CREATE`，并分别拥有且只访问自己的同名 schema |
| Redis | 项目 ACL 用户；key 前缀 `knowledge-core:development:*` |
| Nacos | namespace ID `dev`；group `KNOWLEDGE_CORE`；四个只读 service user |
| NATS | account `KC_DEV`；user `knowledge-core-dev`；密码来自平台生成文件，不复制 admin 密码 |
| Etcd | user/role `knowledge-core-dev`；只允许 `/knowledge-core/development/registry/` prefix read/write |
| MinIO | bucket `knowledge-core-dev`；只允许该 bucket 所需的最小 object/list 权限 |
| ClamAV | 只允许到 `clamav.clamav.svc.cluster.local:3310` 的 TCP egress |

Etcd 平台脚本只初始化 root auth。项目 Etcd user/role 必须用一次性受控 Job 创建，
密码从 Secret 注入，不能出现在命令历史或 YAML。MinIO 同样通过 operator 凭据创建
bucket、policy 和项目 access key；root/operator Secret 不得复制到应用 namespace。

Nacos 动态配置 data ID 固定为：

```text
gateway.dynamic.yaml
identity.dynamic.yaml
knowledge.dynamic.yaml
collaboration.dynamic.yaml
```

每个服务使用独立 Nacos reader、`KNOWLEDGE_CORE_NACOS_KEY_ID` 和 32-byte
AES-256 KEK。动态文档通过 `configctl encrypt` 验证并加密，明文文档与 KEK 均不入库。
Nacos `3.2.3` 的管理发布接口是 `POST /nacos/v3/admin/cs/config`；表单字段必须使用
`dataId`、`groupName`、`namespaceId`、`content` 和 `type=json`。其中 `groupName` 是 v3
对旧 `group` 字段的替代。请求必须同时携带管理员登录得到的 Bearer token 和
`accessToken`，并严格校验响应为 `{"code":0,"data":true}`。运行时四个 reader 只保留
读取权限，不能使用平台管理员账号。

## 5. 外部 Secret 和信任链

### 5.1 应用对象

Argo CD 首次同步前，`knowledge-core-dev` 中必须存在：

| Object | required keys |
| --- | --- |
| ConfigMap `knowledge-core-trust-bundle` | `internal-ca.crt`, `nats-ca.crt` |
| Secret `harbor-pull-secrets` | `.dockerconfigjson`，type `kubernetes.io/dockerconfigjson` |
| Secret `knowledge-core-gateway-secrets` | Nacos 四个公共 key；`GATEWAY_AUTH_PUBLIC_KEY`, `GATEWAY_REDIS_PASSWORD`, `GATEWAY_ETCD_PASSWORD` |
| Secret `knowledge-core-identity-secrets` | Nacos 四个公共 key；`IDENTITY_AUTH_PRIVATE_KEY`, `IDENTITY_AUTH_PUBLIC_KEY`, `IDENTITY_POSTGRES_PASSWORD`, `IDENTITY_REDIS_PASSWORD`, `IDENTITY_ETCD_PASSWORD` |
| Secret `knowledge-core-knowledge-secrets` | Nacos 四个公共 key；`KNOWLEDGE_AUTH_PUBLIC_KEY`, `KNOWLEDGE_POSTGRES_PASSWORD`, `KNOWLEDGE_ETCD_PASSWORD`, `KNOWLEDGE_NATS_PASSWORD`, `KNOWLEDGE_OBJECT_STORAGE_ACCESS_KEY`, `KNOWLEDGE_OBJECT_STORAGE_SECRET_KEY` |
| Secret `knowledge-core-collaboration-secrets` | Nacos 四个公共 key；`COLLABORATION_POSTGRES_URL`, `COLLABORATION_REDIS_URL`, `COLLABORATION_NATS_PASSWORD`, `COLLABORATION_ETCD_PASSWORD` |

每个服务的 Nacos 公共 key 是：

```text
KNOWLEDGE_CORE_NACOS_USERNAME
KNOWLEDGE_CORE_NACOS_PASSWORD
KNOWLEDGE_CORE_NACOS_KEY_ID
KNOWLEDGE_CORE_NACOS_KEK
```

Secret 应使用 SOPS/age 管理。age 私钥只进入 `argocd/argocd-sops-age` 的
`keys.txt`，并离线备份；禁止提交明文 Secret、CA 私钥或未加密中间文件。

### 5.2 CI 对象

`ci` namespace 需要：

| Object | required keys/用途 |
| --- | --- |
| Secret `knowledge-core-github-webhook` | `secret`，Knowledge-Core source webhook HMAC |
| Secret `knowledge-core-github-app` | `app-id`, `installation-id`, `private-key` |
| Secret `knowledge-core-harbor-push` | `.dockerconfigjson`，project push robot |
| Secret `knowledge-core-harbor-pull` | `.dockerconfigjson`，project pull robot；绑定到 `knowledge-core-workflow` ServiceAccount |
| Secret `knowledge-core-cosign` | `cosign.key`, `cosign.password`（由 Cosign Kubernetes keystore 生成） |
| Secret `knowledge-core-ci-test-dependencies` | `postgres-password`，只供 Workflow sidecar 测试 |
| Secret `knowledge-core-argo-events-nats` | `auth.yaml`，内容为 `KC_CI` username/password |
| ConfigMap `knowledge-core-harbor-ca` | `ca.crt`；release tag 步骤通过 `HARBOR_CA_FILE` 显式传给 curl，不依赖 OpenSSL hashed CA directory |
| ConfigMap `knowledge-core-ci-proxy` | Workflow 的大小写 `HTTP(S)_PROXY`/`NO_PROXY` 环境 |

GitHub App 需安装到 Knowledge-Core 和 `k3s-home-deploy`。Knowledge-Core 需要
contents read/write、pull requests read/write 与 commit statuses read/write，GitOps
仓库需要 contents read/write；不要授予 administration 权限。`main` 保护规则要求
`knowledge-core/smoke` 且只允许该 App 推进，禁止直接 push、force-push 和删除；仓库只
启用 merge commit，禁用 squash/rebase 与 linear history，确保自动 `dev -> main` PR
始终生成双父 merge commit。

当前单节点集群的 GitHub 出口通过 `knowledge-core-ci-proxy` Deployment 桥接：受限
host-network 容器只监听 CNI 网关 `10.42.0.1:10992`，并转发到节点既有的
`127.0.0.1:10991` sing-box。它不创建 Service，也不监听节点 LAN 地址；节点代理必须先
处于监听状态。Workflow 主容器统一从同名 ConfigMap 注入代理变量。`NO_PROXY` 必须同时
覆盖 loopback、Pod/Service CIDR、`.svc`/`.svc.cluster.local`、节点地址以及
`*.happyladysauce.local`、`*.happyladysauce.cn`，使 sidecar、NATS 和 Harbor 流量不离开集群。

## 6. 容器内 Rust 构建和缓存

Rust 不使用机器原生 toolchain。CI 工具镜像由 k3s 内 rootless BuildKit 构建：

```text
harbor.happyladysauce.local/knowledge-core/ci-rust@sha256:<digest>
```

`.github/ci/RustDockerfile` 固定 Rust 版本和基础镜像 digest；首次构建使用可访问的
Docker Hub mirror，成功后始终由 Harbor digest 拉取。只有 Rust toolchain、
`cargo-deny` 或 Dockerfile 变化才重建该镜像。

缓存分两层：

- PVC `knowledge-core-ci-cache` 保存 `cargo-home`、`cargo-target`、Go 和扫描缓存。
- PVC `knowledge-core-buildkit-cache` 保存 OCI layer/cache metadata。

Rust 容器启动时把基础镜像的 `/usr/local/cargo/config.toml` 安装到持久化
`CARGO_HOME`，确保挂载缓存后仍使用镜像固定的 sparse registry 配置。Cargo HTTP
请求关闭 multiplexing，单次请求超时 60 秒并最多重试 5 次，避免 registry 连接抖动
占满 Workflow deadline。`cargo-deny` 获取官方 RustSec advisory DB 时，Git 子进程使用
临时 HTTP/1.1 配置和 60 秒低速窗口；这些参数不写入节点或 runner 的全局 Git 配置。
advisory DB 在编译前以单次 120 秒、最多 3 次的策略独立获取，成功后供应链检查
使用 `--offline`，避免 release 编译完成后才因 GitHub 网络抖动失败。失败尝试会删除
cargo-deny 留下的半成品数据库目录和锁文件，下一次尝试始终执行干净 clone。

Rust pod 执行 format、Clippy、tests、release build、cargo-deny、生成漂移和真实
PostgreSQL/Redis/NATS/Etcd 测试。它把已经通过检查的 release binary 写入
`.ci-artifacts/knowledge-core-collaboration`；`docker/collaboration/dockerfile` 只封装
该二进制，不再运行 Cargo。这样一个 Workflow 只进行一次 production release 编译。
PostgreSQL 测试 URL 不包含密码；`postgres-password` 通过独立环境变量注入，并由
Rust URL API 编码后再传给 SQLx，Secret 中的 URL 保留字符不会破坏连接字符串。

BuildKit 容器保持 non-root，仅为 rootlesskit 的 `newuidmap/newgidmap` 开启
`allowPrivilegeEscalation` 和 `SETUID/SETGID`，其他模板继续使用通用 restricted
security context。`coredns-custom` 把 `harbor.happyladysauce.local` exact rewrite 到
Higress Gateway Service 名，BuildKit、Trivy 和 Cosign 因而使用同一个受 CA 验证的 TLS host。
Go runtime 镜像直接引用官方 `gcr.io/distroless` 固定 digest，并通过 CI 出口代理拉取；不依赖
可能持续返回 5xx 的第三方 GCR 镜像站。
BuildKit 同时把 CI ConfigMap 中的大小写 HTTP(S)/NO_PROXY 作为 Docker build args 传入构建阶段，
并显式设置 `GOPROXY=https://goproxy.cn,direct`；Dockerfile 内的 `go mod download` 与 `go build`
因此不会绕过代理直连 `proxy.golang.org`。
每个镜像的 build+push 最多尝试三次并以 5/10 秒退避；只有成功尝试生成的 metadata
会进入 digest 收集，镜像站瞬时 5xx 不会留下可被后续步骤消费的半成品记录。
Trivy 扫描通过 template-level mutex 串行访问共享 `/cache/trivy`；首次运行只下载一次漏洞库，
后续镜像复用缓存，避免并发初始化 BoltDB 导致锁超时或 mmap 崩溃。
仓库只允许在 `.trivyignore.yaml` 中记录可审计的 VEX 例外，并同时限定镜像路径、package URL、
原因和到期日。当前 `CVE-2026-41602` 只影响未被 Gateway 调用的 `TFramedTransport`；
`govulncheck ./services/gateway/...` 已验证漏洞符号不可达，而固定的 Hertz/thriftgo 生成链仍依赖
pre-context Thrift API。该例外在 `2026-11-06` 到期，CI 使用 `--show-suppressed` 保留扫描证据。
Cosign sign 容器继续使用只读 root filesystem，并把 `HOME` 指向独立的 `/tmp` emptyDir；
Cosign v3 因而可以写入 Sigstore TUF 元数据，而不会要求可写镜像层或扩大容器权限。

## 7. 完整 CI/CD 流程

```text
push dev
  -> GitHub source webhook
  -> hooks.happyladysauce.cn/events/knowledge-core
  -> Higress -> Argo Events EventSource -> NATS KC_CI EventBus
  -> Sensor 只接受 refs/heads/dev，并以 mode=release 创建 Workflow
  -> Node、Go CI/race、Rust CI/真实依赖并行
  -> Go/Rust 双向 interop
  -> ref CAS 校验
  -> rootless BuildKit 构建并推送四个 SHA tag 镜像
  -> 收集 digest -> 单 Pod 顺序 Trivy 扫描
  -> GitOps base + overlay/dev digest CAS commit
  -> 强制 Argo CD refresh，等待精确 GitOps revision Synced/Healthy
  -> 校验四个 Deployment 使用预期 digest
  -> dev readiness + public documents 冒烟
  -> 冒烟失败时仅在 GitOps main 仍指向本次提交时创建 revert
  -> 冒烟成功后 Cosign 对四个 digest 签名
  -> 创建或复用唯一的 dev -> main PR，以测试 SHA 作 CAS 执行 merge commit
  -> 校验 merge commit 的 base/head/tree
  -> 从 VERSION 的 major.minor 自动选择下一个 patch 版本
  -> 为四个 Harbor digest 创建 vX.Y.Z tag
  -> 在 merge SHA 创建 Git tag 和 GitHub Release
  -> 发布全部成功后，非 force fast-forward dev 到 merge SHA
```

关键规则：

- Workflow 参数只有 `mode=verify|release`。手工回退默认 `verify`，执行全部质量门禁、构建和
  扫描，但不修改 GitOps、`main` 或 release；Sensor 固定使用 `release`。
- source ref 只接受 `refs/heads/dev`，每次写操作前重新校验 ref 仍指向目标 SHA。
- `VERSION` 只保存 `major.minor`，例如 `0.1`；CI 在已存在 `v0.1.*` 中选择下一个 patch。
- 构建开始和结束写 `knowledge-core/ci`；部署阶段独立写
  `knowledge-core/smoke`。两者都不写集群内不可访问的 Argo UI URL。
- RustSec 数据库拉取有三次、每次 120 秒的硬上限；失败重试前清理 cargo-deny 留下的半成品数据库目录，成功后 `cargo deny` 只离线读取该缓存。
- GitOps push 使用三次 compare-and-swap 重试；失败时不直接修改集群 Deployment。回滚同样
  检查当前 GitOps revision 和父提交，其他提交已经推进时拒绝覆盖。
- `main` 只能通过自动 PR 的 `merge` 方法推进；合并前后校验 head SHA、两个 parent、tree 和
  `main`/`dev` ref。版本发布成功后才用 `force=false` 把 `dev` 对齐到 merge SHA，使 release
  尾段失败时仍能幂等重跑同一 tested SHA。Sensor 丢弃该 GitHub
  App 产生的 dev 对齐 push，避免同一发布形成第二条 Workflow；发生分叉、ref 漂移或版本
  tag 冲突时立即失败。

构建产物只有 `gateway`、`identity`、`knowledge` 和 `collaboration`。部署清单始终使用
digest；SHA tag 用于构建追踪，成功发布后额外创建版本 tag。`configctl` 是运维工具，不发布
为服务镜像；精简流水线不再生成 SBOM 或 attestation。

## 8. GitHub source webhook

在 Knowledge-Core 仓库创建 webhook：

| GitHub 字段 | 值 |
| --- | --- |
| Payload URL | `https://hooks.happyladysauce.cn/events/knowledge-core` |
| Content type | `application/json` |
| Secret | 与 `ci/knowledge-core-github-webhook:secret` 相同 |
| Events | Just the push event |
| SSL verification | Enable |

公网 OpenResty 终结 TLS，把 Host 改为 `hooks.happyladysauce.local` 并转发到
`http://100.100.100.2:30080`。Higress 只接受 Exact path
`/events/knowledge-core`。Secret 创建后把 EventSource `active` 改为 `true`，重新
apply `deploy/ci`；先用 GitHub redelivery 验证 2xx 和 HMAC 拒绝路径。

`.github/workflows/dev-to-main.yml` 只保留 `workflow_dispatch` 手工回退，不响应 push，
不会与 k3s 流水线重复推广同一个 dev SHA。手工回退始终等价于 `mode=verify`。

## 9. Argo CD Application

四个真实 digest、外部 Secret 和 SOPS age key 就绪后，应用仓库中已经提供
`Knowledge-Core/argocd/project.yaml` 与 `application.yaml`；可以在 UI 使用等价参数，
也可以直接 apply 这两个对象：

| 字段 | 值 |
| --- | --- |
| Name | `knowledge-core-dev` |
| Project | `knowledge-core` |
| Repository URL | `git@github.com:HappyLadySauceM/k3s-home-deploy.git` |
| Revision | `main` |
| Path | `Knowledge-Core` |
| Cluster | in-cluster |
| Namespace | `knowledge-core-dev` |
| Sync policy | Automated + Prune + Self Heal |

根 Kustomization 只包含 `overlay/dev`，overlay 自己创建 Namespace，不依赖
`CreateNamespace`。source 必须选择已安装的 `sops-v1.0` CMP；
`Knowledge-Core/argocd/repository.sops.yaml` 保存只读 SSH deploy key，必须先由有 age
私钥的受控环境解密并 apply，禁止在仓库留下 `repository.yaml`。

## 10. Argo CD webhook

GitOps webhook 与 source webhook 共用公网 host，但使用独立 Exact path：

```text
https://hooks.happyladysauce.cn/api/webhook
  -> OpenResty，Host: hooks.happyladysauce.local
  -> Higress :30080
  -> argocd-server.argocd.svc.cluster.local:80/api/webhook
```

在 `argocd/argocd-secret` 注入 `webhook.github.secret`，然后在
`HappyLadySauceM/k3s-home-deploy` 创建 GitHub webhook：

| GitHub 字段 | 值 |
| --- | --- |
| Payload URL | `https://hooks.happyladysauce.cn/api/webhook` |
| Content type | `application/json` |
| Secret | 与 `argocd-secret:webhook.github.secret` 相同 |
| Events | Just the push event |
| SSL verification | Enable |

该 webhook 只触发快速 refresh，是否 sync 仍由 Application sync policy 决定。
验收时 GitHub delivery 必须返回 2xx，GitOps push 后应在秒级看到 refresh；不能用
默认轮询掩盖 webhook 配置错误。

## 11. 启用顺序

1. 同步并验证 `/opt/k3s/kustomization.yaml`，部署公共基础设施和 routes。
2. 创建 dev 数据库角色、Redis ACL、Nacos reader、Etcd prefix role、MinIO bucket/user。
3. 创建 `knowledge-core-dev` 外部 Secret、trust bundle 和 Harbor pull Secret。
4. 在 k3s BuildKit 构建 `ci-control`、`ci-go`、`ci-rust`，把真实 digest 写入 WorkflowTemplate。
5. 创建 CI GitHub App/webhook、Cosign、test dependency Secret 和 Harbor robots。
6. `kubectl apply -k deploy/ci`，先手工运行 `mode=verify` Workflow。
7. 验证四镜像 build/push、单节点 scan、签名容器写目录和单次 Rust release 编译。
8. 初始化 GitOps `Knowledge-Core/base`、`overlay/dev`，写入首组真实 digest，并提交推送。
9. 应用只读 repository Secret、AppProject 和 Application，确认 automated/prune/self-heal。
10. 配置两个 GitHub webhook，确认 EventSource `active: true`，完成一次 dev push release 验收。
11. 验证 `knowledge-core/smoke` 后自动 PR 生成双父 merge commit，版本镜像 tag、Git tag 和
    Release 对应该 merge SHA，最后 `dev` 非 force fast-forward 到 `main`；GitHub Actions
    继续仅作为手工 `verify` 回退。

## 12. 验收命令

```powershell
# Knowledge-Core
kubectl kustomize .\deploy\overlay\dev | Out-Null
kubectl --kubeconfig .\kubectl\k3s.yml apply --dry-run=server -k .\deploy\ci
make ci

# 集群状态
kubectl --kubeconfig .\kubectl\k3s.yml get pod -A
kubectl --kubeconfig .\kubectl\k3s.yml -n ci get workflowtemplate,eventbus,eventsource,sensor
kubectl --kubeconfig .\kubectl\k3s.yml -n ci get workflows --sort-by=.metadata.creationTimestamp
kubectl --kubeconfig .\kubectl\k3s.yml -n argocd get applications.argoproj.io
```

在 `/opt/k3s` 额外执行：

```bash
bash scripts/verify-kustomize.sh
kubectl rollout status statefulset/etcd -n etcd --timeout=300s
kubectl rollout status statefulset/minio -n minio --timeout=300s
kubectl rollout status statefulset/clamav -n clamav --timeout=900s
kubectl rollout status statefulset/nats -n nats --timeout=300s
kubectl rollout status deployment/nacos -n nacos --timeout=600s
curl -fsSIL https://nacos.happyladysauce.local/
curl -fsSI https://minio.happyladysauce.local/
```

端到端还必须核对：

- Workflow 日志只有一次 project `cargo build --release`。
- Harbor 四个 SHA tag、版本 tag 和 Cosign signature 指向同一组 digest。
- GitOps commit 只包含 base 快照和 `overlay/dev` digest，没有 Secret 明文。
- Argo CD sync 后四个 Pod ready，readiness 能证明外部依赖可用。
- 错误 webhook HMAC 被拒绝，两个 GitHub delivery 都返回预期状态。
- Etcd/MinIO apply 重跑幂等，PVC 和项目账号不发生轮换。

## 13. 回滚和边界

- build/scan 前失败时不更新 GitOps；GitOps 更新后的 wait 或 smoke 失败会执行受 CAS 保护的 revert。
- 应用回滚只 revert 本次 GitOps promotion，禁止直接改 Deployment image；签名、main 或 release
  失败发生在 smoke 之后，不回滚已经验证通过的 dev 部署，可幂等重跑同一 SHA。
- webhook 故障时 Argo CD 可暂时轮询；source CI 使用手工 `mode=verify`，不得跳过 scan 或 ref CAS。
- cache 损坏时只删除明确命名的 CI cache PVC，不得删除应用数据 PVC。
- Secret、CA 或 SOPS age key 泄露后必须轮换凭据和受影响的历史密文。

当前明确不在本次范围：Identity/Knowledge repository 的真实 PostgreSQL 测试。
单节点 `local-path` 仍没有 HA；备份恢复、容量、证书轮换、依赖 stop/start 和完整
WebSocket E2E 演练完成前，不能把该 dev 集群描述为生产级环境。
