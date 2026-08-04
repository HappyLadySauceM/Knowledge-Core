# Knowledge Core 框架设计

> 状态：当前代码实现基线，更新于 2026-08-04。系统由 Gateway、Identity、Knowledge 和 Rust Collaboration 四个服务组成；Rust 切换尚未在生产环境执行，性能、完整链路和切换/回滚演练仍是发布前门禁。Identity 与 Knowledge 的真实 PostgreSQL 集成测试仍未纳入当前门禁。
>
> Rust Collaboration 的设计决策、实现状态和未完成验证见 [`rust-collaboration-design.md`](rust-collaboration-design.md)，破坏性切换边界见 [`migrations/2026-08-rust-collaboration.md`](migrations/2026-08-rust-collaboration.md)。

## 1. 系统目标与边界

Knowledge Core 是一个包含 Go module 与 Rust workspace 的 Monorepo。公网 HTTP 使用 Hertz；内部同步 RPC 使用 Thrift，Go 端使用 Kitex、Rust 端使用 Volo；实时协作使用 Axum WebSocket、Yrs 和标准 y-sync。关系数据按服务 schema 隔离，附件存入 S3 兼容对象存储。

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
| Gateway | public `:8080`，admin `:8082` | 无业务数据库 | Redis、Etcd resolver、Identity RPC、Knowledge RPC、Collaboration RPC |
| Identity | RPC `:8881`，admin `:8081` | PostgreSQL `identity` schema | PostgreSQL、Redis、Etcd registry |
| Knowledge | RPC `:8882`，admin `:8083` | PostgreSQL `knowledge` schema、S3 对象 | PostgreSQL、Etcd registry/resolver、NATS JetStream、Identity RPC、S3、ClamAV、Collaboration RPC |
| Collaboration | WebSocket `:8091`，RPC `:8883`，admin `:8084` | PostgreSQL `collaboration` schema 中的 Yjs update、snapshot、version、projection job 和 outbox | PostgreSQL、Redis、NATS JetStream、Etcd registry/resolver、Knowledge RPC |

所有权规则如下：

- Gateway 是 HTTP edge，不持久化领域数据，不直连 Identity、Knowledge 或 Collaboration 的数据库。
- Identity 只拥有用户、凭据状态和 token version。其他服务通过 Identity RPC 获取当前用户或解析成员用户名。
- Knowledge 拥有文档元数据、成员、公开投影、附件状态、幂等记录和 outbox，不保存 Yjs 二进制状态或版本。
- Collaboration 拥有协作状态和版本，不复制文档权限规则。创建 session 时通过 Knowledge RPC 复核访问级别；短期单次 ticket 被消费后建立有明确到期时间的连接。
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
  Cargo.toml          Collaboration package与独立 Rust workspace
  Cargo.lock/deny.toml/rust-toolchain.toml
  src/actor.rs        文档 actor、持久化后应用与广播
  src/websocket.rs    Axum WebSocket、y-sync 与 awareness
  src/rpc/            Volo server/client、Etcd 与 mTLS
  src/storage/        SQLx PostgreSQL repository 与 migration
  src/worker.rs       投影、快照、自动版本、outbox 与失效订阅
  interop/            最小 Node/Yjs/y-prosemirror 互操作 fixture
  tools/rust-codegen/ Collaboration Thrift/Volo 生成工具
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
  NATS JetStream + S3 + ClamAV + Collaboration Kitex client -> workers
  Kitex RPC + Hertz admin

Gateway
  Redis limiter + Etcd resolver
  -> Identity/Knowledge/Collaboration typed Kitex clients
  -> JWT verifier + HTTP middleware/handlers
  -> Hertz public + Hertz admin

Collaboration
  SQLx migration + Redis ticket store + NATS JetStream + Etcd
  -> Knowledge Volo client + document actors
  -> Axum WebSocket/admin + Volo RPC + version/projection/outbox workers
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
| 协作会话 | `POST /api/v1/studio/documents/:document_id/collaboration-sessions` |
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

Identity RPC 提供 `Register`、`Authenticate`、`GetCurrentUser` 和 `ResolveUser`。Gateway 只调用生成的 typed client，不复制领域规则。

Knowledge RPC 除文档、成员和附件用例外，还提供：

- `AuthorizeCollaboration`：从可信 metadata 中取得 access token，返回 actor、`viewer|editor|owner`、permission revision 和 token expiry。
- `ProjectCollaboration`：按 document generation 与单调 sequence 更新公开 rich-text 投影。
- `Ping`：返回 readiness；会检查 Knowledge 的必要依赖，包括 Collaboration。
- `Live`：只证明 Knowledge RPC 进程存活，不读取 readiness。Collaboration 启动和 supervisor 使用该方法，避免 Knowledge readiness 与 Collaboration readiness 互相等待。

Collaboration RPC 提供 `Ping`、`CreateSession`、版本列表/创建/详情/恢复和 `PurgeDocument`。除 `Ping` 外的六个业务 RPC 都先检查完整应用 readiness；not-ready 时统一返回 `40007 / collaboration.unavailable`，且不会调用 Knowledge、ticket、store 或 actor。Gateway 通过 `CreateSession` 获得短期 ticket；Knowledge 的清理 worker 通过 `PurgeDocument` 删除协作数据。生产 RPC 双向验证 mTLS，并通过 TTHeader 传播 deadline、request ID、W3C trace 和必要的敏感 token metadata；token 永不进入日志或 telemetry。

WebSocket 入口固定为 `/v1/documents/{document_id}`。客户端先调用 Gateway session API，再通过 `Sec-WebSocket-Protocol: knowledge-core-yjs-v1, ticket.<opaque>` 传递一次性 ticket；ticket 只以 SHA-256 key 存入 Redis，并用 `GETDEL` 原子消费。握手严格校验 UUIDv7、精确 Origin、协议、容量与速率边界。viewer 只读，session 到期后客户端重新创建 session；不提供旧 Hocuspocus token refresh 扩展。

每个活跃文档由单一 actor 串行拥有 Yrs state、sequence、generation、连接集合和有界 command queue。客户端 update 先在候选 state 中校验大小和 rich-text schema，再由 PostgreSQL transaction 写入 update、projection job 与 outbox；只有 commit 成功后才更新共享内存并广播。重复且不改变 state 的 update 不推进 sequence。远端实例通过 JetStream fanout 接收已提交 update，发现 gap 时从 PostgreSQL 补齐；PostgreSQL 始终是持久事实源。

权限事件携带正的 permission revision，只以 `4403` 关闭 revision 更旧的连接；相同或更旧的重复事件不影响新授权连接。permission subject 独占只按 24 小时 max age 清理、没有 size eviction 的 stream；新 durable 使用 `DeliverPolicy::All` 回放全部保留历史。创建 consumer 后以 permission stream `last_sequence` 固定启动目标，只有服务端连续 ACK floor 的 stream sequence 越过目标才允许 ready；若目标消息在投递前因时间保留收缩而全部消失，则必须同时观察到 `num_pending=0` 和 `num_ack_pending=0`。每个实例保留有界 revision watermark，覆盖事件先于 actor/ticket 消费到达及 actor 回收后的短期竞态。文档失效仍以 `4409` 关闭整个 actor。失效订阅解析、路由或 ACK 失败时不确认消息并使进程 not-ready；当前实现 fail closed 后由外部编排重启，不在进程内无限自愈。

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
- 领域变更与 outbox 在同一数据库事务内落地。worker 使用 JetStream server PubAck 后才标记 published，并以 outbox message ID 作为 deduplication ID；stream 缺失或发布失败会记录 retry，不能把 Core NATS 接收当成持久化成功。消费者仍必须按事件 ID/业务 revision 幂等处理。
- 附件完成上传后进入扫描队列；ClamAV 不可用时有界退避，污染或类型不匹配对象进入 rejected 并异步删除，只有 ready 对象可下载。

### 7.3 Collaboration

Collaboration 使用显式 SQLx schema migration，保存 document heads、updates、snapshots、versions、projection jobs、idempotency keys 和 outbox。首次 migration 遇到未受本 migration 历史管理的旧 Node schema 时拒绝启动，不能误记为成功。

- update sequence 单调递增，快照与 compaction 不改变可恢复语义。
- 手工/自动版本保存不可变 Yjs state；恢复要求 expected sequence，在 actor 内生成恢复 update，并在单一事务中提交 baseline/update/version/head/projection/idempotency/outbox，避免覆盖并发更新或产生部分状态。
- 投影 worker 把当前 Yjs 文档转换为受限 rich-text JSON 和 plain text，再调用 Knowledge；失败按有界重试处理。
- `knowledge.permissions.changed` 和 `collaboration.documents.invalidated` 只用于失效通知，不替代 PostgreSQL 持久化。
- Collaboration 是两套 JetStream stream contract 的 owner。默认 `KNOWLEDGE_CORE_EVENTS` 只包含 `collaboration.documents.updated` 与 `collaboration.documents.invalidated`，使用 24 小时 max age、1 GiB max bytes；默认 `KNOWLEDGE_CORE_PERMISSIONS` 只包含 `knowledge.permissions.changed`，使用 24 小时 max age 且 `max_bytes=-1`，避免容量驱逐破坏 ticket TTL 覆盖。两者名称必须不同，并共同要求 Limits retention、File storage、DiscardOld、24 小时 duplicate window 与 1 MiB max message；subject 或已有 stream 配置漂移时拒绝 ready。每个副本使用由唯一且重启稳定的 `COLLABORATION_INSTANCE_ID` 派生的 durable consumer，确保 fanout 与未 ACK redelivery。

## 8. 配置、Secret 与 TLS

Go 服务分别使用 `IDENTITY_`、`KNOWLEDGE_`、`GATEWAY_` 环境变量前缀。配置优先级为：

```text
默认值 < 严格 YAML < 环境变量
```

每个命令持有独立 Cobra flag set 和 Viper 实例，YAML 严格拒绝未知字段。配置文件只保存非敏感值；password、DSN、JWT key、对象存储凭据、NATS/Redis 认证材料只能由环境变量或部署平台 Secret manager 注入。

Collaboration 完全通过 `COLLABORATION_*` 环境变量配置，数值、URL、Origin、认证组合和 TLS 文件在启动前严格校验。

四个服务可通过 `KNOWLEDGE_CORE_NACOS_*` 启用 Nacos `3.2.3` 动态配置。bootstrap 严格要求 HTTPS endpoint、namespace/group/data ID、每服务 reader 凭据、CA 文件、key ID 和 32-byte KEK；Go 与 Rust 使用同一 AES-256-GCM 信封和坐标 AAD。初始配置获取失败会阻断启动，运行中失联保留 last-good 并告警，恢复后继续监听/轮询。当前动态文档只实现原子日志级别更新，依赖池和监听地址仍由静态 YAML/环境变量装配，不得描述为已经支持热换。

k3s 中 Nacos 部署在 `config-system`，HTTP/gRPC 使用现有 `happyladysauce-ca` 签发的 TLS 证书，并复用已有 PostgreSQL 服务中的独立 `nacos` database。test/prod 使用不同 Nacos namespace，每个服务只读自己的 `<service>.dynamic.yaml`。应用 namespace 通过只含公共证书的 ConfigMap 挂载 CA；TLS 私钥不跨 namespace 复制。

代码当前强制的生产约束包括：

- Gateway 的公开 base URL 必须与 public listener TLS 一致；production 的 Collaboration RPC client 必须使用验证开启的双向 mTLS，公开协作地址必须为 `wss`。
- Knowledge 的非 development 环境要求 Collaboration RPC 双向 mTLS，并禁止自动创建对象存储 bucket。
- Collaboration production 要求非空精确 Origin、RPC 双向 mTLS、PostgreSQL 验证 TLS、`rediss`、Knowledge RPC 双向 mTLS，以及 NATS 与 Etcd TLS。公开 WebSocket TLS 可由服务或明确信任的 ingress 终止。

公共 option 还支持 Go 服务的 PostgreSQL、Redis、Etcd、NATS、Kitex、Hertz 和 OTLP TLS。生产部署必须为实际网络边界启用 TLS/mTLS；尚未由环境模式强制的传输不能因为默认 development 配置可启动就视为生产安全。

## 9. 输入安全、错误和观测

Gateway 中间件顺序为 tracing、metrics、依赖注入、panic recovery、access log、安全头、CORS、全局限流和可选认证；Studio 路由再施加强制认证。可信代理 CIDR 为空时不解析转发地址，只有直接对端属于配置 CIDR 时才接受代理链。

Redis 固定窗口限流默认全局 `300/min`，注册/登录额外 `20/min`。Redis 或身份复核异常时失败关闭。

Go 服务日志统一使用 `log/slog` JSON 和 `pkg/log` 脱敏，trace 使用 W3C TraceContext/Baggage。Hertz、Kitex、GORM、Redis 与 NATS 从入口 context 延续 deadline、request ID 和 trace；SQL 参数、Redis key、Authorization、Cookie、payload 和原始错误文本不得进入日志或 metric label。

四个服务各自持有独立 Prometheus registry，并在 admin listener 的 `/metrics` 暴露。标签只使用路由模板、RPC 方法、状态码、稳定业务码和依赖名等低基数字段。Rust Collaboration 使用 `tracing` JSON、W3C TraceContext/Baggage 与可选 OTLP exporter；WebSocket、Volo、SQLx、Redis、Etcd 和 NATS 延续 request context，且不记录 ticket、token、payload、DSN 或用户/文档 ID metric label。

## 10. 生命周期与 readiness

Go 长运行组件实现 `Name`、`Serve`、`Ready(context.Context)` 和 `Shutdown(context.Context)`。Runtime 并发启动组件，只有 listener、Kitex Etcd 注册和 worker readiness 均完成后才把进程设为 serving。

服务 readiness 证明必要依赖可用：

- Identity：PostgreSQL、Redis、Etcd。
- Knowledge：PostgreSQL、Etcd registry/resolver、NATS、Identity、S3、ClamAV、Collaboration。
- Gateway：Redis、Etcd resolver、Identity、Knowledge、Collaboration。
- Collaboration：PostgreSQL、Knowledge `Live`、Redis ticket backend、NATS JetStream、Etcd discovery/registration、actor/workers 和三个 listener。

Collaboration 探测 Knowledge `Live`，而 Knowledge 使用 Collaboration `Ping` 检查 readiness，因此没有互相等待的 readiness 闭环。Collaboration `Ping` 与 admin ready 共用完整应用 `HealthState`，启动中或任一受监督依赖/listener 失败时返回 `not_ready`，不能只凭 Etcd 已注册判断。RPC serve task 正常返回、报错、panic 或被 abort 都会被标记为 stopped；非计划退出会立即停止 WebSocket 接入并触发进程失败。Etcd registration 除 keepalive 外还周期校验 key、value 与 lease 所有权，外部删除或覆盖会 fail closed；resolver 对注册记录中的严格 `host:port` 在 snapshot 总 deadline 内解析，非法地址、DNS 失败、空结果或超时同样 fail closed。Gateway 仍使用双方的 `Ping`。Compose 只约束依赖进程启动顺序；最终可服务状态由应用级 readiness 判定。

退出时先把 readiness 设为失败，再按 component 注册逆序停止入口和 worker，等待在途请求，最后按逆序关闭 client、registry、消息、缓存和数据库资源，并 flush telemetry。所有启动、shutdown、cleanup 和 worker operation 都有时间上限。若组件未安全退出，Go Runtime 保留依赖而不是提前关闭底层资源。

Collaboration 收到信号后先 not-ready 并撤销 Etcd 注册，再停止 RPC 与 WebSocket 接入、关闭连接和 actor、停止 worker/subscription，最后关闭 Knowledge client、Etcd、NATS、Redis、PostgreSQL 并 flush telemetry；进程级 timeout 限制整个 shutdown。NATS subscription 意外结束会 fail closed，由外部编排重启进程。

## 11. 生成、CI 与交付

IDL 源位于 `idl/http/v1` 和 `idl/rpc/v1`。生成工具固定为 Kitex `v0.16.2`、Hertz `v0.9.7`、thriftgo `0.4.5`；生成文件所有权以 `scripts/generated-files.txt` 为准。Gateway handler 和未列入清单的 route middleware 保持手写/混合所有权。

```text
make generate
make generate-check
go run ./scripts/idlguard compat-git <merge-base> idl
```

`make ci` 同时执行 Go/Rust format、vet、golangci-lint、Clippy `-D warnings`、无缓存测试、release build、govulncheck、cargo-deny 和 Go/Rust 生成漂移检查。远端 `.github/ci/run.sh` 还在 rootless Docker 中执行 Go race、真实 PostgreSQL/Redis/NATS/Etcd 测试、双向 Kitex/Volo 与 Yjs 互操作，并构建 Rust production image，验证固定运行 UID/GID `10001:10001`、无 Node/npm 和非法配置 fail-fast。真实依赖阶段设置 `COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1`；缺少 PostgreSQL、Redis、NATS 或 Etcd 对应连接变量时测试必须失败，不能静默 skip。Node 24 只运行 `services/collaboration/interop` fixture。

`dev` push 只有通过容器化 verify 后才由 GitHub Actions 创建或复用 `dev -> main` PR，并以 merge commit 推进 `main`。

Kubernetes 所有权分为三层：`/opt/k3s/knowledge-core-platform` 是共享基础设施声明源；应用仓库 `deploy/base` 是环境无关工作负载真源；私有 `k3s-home-deploy` 保存 CI 同步的 base 快照以及 test/prod overlay、SOPS Secret 和镜像 digest。共享平台复用已有 PostgreSQL 与 Redis，只新增 NATS、业务 Etcd、MinIO、ClamAV 和 Nacos。应用 base 不包含 namespace、Secret 或部署 digest；每个服务自己的 Secret 同时持有其 Nacos reader 凭据和 KEK。

仓库内已经存在 Argo Workflows/Events、rootless BuildKit、Trivy/Syft/Cosign 和 GitOps CAS 推广清单，但它们在 GitHub App、Harbor/Cosign、SOPS/age Secret、固定 CI 镜像 digest 和首个真实 workflow 完成前仍属于未激活交付资产。self-hosted `.github/ci/run.sh` 继续作为回退；Argo CD Application 由管理面板创建。

### 当前不兼容基线的迁移记录

本次契约重构已明确批准不保留向后兼容层。相对基线 `0372116`，compat guard 预期失败，主要变化是认证路由改为 users/sessions 资源模型、成功响应移除 envelope、文档 ID 改为 UUID、Unix 秒时间改为 RFC3339、通用 document operation/status 方法改为明确的 CRUD/publication/member/version/attachment 端点。

迁移时必须重新生成所有 HTTP/RPC client，并让 Gateway、Identity、Knowledge、Collaboration 与调用方在同一发布窗口切换；旧 client 不得继续调用新服务。存在旧契约数据的环境应在切换前完成一次性数据转换或清空非生产数据，不做 dual-read、dual-write 或兼容代理。发布验证必须使用本文件第 4、5 节的新契约和强 ETag 语义。

## 12. 当前验证边界

当前自动化测试覆盖领域校验、用例、transport 映射、严格 HTTP 输入、JWT、配置、资源关闭，以及 Collaboration 的 commit-before-broadcast、actor 恢复、重复 update、版本恢复、投影/outbox、真实 PostgreSQL/Redis/NATS/Etcd、双向 Kitex/Volo mTLS/metadata、Yjs fixture 和多实例 JetStream fanout/redelivery。仍需明确保留以下边界：

- Identity repository/migration 没有针对真实 PostgreSQL 的自动化集成测试。
- Knowledge repository/migration、事务、约束和 SQL cursor 没有针对真实 PostgreSQL 的自动化集成测试。
- 当前 CI 构建并 smoke Rust Collaboration 镜像，但不启动完整 Compose 执行跨服务 WebSocket E2E。
- k3s 原生 CI 尚未以真实 GitHub webhook 完成一次从状态上报、构建、签名到 GitOps test digest 更新的端到端运行。
- SOPS age 私钥尚未生成和离线备份，因此 GitOps Secret 与 Nacos 加密动态文档尚未创建；平台 `apply.sh` 也尚未执行。
- prod overlay 保持 Collaboration `production` 校验，但现有共享 PostgreSQL/Redis 与新增 NATS/Etcd 尚未形成完整的验证 TLS/mTLS 链路，prod Application 当前不能激活。
- Node/Rust 性能基线、PostgreSQL/NATS stop/start 故障演练、备份恢复、切换/回滚和滚动升级尚未完成。
- 生产镜像最终 digest 只能在发布构建后记录，仓库内门禁不能替代部署环境证书、容量和网络策略验证。

因此，Rust 方案已形成可构建、可测试的当前代码实现，但尚不能据此宣称已完成生产切换或完整生产级验收。发布前仍需完成完整链路、性能、证书轮换、备份恢复、容量、故障与切换/回滚演练；Identity/Knowledge 的真实数据库验证继续作为单独缺口保留。
