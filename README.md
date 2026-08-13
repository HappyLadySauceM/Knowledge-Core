# Knowledge Core

Knowledge Core 是一个支持文档元数据、权限、附件、实时协作与版本恢复的知识协作后端。仓库包含 Go module 与 Rust workspace；Collaboration 服务使用 Rust、Yrs 和标准 y-sync 协议。

当前实现包含四个服务：

- Gateway：公网 HTTP edge，负责严格输入校验、JWT、CORS、安全头、限流、错误映射和上游编排。
- Identity：用户注册、密码认证、账户锁定、Ed25519 JWT 签发与用户状态复核。
- Knowledge：文档元数据、成员权限、发布、附件、回收站、投影、配额和 outbox。
- Collaboration：Yrs/y-sync WebSocket、一次性 session ticket、持久化 update、快照、版本、恢复和多实例同步。

详细架构和运行时契约见 [docs/framework-design.md](docs/framework-design.md)，编码约束见 [AGENTS.md](AGENTS.md)。

## 当前能力

| 范围 | 公开入口 | 行为 |
| --- | --- | --- |
| 健康 | `GET /health/live`、`GET /health/ready` | Gateway 进程与依赖状态 |
| 用户 | `POST /api/v1/users` | 注册用户 |
| 会话 | `POST /api/v1/sessions` | 用户名或邮箱登录，返回 Bearer token |
| 当前用户 | `GET /api/v1/users/me` | 验签后向 Identity 复核 active 状态与 token version |
| 公开文档 | `GET /api/v1/documents`、`GET /api/v1/documents/:slug` | 发布列表、投影内容和附件元数据 |
| 附件下载 | `GET /api/v1/attachments/:attachment_id/content` | 返回 `303 See Other` 到短期预签名地址 |
| Studio 文档 | `/api/v1/studio/documents` | 列表、创建、读取、更新、删除、发布和取消发布 |
| 成员 | `/api/v1/studio/documents/:document_id/members` | viewer/editor 成员管理 |
| 版本 | `/api/v1/studio/documents/:document_id/versions` | 手工版本、详情和恢复 |
| 协作会话 | `POST /api/v1/studio/documents/:document_id/collaboration-sessions` | 创建短期、单次使用的 WebSocket ticket |
| 附件 | `/api/v1/studio/documents/:document_id/attachments` | 预签名上传、完成扫描和删除 |
| 回收站 | `/api/v1/studio/trash` | 删除文档列表与恢复 |
| 实时协作 | `ws://localhost:8091/v1/instances/:ordinal/documents/:document_id` | y-sync、awareness、权限复核、只读控制和稳定关闭码；Compose 单实例时 Gateway 返回 ordinal `0`，进程仍接受 `/v1/documents/:document_id` |

完整 HTTP 契约源为 [idl/http/v1/gateway.thrift](idl/http/v1/gateway.thrift)。

## API 契约

- JSON 请求必须使用 `Content-Type: application/json`。未知字段、额外 JSON 值、重复关键 header/query、非法数字和未知 query 均被拒绝。
- 成功响应直接返回资源或分页对象，不使用 envelope。
- 失败响应使用 RFC 9457 `application/problem+json`，包含稳定的 `code`、`key`、`request_id`，可用时包含 `trace_id`。
- 文档和成员写操作使用强 ETag。响应示例为 `ETag: "12"`，调用方必须把该值原样放入 `If-Match`。
- 支持幂等的创建/恢复操作通过 `Idempotency-Key` 传入 1-128 个可见 ASCII 字符。
- 分页 `cursor` 是 opaque token；客户端只能保存并原样回传，不能依赖其内部结构。
- Gateway 只使用配置的公开 base URL、Collaboration WebSocket URL 生成响应地址，不信任请求的 `Host` header。

示例：

```powershell
$baseUri = "http://127.0.0.1:8080"
$user = Invoke-RestMethod `
  -Method Post `
  -Uri "$baseUri/api/v1/users" `
  -ContentType "application/json" `
  -Body '{"username":"alice","email":"alice@example.com","password":"local-password-123"}'

$session = Invoke-RestMethod `
  -Method Post `
  -Uri "$baseUri/api/v1/sessions" `
  -ContentType "application/json" `
  -Body '{"identifier":"alice","password":"local-password-123"}'

$token = $session.access_token
$headers = @{ Authorization = "Bearer $token" }
Invoke-RestMethod -Uri "$baseUri/api/v1/users/me" -Headers $headers
```

## 架构

```mermaid
flowchart LR
    Client[HTTP / WebSocket client] --> Gateway[Gateway :8080]
    Client --> Collaboration[Collaboration :8091]
    Gateway --> Identity[Identity RPC :8881]
    Gateway --> Knowledge[Knowledge RPC :8882]
    Gateway --> CollaborationRPC[Collaboration RPC :8883]
    Collaboration --> KnowledgeRPC[Knowledge RPC :8882]
    Identity --> PostgreSQL[(PostgreSQL)]
    Identity --> Redis[(Redis)]
    Knowledge --> PostgreSQL
    Knowledge --> S3[(S3 / MinIO)]
    Knowledge --> ClamAV[ClamAV]
    Knowledge --> NATS[(NATS)]
    Collaboration --> PostgreSQL
    Collaboration --> Redis
    Collaboration --> NATS
    Collaboration --> Etcd
    Gateway --> Redis
    Gateway --> Etcd[(Etcd)]
    Identity --> Etcd
    Knowledge --> Etcd
```

Knowledge 不保存 Yjs update、快照或版本；这些数据属于 Collaboration。Collaboration 不直连 Identity 或 Knowledge 数据库，而是通过生成的 Knowledge Thrift RPC 取得文档权限并提交投影。

## 环境要求

- Go `1.26.5`
- Rust `1.97.1`（由 `services/collaboration/rust-toolchain.toml` 固定）
- cargo-deny `0.20.2`（本地执行供应链门禁）
- Node.js `24.18.1`（仅 `services/collaboration/interop` 互操作 fixture）
- Docker Engine/Desktop 与 Compose v2
- GNU Make
- 修改 IDL 时使用 Kitex `v0.16.2`、Hertz `v0.9.7`、thriftgo `0.4.5`

主要端口：

| 服务 | 端口 |
| --- | --- |
| Gateway public/admin | `8080` / `8082` |
| Identity RPC/admin | `8881` / `8081` |
| Knowledge RPC/admin | `8882` / `8083` |
| Collaboration WebSocket/RPC/admin | `8091` / `8883` / `8084` |
| PostgreSQL/Redis/Etcd/NATS | `5432` / `6379` / `2379` / `4222` |
| MinIO/console/ClamAV/Prometheus/Tempo | `9000` / `9001` / `3310` / `9090` / `3200` |
| OTel Collector (OTLP gRPC/HTTP) | `4317` / `4318` |

## Compose 快速开始

以下 PowerShell 示例把 Secret 保留在当前进程环境中，不创建 `.env` 文件：

```powershell
$keys = @{}
go run ./scripts/authkeys | ForEach-Object {
  $name, $value = $_ -split "=", 2
  $keys[$name] = $value
}

$postgresPassword = Read-Host "PostgreSQL password"
$encodedPostgresPassword = [uri]::EscapeDataString($postgresPassword)

$env:KC_POSTGRES_PASSWORD = $postgresPassword
$env:KC_COLLABORATION_POSTGRES_URL = "postgres://knowledge_core:$encodedPostgresPassword@postgres:5432/knowledge_core"
$env:KC_AUTH_PRIVATE_KEY = $keys["IDENTITY_AUTH_PRIVATE_KEY"]
$env:KC_AUTH_PUBLIC_KEY = $keys["IDENTITY_AUTH_PUBLIC_KEY"]
$env:KC_MINIO_ACCESS_KEY = "knowledge-core-local"
$env:KC_MINIO_SECRET_KEY = Read-Host "MinIO secret"

docker compose -f docker/infrastructure/docker-compose.yml up -d --build
docker compose -f docker/infrastructure/docker-compose.yml ps
```

ClamAV 首次启动需要初始化病毒库，Knowledge 在 ClamAV、对象存储、NATS、数据库、Etcd、Identity 和 Collaboration 均可用后才会 ready。Compose 默认启用本地 OTel Collector + Tempo；打开 `http://127.0.0.1:3200` 查询 trace。collector 的 tail sampling 保留错误、parking/DLQ、超过 1 秒的 trace，并按 10% 保留其余成功 trace。生产部署不要复用本地地址，OTLP endpoint、TLS 和鉴权由部署平台注入。检查入口：

```powershell
Invoke-RestMethod http://127.0.0.1:8081/readyz
Invoke-RestMethod http://127.0.0.1:8082/readyz
Invoke-RestMethod http://127.0.0.1:8083/readyz
Invoke-RestMethod http://127.0.0.1:8080/health/ready
```

Knowledge RPC 的 `Ping` 返回完整 readiness，`Live` 只返回 `knowledge/live` 且不读取 readiness；Collaboration 启动和 supervisor 使用 `Live`，避免双方 readiness 冷启动互相等待。Collaboration 的 `Ping` 与 admin ready 共用完整应用状态；其余六个 RPC 在应用 not-ready 时会先返回 `40007 / collaboration.unavailable`，不会调用 Knowledge、ticket、store 或 actor。RPC serve task 的任何非计划退出、Etcd 注册 key/value/lease 所有权丢失或 permission consumer 尚未追平启动快照都会使服务 fail closed。

停止后移除当前 shell 中的 Secret：

```powershell
docker compose -f docker/infrastructure/docker-compose.yml down
Remove-Item Env:KC_POSTGRES_PASSWORD
Remove-Item Env:KC_COLLABORATION_POSTGRES_URL
Remove-Item Env:KC_AUTH_PRIVATE_KEY
Remove-Item Env:KC_AUTH_PUBLIC_KEY
Remove-Item Env:KC_MINIO_ACCESS_KEY
Remove-Item Env:KC_MINIO_SECRET_KEY
$keys.Clear()
```

生产环境必须使用部署平台 Secret manager，并为外部 WebSocket、Gateway/Knowledge 到 Collaboration RPC、Collaboration 到 Knowledge RPC、PostgreSQL、Redis、NATS 和 Etcd 配置代码要求的 TLS/mTLS。每个 Collaboration 副本必须配置唯一且重启后稳定的 `COLLABORATION_INSTANCE_ID`，以保持 JetStream durable consumer 的重投递语义。跨服务 subject 固定为 `collaboration.documents.updated`、`collaboration.documents.invalidated` 和 `knowledge.permissions.changed`。前两个 subject 属于默认名为 `KNOWLEDGE_CORE_EVENTS` 的 document stream，固定 24 小时/1 GiB 保留；permission subject 独占默认名为 `KNOWLEDGE_CORE_PERMISSIONS` 的 stream，`max_bytes=-1`，只按固定 24 小时 max age 清理，避免文档写入量挤掉仍覆盖 ticket TTL 的撤权事件。两个 stream 都要求 Limits retention、File storage、DiscardOld、24 小时 duplicate window 和 1 MiB max message，且名称必须不同。新 permission durable 使用 `DeliverPolicy::All`，以创建 consumer 后读取的 permission stream `last_sequence` 为启动快照；服务端 ACK floor 的 stream sequence 越过该快照后才允许 ready，retention 已清空剩余集合时要求 `num_pending` 与 `num_ack_pending` 同时为零。subject、stream 或 consumer 契约漂移都会拒绝 ready。

## 本地开发

顶层不再保留共享 `config/`；Go 服务的非敏感静态配置由各自的
`services/<service>/etc/config.yaml` 管理。单独运行 Go 服务时显式指定对应 YAML：

```powershell
go run ./services/identity --config services/identity/etc/config.yaml
go run ./services/knowledge --config services/knowledge/etc/config.yaml
go run ./services/gateway --config services/gateway/etc/config.yaml
```

Rust Collaboration 的配置加载器位于服务内，通过环境变量接收静态配置；先启动
PostgreSQL、Redis、NATS、Etcd 和 Knowledge，并设置至少数据库连接后运行：

```powershell
Set-Location services/collaboration
$env:COLLABORATION_POSTGRES_URL = "postgres://knowledge_core:<password>@127.0.0.1:5432/knowledge_core"
cargo run --locked -p knowledge-core-collaboration
```

Node 目录只用于 Yjs/y-prosemirror 互操作验证，不参与生产服务：

```powershell
Set-Location services/collaboration/interop
npm ci
npm run ci
```

## 质量门禁

根门禁同时覆盖 Go 和 Rust：

```text
make tidy
make ci
make race
```

单独执行 Rust 与 Node 互操作门禁：

```text
cd services/collaboration
cargo fmt --all --check
cargo clippy --workspace --all-features --locked -- -D warnings
cargo test --workspace --all-targets --all-features --locked
cargo deny check advisories bans licenses sources
cd interop
npm ci
npm run ci
```

`.github/workflows/pipeline.yml` 是唯一项目 workflow；它调用 `/opt/HappyLadySauceM/ci-templates`
发布的 Python 控制镜像。`dev` push 先运行 `make ci` 与 Collaboration Node 互操作门禁，再按
变更服务从 Harbor 缓存构建并覆盖 `dev` 镜像 tag。控制镜像复用稳定 Buildx builder，并向
Dockerfile 传入 `BUILD_JOBS`（宿主机 CPU 的 3/4）；Go/Rust 构建层通过固定 cache mount id
跨服务复用 module/cargo 缓存。镜像不扫描、不签名；当前 `dev` tag 构建前会
保留为 `previous`，Argo CD 同步和冒烟成功后删除 `previous`，由 Harbor 垃圾回收回收旧层。

仓库 Actions Secret 只提供 `HARBOR_DOCKER_CONFIG_JSON` 与 `HARBOR_CA_PEM`。`release` environment
只允许 `dev`，并提供 `GH_APP_ID`、`GH_APP_PRIVATE_KEY`、`K3S_RELEASE_KUBECONFIG` 与
`DEEPSEEK_API_KEY`；Secret 不得写入 workflow、项目配置或日志。

## k3s 与 GitOps

共享基础设施的声明源是 `k3s-home-deploy/k3s`，服务器路径为 `/opt/k3s`。它复用已有
PostgreSQL 和 Redis，并在独立 namespace 提供 Nacos、NATS、Etcd、MinIO 与 ClamAV；Nacos
使用共享 PostgreSQL。项目 namespace 只接收项目级账号，平台 root/admin Secret 不进入应用。

应用部署模板按服务放在 `deploy/<service>/`。每个服务自主维护 `base/`
中的 Deployment、Service 与 Kustomization，以及 `overlay/dev/` 中的日志、运行环境、
超时等服务行为配置；不再使用共享的 `deploy/base` 或 `deploy/overlay/dev`。PostgreSQL、
Redis、Etcd、NATS、Nacos、MinIO 与 ClamAV 的 endpoint、账号、TLS、数据库名和前缀由私有
`k3s-home-deploy` 维护，并在 `Knowledge-Core/dev/<service>` 中合入对应服务 ConfigMap。
`Knowledge-Core/dev/common` 统一提供运行时补丁和不可变镜像 digest；共享 Namespace、Secret、
trust bundle、NetworkPolicy 和发布 RBAC 由 `knowledge-core-foundation-dev` 管理。
GitOps 仓库同时保存 SOPS Secret、trust bundle 和不可变镜像 digest；应用仓库不记录具体
集群拓扑或凭据。

质量、镜像构建、GitOps 快照、Argo 健康和 dev 冒烟均在同一 workflow 中顺序执行。Rust 门禁只在
Rust、IDL、生成器、Makefile 或 workflow 变更时运行；其他提交仍执行 Go 门禁和生成漂移检查。
`make ci` 不执行 Rust release 编译，受影响的 Collaboration 镜像构建会在 Docker 阶段完成该编译。
冒烟成功后，DeepSeek 根据限长并脱敏的代码 diff 生成中文功能变更摘要：共享/CI/构建类变更
写入 Release 的 Shared changes；仅当 `services/<svc>` 或 `deploy/<svc>` 有业务变更时才出现
对应服务小节。DeepSeek 失败会阻止 `main` promotion。成功时只允许 fast-forward `main`，
并只创建一个 `vMAJOR.MINOR.PATCH` 聚合 tag 与单一 GitHub Release（标题同为该版本号）；不再创建
各服务独立 Git tag。Release 列出本次 Deployed services，不记录 commit/workflow 元数据。
GitOps 快照推送同样要求远端分支未发生变化，禁止 force-push。

项目级聚合版本读取根目录 `VERSION`；`services/<service>/VERSION` 可作人工参考，CI 不再据此打 tag。

Argo CD repository Secret、AppProject 和 ApplicationSet 由私有 GitOps 仓库声明。ApplicationSet
为 Gateway、Identity、Knowledge、Collaboration 分别生成 Application；服务 Application 使用
普通 Kustomize source，foundation Application 使用独立 `ksops-v1.0` source 解密 Secret；
同步策略为 Automated/Prune/Self Heal，镜像始终引用不可变 Harbor digest。

当前测试覆盖领域、逻辑、transport、严格输入、错误映射、Collaboration commit-before-broadcast、恢复、投影、outbox、生命周期、双向 Kitex/Volo、Yjs 和多实例 JetStream 行为。按照当前范围，Identity 与 Knowledge repository 尚未包含真实 PostgreSQL 集成测试；不要把现有 mock/单元测试等同于数据库兼容性验证。Rust Collaboration 的性能对比、完整 Compose WebSocket E2E、依赖 stop/start、备份及切换/回滚演练仍需在发布前完成。

## 代码生成

契约源位于 `idl/`，生成文件所有权以 [scripts/generated-files.txt](scripts/generated-files.txt) 为准：

```powershell
make generate
make generate-check
```

IDL 变更还必须与 merge base 执行兼容检查：

```text
go run ./scripts/idlguard compat-git <merge-base> idl
```

本次 API 允许不兼容演进；兼容检查仍应执行并记录具体差异，不能静默跳过。
