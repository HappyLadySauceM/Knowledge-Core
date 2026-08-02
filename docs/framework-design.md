# Knowledge Core 框架设计

> 状态：当前实现基线，更新于 2026-08-02。本文只描述仓库中已经实现的行为。系统由 Gateway、Identity、Knowledge 和 Collaboration 四个服务组成；Identity 与 Knowledge 的真实 PostgreSQL 集成测试仍未纳入当前门禁。
>
> 规划提案（尚未实现）：[`Rust Collaboration 重写设计提案`](rust-collaboration-design.md)。该提案不属于本文描述的当前运行时能力。

## 1. 系统目标与边界

Knowledge Core 是一个单 Go module Monorepo，Collaboration 使用 Node.js/TypeScript。公网 HTTP 使用 Hertz，内部同步 RPC 使用 Kitex + Thrift，实时协作使用 Hocuspocus/Yjs。关系数据按服务 schema 隔离，附件存入 S3 兼容对象存储。

当前实现覆盖：

- 用户注册、密码登录、账户锁定、Ed25519 access token 和用户状态复核。
- 文档元数据、成员权限、发布、软删除、恢复、延迟清理和公开投影。
- 附件预签名上传、异步 ClamAV 扫描、短期下载地址和对象清理。
- Yjs update 持久化、快照压缩、手工/自动版本、恢复、多实例同步和权限失效。
- Gateway 的严格输入边界、安全中间件、限流、稳定错误映射和上游编排。

当前明确不提供：

- refresh token、logout、JWT cookie、JWKS 或在线密钥轮换。
- 跨服务数据库事务、exactly-once 消息或同步的全局强一致性。
- 配置热更新；连接、证书和 provider 变化通过滚动重启生效。
- 任意服务共享数据库模型或绕过公开/内部契约直连其他服务的 schema。

## 2. 服务与数据所有权

| 服务 | 入口 | 拥有的数据 | 必需依赖 |
| --- | --- | --- | --- |
| Gateway | public `:8080`，admin `:8082` | 无业务数据库 | Redis、Etcd resolver、Identity RPC、Knowledge RPC、Collaboration internal HTTP |
| Identity | RPC `:8881`，admin `:8081` | PostgreSQL `identity` schema | PostgreSQL、Redis、Etcd registry |
| Knowledge | RPC `:8882`，admin `:8083`，internal `:8090` | PostgreSQL `knowledge` schema、S3 对象 | PostgreSQL、Etcd registry/resolver、NATS、Identity RPC、S3、ClamAV、Collaboration internal HTTP |
| Collaboration | WebSocket `:8091`，internal `:8092` | PostgreSQL `collaboration` schema 中的 Yjs update、snapshot、version 和 job | PostgreSQL、Redis、NATS、Knowledge internal HTTP |

所有权规则如下：

- Gateway 是 HTTP edge，不持久化领域数据，不直连 Identity、Knowledge 或 Collaboration 的数据库。
- Identity 只拥有用户、凭据状态和 token version。其他服务通过 Identity RPC 获取当前用户或解析成员用户名。
- Knowledge 拥有文档元数据、成员、公开投影、附件状态、幂等记录和 outbox，不保存 Yjs 二进制状态或版本。
- Collaboration 拥有协作状态和版本，不复制文档权限规则。每次连接或 token 更新都通过 Knowledge internal HTTP 复核访问级别。
- `pkg/` 只承载跨服务公共能力，禁止导入 `services/*`。

主要目录职责：

```text
pkg/
  app/          Go 进程生命周期与组件编排
  auth/         Ed25519 JWT wire contract
  codec/json/   公共严格 JSON codec
  error/        稳定错误、Kitex/Hertz 映射与 RFC 9457
  option/       可复用配置模型及校验
  log/trace/metrics/health/metadata/
  postgres/redis/etcd/nats/
  transport/    Hertz、Kitex 的公共传输适配

services/<go-service>/
  main.go       只创建并执行应用命令
  spec.go       只声明应用规格
  internal/config/
  internal/context/
  internal/domain|logic|repository|transport/

services/collaboration/
  src/collaboration/  Hocuspocus/Yjs 边界
  src/storage/        PostgreSQL 持久化与迁移
  src/http/           internal HTTP
  src/workers.ts      投影、快照、自动版本和维护 worker
```

## 3. 依赖装配

Go 服务使用 `pkg/app.Spec[C]` 注册配置、公共 runtime options 和 `ServiceContext`。`Runtime` 只持有进程级日志、追踪、健康、指标、component 和 cleanup，不是服务定位器。业务资源由各服务的 `NewServiceContext` 显式构造，构造函数校验必需依赖并返回错误。

装配链如下：

```text
Identity
  PostgreSQL -> migration -> UserRepository -> register/authenticate/get-user logic
  Redis + Etcd registry -> Kitex RPC + Hertz admin

Knowledge
  PostgreSQL -> versioned SQL migration -> Store -> document/member/attachment logic
  Etcd registry/resolver -> Identity client
  NATS + S3 + ClamAV + Collaboration client -> workers
  Kitex RPC + internal Hertz + Hertz admin

Gateway
  Redis limiter + Etcd resolver
  -> Identity/Knowledge typed Kitex clients + Collaboration typed HTTP client
  -> JWT verifier + HTTP middleware/handlers
  -> Hertz public + Hertz admin

Collaboration
  PostgreSQL migration + Knowledge HTTP client + NATS invalidator
  -> Hocuspocus/Yjs + Redis extension
  -> version service + workers + internal HTTP
```

已打开资源在成功创建后立即注册 cleanup；注册失败时同步关闭刚创建的资源。请求路径不使用全局 Viper、全局连接或隐藏式依赖注入。

## 4. 外部 API 契约

HTTP 契约源是 `idl/http/v1/gateway.thrift`。Gateway 当前公开：

| 范围 | 路由 |
| --- | --- |
| 健康 | `GET /health/live`、`GET /health/ready` |
| 用户/会话 | `POST /api/v1/users`、`POST /api/v1/sessions`、`GET /api/v1/users/me` |
| 公开文档 | `GET /api/v1/documents`、`GET /api/v1/documents/:slug` |
| 公开附件 | `GET /api/v1/attachments/:attachment_id/content` |
| Studio 文档 | `/api/v1/studio/documents` 下的列表、创建、读取、更新、删除、发布和取消发布 |
| 成员 | `/api/v1/studio/documents/:document_id/members` |
| 版本 | `/api/v1/studio/documents/:document_id/versions` |
| 附件 | `/api/v1/studio/documents/:document_id/attachments` |
| 回收站 | `/api/v1/studio/trash` |

Gateway 的 HTTP 规则：

- 需要 body 的请求必须使用 `Content-Type: application/json`。
- Go HTTP 入口统一使用 `pkg/codec/json`，拒绝未知字段、多余 JSON 值、非法数字、未知 query、重复关键 header/query 和非法 path 参数。
- 成功响应直接返回资源或分页对象，不使用 envelope。
- 错误响应使用 RFC 9457 `application/problem+json`，包含稳定 `code`、`key`、`request_id`，可用时包含 `trace_id`。未知内部错误不暴露 cause、SQL、地址或堆栈。
- Studio 路由和 `/users/me` 必须认证；公开文档允许匿名读取，并可在携带有效 token 时返回调用方可见的访问上下文。
- 文档和成员写操作使用强 ETag，格式为 `"<revision>"`；调用方必须把读取到的值原样放入 `If-Match`。
- 支持幂等的创建/恢复操作使用 `Idempotency-Key`；分页 cursor 是 opaque token。
- 附件下载返回 `303 See Other` 和短期预签名 `Location`，不代理对象正文。
- 响应中的公开 HTTP/WebSocket URL 只来自已校验配置，不信任请求 `Host`。

## 5. 内部契约与实时协作

Identity RPC 提供 `Register`、`Authenticate`、`GetCurrentUser` 和 `ResolveUser`。Knowledge RPC 提供文档、成员和附件用例；Gateway 只调用生成的 typed client，不复制领域规则。

Knowledge internal HTTP 仅供 Collaboration 使用：

- `POST /internal/v1/documents/:document_id/authorization`：校验匿名或 Bearer 身份，返回 `viewer`、`editor` 或 `owner` 及 permission revision。
- `PUT /internal/v1/documents/:document_id/projection`：按单调 sequence 更新公开 rich-text 投影。
- `GET /health/live`、`GET /health/ready`：供依赖探测。

Collaboration internal HTTP 仅供 Gateway/Knowledge 使用：

- 版本列表、创建、详情和恢复。
- `DELETE /internal/v1/documents/:document_id`：在最终清理前删除协作数据。
- `/health/live` 和 `/health/ready`。

生产环境中两个 internal HTTP listener 使用 mTLS。内部请求保留超时、request ID 和安全错误映射。

WebSocket 入口固定为配置的 collaboration path。连接时严格检查 Origin，并向 Knowledge 复核文档权限：viewer 被设置为只读，editor/owner 可提交 update。token 临近过期时服务要求同步新 token；超过 grace period 仍未更新则以稳定协议码关闭连接。权限变更和文档失效通过 NATS 关闭相关连接，迫使客户端重新鉴权。

`DurableUpdateGate` 在同步 ACK 前持久化 update 并推进 sequence；单次 update 和合并文档大小均有上限。Redis Hocuspocus extension 用于多实例协作广播，PostgreSQL 是持久事实源。

## 6. 身份与授权

Access token 使用 Ed25519，默认有效期 15 分钟。固定契约为：

- issuer：`knowledge-core.identity`
- audience：`knowledge-core.api`
- claims：subject、role、token version、`iat`、`nbf`、`exp` 和随机 `jti`
- 验证：固定 EdDSA algorithm、issuer、audience、严格 base64 解码和 30 秒时钟偏差

Gateway 先做本地验签，再把原始 access token 通过 Kitex metadata 传给 Identity/Knowledge。Identity 的 `GetCurrentUser` 与 `ResolveUser` 会重新验签并从数据库复核 active 状态和最新 token version；依赖异常时失败关闭。

文档 owner 始终拥有最高权限。成员只允许 `viewer` 或 `editor`，成员变更推进 permission revision 并发布权限失效事件。Knowledge 负责最终授权决策，Gateway 和 Collaboration 只消费结果。

密码使用 bcrypt。登录失败计数和 15 分钟锁定由 PostgreSQL 原子更新与事务行锁维护；响应和日志永远不包含密码 hash。

## 7. 持久化与一致性

### 7.1 Identity

Identity 启动时在 PostgreSQL advisory transaction lock 内创建/校验 `identity` schema，执行 GORM `AutoMigrate`，并补齐大小写不敏感唯一索引和命名 check constraint。所有 repository 查询使用 `db.WithContext(ctx)`。

AutoMigrate 只适用于当前增量阶段，不替代破坏性 schema 演进的 expand/migrate/contract 流程。

### 7.2 Knowledge

Knowledge 使用带版本记录的显式 SQL migration。`knowledge` schema 包含 documents、slug aliases、members、projections、attachments、scan jobs、outbox 和 idempotency keys。

- metadata、content 和 permission 使用独立 revision。
- 写操作通过 revision/`If-Match` 提供乐观并发控制。
- 文档软删除后进入保留期；worker 依次清理 Collaboration 数据、S3 对象和 Knowledge 记录。
- 领域变更与 outbox 在同一数据库事务内落地。outbox 对 NATS 提供 at-least-once 发布，消费者必须按事件 ID/业务 revision 幂等处理。
- 附件完成上传后进入扫描队列；ClamAV 不可用时有界退避，污染或类型不匹配对象进入 rejected 并异步删除，只有 ready 对象可下载。

### 7.3 Collaboration

Collaboration 使用显式 schema migration，保存 document heads、updates、snapshots、versions、projection jobs 和 idempotency keys。

- update sequence 单调递增，快照与 compaction 不改变可恢复语义。
- 手工/自动版本保存不可变 Yjs state；恢复要求 expected sequence，避免覆盖并发更新。
- 投影 worker 把当前 Yjs 文档转换为受限 rich-text JSON 和 plain text，再调用 Knowledge；失败按有界重试处理。
- `knowledge.permissions.changed` 和 `collaboration.documents.invalidated` 只用于失效通知，不替代 PostgreSQL 持久化。

## 8. 配置、Secret 与 TLS

Go 服务分别使用 `IDENTITY_`、`KNOWLEDGE_`、`GATEWAY_` 环境变量前缀。配置优先级为：

```text
默认值 < 严格 YAML < 环境变量
```

每个命令持有独立 Cobra flag set 和 Viper 实例，YAML 严格拒绝未知字段。配置文件只保存非敏感值；password、DSN、JWT key、对象存储凭据、NATS/Redis 认证材料只能由环境变量或部署平台 Secret manager 注入。

Collaboration 完全通过 `COLLABORATION_*` 环境变量配置，数值、URL、Origin、认证组合和 TLS 文件在启动前严格校验。

代码当前强制的生产约束包括：

- Gateway 的公开 base URL 必须与 public listener TLS 一致；production 的 Collaboration internal client 必须使用验证开启的 mTLS，公开协作地址必须为 `wss`。
- Knowledge 的非 development 环境要求 internal HTTP mTLS、HTTPS Collaboration origin，并禁止自动创建对象存储 bucket。
- Collaboration production 要求 `wss`、非空精确 Origin、internal HTTP mTLS、PostgreSQL 验证 TLS、`rediss`、到 Knowledge 的 mTLS 和 NATS TLS。

公共 option 还支持 Go 服务的 PostgreSQL、Redis、Etcd、NATS、Kitex、Hertz 和 OTLP TLS。生产部署必须为实际网络边界启用 TLS/mTLS；尚未由环境模式强制的传输不能因为默认 development 配置可启动就视为生产安全。

## 9. 输入安全、错误和观测

Gateway 中间件顺序为 tracing、metrics、依赖注入、panic recovery、access log、安全头、CORS、全局限流和可选认证；Studio 路由再施加强制认证。可信代理 CIDR 为空时不解析转发地址，只有直接对端属于配置 CIDR 时才接受代理链。

Redis 固定窗口限流默认全局 `300/min`，注册/登录额外 `20/min`。Redis 或身份复核异常时失败关闭。

Go 服务日志统一使用 `log/slog` JSON 和 `pkg/log` 脱敏，trace 使用 W3C TraceContext/Baggage。Hertz、Kitex、GORM、Redis 与 NATS 从入口 context 延续 deadline、request ID 和 trace；SQL 参数、Redis key、Authorization、Cookie、payload 和原始错误文本不得进入日志或 metric label。

Identity、Knowledge 和 Gateway 各自持有独立 Prometheus registry，并在 admin listener 的 `/metrics` 暴露。标签只使用路由模板、RPC 方法、状态码、稳定业务码和依赖名等低基数字段。Collaboration 当前提供 JSON 日志和健康端点，尚未接入公共 Go 指标/追踪实现。

## 10. 生命周期与 readiness

Go 长运行组件实现 `Name`、`Serve`、`Ready(context.Context)` 和 `Shutdown(context.Context)`。Runtime 并发启动组件，只有 listener、Kitex Etcd 注册和 worker readiness 均完成后才把进程设为 serving。

服务 readiness 证明必要依赖可用：

- Identity：PostgreSQL、Redis、Etcd。
- Knowledge：PostgreSQL、Etcd registry/resolver、NATS、Identity、S3、ClamAV、Collaboration。
- Gateway：Redis、Etcd resolver、Identity、Knowledge、Collaboration。
- Collaboration：PostgreSQL、Knowledge liveness、Redis publisher、NATS。

Collaboration 探测 Knowledge liveness，而 Knowledge 探测 Collaboration readiness，因此没有互相等待的 readiness 闭环。Compose 也只用单向启动依赖；最终可服务状态仍由应用级 readiness 判定。

退出时先把 readiness 设为失败，再按 component 注册逆序停止入口和 worker，等待在途请求，最后按逆序关闭 client、registry、消息、缓存和数据库资源，并 flush telemetry。所有启动、shutdown、cleanup 和 worker operation 都有时间上限。若组件未安全退出，Go Runtime 保留依赖而不是提前关闭底层资源。

Collaboration 收到信号后依次停止 internal listener、WebSocket、workers、NATS、Knowledge client 和 PostgreSQL，并用进程级 timeout 限制整个 shutdown。

## 11. 生成、CI 与交付

IDL 源位于 `idl/http/v1` 和 `idl/rpc/v1`。生成工具固定为 Kitex `v0.16.2`、Hertz `v0.9.7`、thriftgo `0.4.5`；生成文件所有权以 `scripts/generated-files.txt` 为准。Gateway handler 和未列入清单的 route middleware 保持手写/混合所有权。

```text
make generate
make generate-check
go run ./scripts/idlguard compat-git <merge-base> idl
```

`make ci` 执行格式、vet、golangci-lint、无缓存测试、build、govulncheck 和生成漂移检查。远端 `.github/ci/run.sh` 还在 rootless Docker 中执行 Go race test，并在独立 Node 24 容器执行 Collaboration format check、ESLint、typecheck、Vitest、build 和 `npm audit --audit-level=high`。

`dev` push 只有通过容器化 verify 后才由 GitHub Actions 创建或复用 `dev -> main` PR，并以 merge commit 推进 `main`。

### 当前不兼容基线的迁移记录

本次契约重构已明确批准不保留向后兼容层。相对基线 `0372116`，compat guard 预期失败，主要变化是认证路由改为 users/sessions 资源模型、成功响应移除 envelope、文档 ID 改为 UUID、Unix 秒时间改为 RFC3339、通用 document operation/status 方法改为明确的 CRUD/publication/member/version/attachment 端点。

迁移时必须重新生成所有 HTTP/RPC client，并让 Gateway、Identity、Knowledge、Collaboration 与调用方在同一发布窗口切换；旧 client 不得继续调用新服务。存在旧契约数据的环境应在切换前完成一次性数据转换或清空非生产数据，不做 dual-read、dual-write 或兼容代理。发布验证必须使用本文件第 4、5 节的新契约和强 ETag 语义。

## 12. 当前验证边界

当前单元测试覆盖领域校验、用例、transport 映射、严格 HTTP 输入、JWT、配置、资源关闭和 Collaboration update ACK 顺序等关键行为。仍需明确保留以下边界：

- Identity repository/migration 没有针对真实 PostgreSQL 的自动化集成测试。
- Knowledge repository/migration、事务、约束和 SQL cursor 没有针对真实 PostgreSQL 的自动化集成测试。
- 当前 CI 执行代码和生成门禁，但不构建服务镜像，也不启动完整 Compose 做跨服务行为测试。
- Collaboration 尚无 Prometheus/OpenTelemetry 集成，分布式故障和滚动升级行为未做自动化演练。

因此，当前实现具备明确边界、失败关闭、资源回收和可重复构建基础，但在宣称完整生产就绪前仍需由部署环境完成真实数据库兼容性、完整链路、证书轮换、备份恢复、容量和故障演练验证。
