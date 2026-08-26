# Knowledge Core 框架设计

> 状态：当前代码实现基线，更新于 2026-08-23。系统由 Gateway、Identity、Knowledge、Attachment、Platform 和 Rust Collaboration 六个服务组成；Rust 切换尚未在生产环境执行，性能、完整链路和切换/回滚演练仍是发布前门禁。Identity、Knowledge 与 Platform 的真实 PostgreSQL 集成测试仍未纳入当前门禁。
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
- 管理员实时写入站点、邮件和 AI 配置；Platform 对修订、密文、审计和可靠变更事件负责。

当前明确不提供：

- JWKS、JWT 在线密钥轮换和多区域会话复制。
- 跨服务数据库事务、exactly-once 消息或同步的全局强一致性。
- 连接、证书、进程拓扑等基础设施配置的热重建；这类变化仍通过滚动重启生效。
- 邮件和 AI 业务服务的配置事件消费者；Platform 已可靠发布变更事件，但尚未让这些消费者热加载密钥快照。
- 任意服务共享数据库模型或绕过公开/内部契约直连其他服务的 schema。

## 2. 服务与数据所有权

| 服务 | 入口 | 拥有的数据 | 必需依赖 |
| --- | --- | --- | --- |
| Gateway | public `:8080`，admin `:8082` | 无业务数据库 | Redis、Identity RPC、Knowledge RPC、Collaboration RPC、Attachment RPC、Platform RPC |
| Identity | RPC `:8881`，admin `:8081` | PostgreSQL `identity` schema | PostgreSQL、Redis |
| Knowledge | RPC `:8882`，admin `:8083` | PostgreSQL `knowledge` schema 中的文档、成员、公开投影和 outbox；文档附件进入兼容迁移窗口 | PostgreSQL、NATS JetStream、Identity RPC、Collaboration RPC、Attachment RPC |
| Attachment | RPC `:8884`，admin `:8085` | PostgreSQL `attachment` schema 中的通用附件、multipart 状态、扫描任务和引用 | PostgreSQL、MinIO、ClamAV |
| Collaboration | WebSocket `:8091`，RPC `:8883`，admin `:8084` | PostgreSQL `collaboration` schema 中的 Yjs update、snapshot、version、projection job 和 outbox | PostgreSQL、Redis、NATS JetStream、Knowledge RPC |
| Platform | RPC `:8885`，admin `:8086` | PostgreSQL `platform` schema 中的配置快照、审计、幂等记录和 outbox | PostgreSQL、NATS JetStream |

所有权规则如下：

- Gateway 是 HTTP edge，不持久化领域数据，不直连 Identity、Knowledge 或 Collaboration 的数据库。
- Identity 只拥有用户、凭据状态和 token version。其他服务通过 Identity RPC 获取当前用户或解析成员用户名。
- Knowledge 拥有文档元数据、成员、公开投影、幂等记录和 outbox，不保存 Yjs 二进制状态或版本；图片、视频、文件和压缩包由 Attachment 统一拥有，旧文档附件接口仅保留兼容窗口。
- Collaboration 拥有协作状态和版本，不复制文档权限规则。创建 session 时通过 Knowledge RPC 复核访问级别；短期单次 ticket 被消费后建立有明确到期时间的连接。
- Platform 拥有网页管理员可写的业务配置。Gateway 只提供 HTTP façade；其他服务不得直写 `platform` schema。Nacos 继续拥有进程启动和基础设施连接配置，两类配置不互相覆盖。
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
  postgres/redis/nats/
  transport/    Hertz、Kitex 的公共传输适配

services/<go-service>/
  main.go       只创建并执行应用命令
  spec.go       只声明应用规格
  etc/config.yaml  服务私有的非敏感静态配置
  internal/config/
  internal/context/
  internal/domain|logic|repository|transport/

services/collaboration/
  Cargo.toml          Collaboration package与独立 Rust workspace
  Cargo.lock/deny.toml/rust-toolchain.toml
  src/actor.rs        文档 actor、持久化后应用与广播
  src/websocket.rs    Axum WebSocket、y-sync 与 awareness
  src/rpc/            Volo server/client、静态 DNS 发现与 mTLS
  src/storage/        SQLx PostgreSQL repository 与 migration
  src/worker.rs       投影、快照、自动版本、outbox 与失效订阅
  interop/            最小 Node/Yjs/y-prosemirror 互操作 fixture
  tools/rust-codegen/ Collaboration Thrift/Volo 生成工具

deploy/<service>/
  base/               服务私有 Deployment/StatefulSet、Service 与 Kustomization
  overlay/dev/        仅含服务行为 ConfigMap 与 dev Kustomization
```

## 3. 依赖装配

Go 服务使用 `pkg/app.Spec[C]` 注册配置、公共 runtime options 和 `ServiceContext`。`Runtime` 只持有进程级日志、追踪、健康、指标、component 和 cleanup，不是服务定位器。业务资源由各服务的 `NewServiceContext` 显式构造，构造函数校验必需依赖并返回错误。

装配链如下：

```text
Identity
  PostgreSQL -> migration -> UserRepository -> register/authenticate/get-user logic
  Redis -> Kitex RPC + Hertz admin

Knowledge
  PostgreSQL -> versioned SQL migration -> Store -> document/member/attachment logic
  Identity Kitex client (static host:port)
  NATS JetStream + S3 + ClamAV + Collaboration Kitex client -> workers
  Kitex RPC + Hertz admin

Gateway
  Redis limiter
  -> Identity/Knowledge/Collaboration/Attachment/Platform typed Kitex clients (static host:port)
  -> JWT verifier + HTTP middleware/handlers
  -> Hertz public + Hertz admin

Platform
  PostgreSQL -> versioned SQL migration -> configuration Store
  -> revision/audit/idempotency/outbox transaction -> JetStream publisher
  -> Kitex RPC + Hertz admin

Collaboration
  SQLx migration + Redis ticket store + NATS JetStream
  -> Knowledge Volo client (static host:port) + document actors
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
- `Ping`：返回 Knowledge 本进程 readiness（PostgreSQL、NATS、S3、ClamAV），不探活 Identity 或 Collaboration。
- `Live`：只证明 Knowledge RPC 进程存活，不读取 readiness。Collaboration 不再把它当作启动或 supervisor 的 Ready 门闩。

Collaboration RPC 提供 `Ping`、`CreateSession`、版本列表/创建/详情/恢复和 `PurgeDocument`。除 `Ping` 外的六个业务 RPC 都先检查完整应用 readiness；not-ready 时统一返回 `40007 / collaboration.unavailable`，且不会调用 Knowledge、ticket、store 或 actor。Gateway 通过 `CreateSession` 获得短期 ticket；Knowledge 的清理 worker 通过 `PurgeDocument` 删除协作数据。生产 RPC 双向验证 mTLS，并通过 TTHeader 传播 deadline、request ID、W3C trace 和必要的敏感 token metadata；token 永不进入日志或 telemetry。

内部 RPC 客户端使用静态 `host:port`，由系统 DNS 解析：k3s 使用 ClusterIP Service FQDN，Compose/CI 使用 Docker 服务名。Go Kitex 通过 `WithHostPorts` 拨号；Collaboration 的 Volo Knowledge 客户端用 `StaticDiscover` 在每次 discover 时解析同一地址，非法 `host:port`、DNS 失败、空结果或超时均 fail closed，且不 watch Kubernetes EndpointSlice。进程不再向注册中心报名。Collaboration RPC 走 ClusterIP；WebSocket 仍走 per-pod Service 与 Higress `/v1/instances/{n}/`。生产环境拒绝 `localhost` 与环回拨号地址。

WebSocket 入口为 `/v1/instances/{ordinal}/documents/{document_id}`。`COLLABORATION_INSTANCE_COUNT=1` 时进程额外接受 `/v1/documents/{document_id}`，供 Compose 单实例直连。CreateSession 按 `document_id` 的 SHA-256 桶与 Redis 粘性映射分配实例；Gateway 用 RPC 返回的 `instance_ordinal` 和已校验的 WebSocket base URL 构造 `websocket_url`，不得使用请求 `Host`。握手必须先核对路径 ordinal 与本机一致，再 `GETDEL` ticket；错实例返回 HTTP 404 且不消费 ticket。客户端先调用 Gateway session API，再通过 `Sec-WebSocket-Protocol: knowledge-core-yjs-v1, ticket.<opaque>` 传递一次性 ticket。握手还校验 UUIDv7、精确 Origin、协议、容量与速率边界。viewer 只读，session 到期后客户端重新创建 session；不提供旧 Hocuspocus token refresh 扩展。

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

Identity 启动时在 PostgreSQL advisory transaction lock 内创建/校验 `identity` schema，执行增量 GORM migration，并补齐大小写不敏感唯一索引、命名 check constraint、会话刷新密文和邮件 outbox 字段。已有等价约束不会被重复删除；所有 repository 查询使用 `db.WithContext(ctx)`。

注册、邮箱验证、密码重置和账户停用的用户状态、一次性令牌、会话撤销及邮件 outbox 写入在 Identity 数据库内保持事务边界。Refresh Token 严格轮换，并对并发请求提供 10 秒前序令牌宽限；宽限外复用会撤销会话。

AutoMigrate 只适用于当前增量阶段，不替代破坏性 schema 演进的 expand/migrate/contract 流程。

### 7.2 Knowledge

Knowledge 使用带版本记录的显式 SQL migration。`knowledge` schema 包含 documents、slug aliases、members、projections、attachments、scan jobs、outbox 和 idempotency keys。

- metadata、content 和 permission 使用独立 revision。
- 写操作通过 revision/`If-Match` 提供乐观并发控制。
- 文档子串搜索先用 `UNION` 从 metadata 与 projection trigram GIN 索引生成去重 document ID，再应用权限、状态、稳定游标排序和最终宽行读取。
- 文档软删除后进入保留期；worker 依次清理 Collaboration 数据、S3 对象和 Knowledge 记录。
- outbox 与领域变更同一事务保存受限 W3C propagation headers；发布失败最多退避 8 次，随后进入 parked 状态，保留 message ID 供人工 redrive。消费者按事件 ID/业务 revision 幂等处理。
- 领域变更与 outbox 在同一数据库事务内落地，并同事务 `NOTIFY knowledge_workers`（payload `outbox` / `attachment`）唤醒后台 worker；`workers.poll_interval`（默认 30s）只做到期重试、过期上传、purge/maintenance 与 LISTEN 断线补偿。worker 使用 JetStream server PubAck 后才标记 published，并以 outbox message ID 作为 deduplication ID；stream 缺失或发布失败会记录 retry，不能把 Core NATS 接收当成持久化成功。消费者仍必须按事件 ID/业务 revision 幂等处理。
- 附件创建在文档 row lock 之外还按 uploader 获取 transaction advisory lock，使跨文档的用户配额检查与插入串行化；完成上传后进入按 next-attempt 稳定排序的扫描队列。ClamAV 不可用时有界退避，污染或类型不匹配对象进入 rejected 并异步删除，只有 ready 对象可下载。

### 7.3 Collaboration

Collaboration 使用按版本有序执行并逐项校验 checksum 的显式 SQLx schema migration，保存 document heads、updates、snapshots、versions、projection jobs、idempotency keys 和 outbox。首次 migration 遇到未受本 migration 历史管理的旧 Node schema 时拒绝启动，不能误记为成功；已有 schema 只追加尚未记录的新版本，已应用 migration 的名称或内容漂移会拒绝启动。

- update sequence 单调递增，快照与 compaction 不改变可恢复语义。
- compaction 候选通过单个 lateral aggregate 同时计算待压缩 update 数量和字节数，选中后仍在 document row lock 下重新检查阈值。
- 手工/自动版本保存不可变 Yjs state；恢复要求 expected sequence，在 actor 内生成恢复 update，并在单一事务中提交 baseline/update/version/head/projection/idempotency/outbox，避免覆盖并发更新或产生部分状态。
- 投影 worker 把当前 Yjs 文档转换为受限 rich-text JSON 和 plain text，再调用 Knowledge；失败按有界重试处理。
- Collaboration outbox 与事件 headers 同事务保存 trace context，发布时透传 W3C headers；JetStream delivery 超过 8 次会停车并保持幂等 event key，避免循环/重试放大 span 数量。
- `knowledge.permissions.changed` 和 `collaboration.documents.invalidated` 只用于失效通知，不替代 PostgreSQL 持久化。
- Collaboration 是两套 JetStream stream contract 的 owner。默认 `KNOWLEDGE_CORE_EVENTS` 只包含 `collaboration.documents.updated` 与 `collaboration.documents.invalidated`，使用 24 小时 max age、1 GiB max bytes；默认 `KNOWLEDGE_CORE_PERMISSIONS` 只包含 `knowledge.permissions.changed`，使用 24 小时 max age 且 `max_bytes=-1`，避免容量驱逐破坏 ticket TTL 覆盖。两者名称必须不同，并共同要求 Limits retention、File storage、DiscardOld、24 小时 duplicate window 与 1 MiB max message；subject 或已有 stream 配置漂移时拒绝 ready。每个副本使用由唯一且重启稳定的 `COLLABORATION_INSTANCE_ID` 派生的 durable consumer，确保 fanout 与未 ACK redelivery。

## 8. 配置、Secret 与 TLS

Go 服务分别使用 `IDENTITY_`、`KNOWLEDGE_`、`GATEWAY_` 环境变量前缀。配置优先级为：

```text
默认值 < 严格 YAML < Nacos ApplicationConfig < 环境变量
```

每个命令持有独立 Cobra flag set 和 Viper 实例，YAML 严格拒绝未知字段。配置文件只保存非敏感值；password、DSN、JWT key、对象存储凭据、NATS/Redis 认证材料只能由环境变量或部署平台 Secret manager 注入。

Collaboration 的静态基线来自默认值，Nacos 可覆盖非敏感应用字段，`COLLABORATION_*` 环境变量保持最高优先级；数值、URL、Origin、认证组合和 TLS 文件在启动前严格校验。

四个服务可通过 `KNOWLEDGE_CORE_NACOS_*` 启用 Nacos `3.2.3` 动态配置。bootstrap 严格要求 HTTPS endpoint、namespace/group/data ID、每服务 reader 凭据、CA 文件、key ID 和 32-byte KEK；Go 与 Rust 使用同一 AES-256-GCM 信封和坐标 AAD。Rust SDK 的 gRPC 与 native-tls HTTP 路径必须分别通过 `NACOS_CLIENT_TLS_CA_CERT` 和 `SSL_CERT_FILE` 指向同一 CA，且 `HOME/nacos` 必须等于挂载的 `KNOWLEDGE_CORE_NACOS_RUNTIME_DIR`，启动会在连接前校验并创建 SDK cache 目录。初始配置获取失败会阻断启动，运行中失联保留 last-good 并告警，恢复后继续监听/轮询。

Nacos Data ID 继续使用 `<service>.dynamic.yaml`。新格式为 `knowledge-core.io/v1beta1/ApplicationConfig`，包含 `service`、单调递增 `revision` 和严格的 `config` 映射；旧 `v1alpha1/DynamicConfig` 日志级别文档在迁移期仍可读取。每次修订先完整解密、严格解码、服务绑定和配置校验，任一字段无效会整体拒绝并保留 last-good。日志级别与 `log.health_check_requests` 原子更新；关闭后只抑制成功的 `/livez`、`/readyz`、Gateway `/health/live`、`/health/ready` 以及 Go RPC `Ping`/`Live` 完成日志，失败、业务错误、非 2xx 和 panic 仍记录。Gateway 的 Origin/可信代理/限流/公开端点，Identity 的 bcrypt/新 token TTL/登录锁定策略，Knowledge 的预签名 TTL/ClamAV 限制/worker 时序，以及 Collaboration 的 Origin/握手限流/ticket TTL/新 actor 限制可在线更新。监听地址、连接池、凭据、固定容量和 worker 拓扑等启动期变化不重建依赖，而通过 `knowledge_core_config_restart_required`（Rust 为 `knowledge_core_collaboration_config_restart_required`）和有界字段名日志报告需重启；同一修订中的安全字段仍生效。

k3s 中 Nacos 部署在 `nacos` namespace，HTTP/gRPC 使用现有 `happyladysauce-ca` 签发的 TLS 证书，并复用已有 PostgreSQL 服务中的独立 `nacos` database。唯一应用环境使用 Nacos namespace `dev`，每个服务只读自己的 `<service>.dynamic.yaml`。应用 namespace 通过只含公共证书的 ConfigMap 挂载 CA；TLS 私钥不跨 namespace 复制。发布必须使用独立运维 writer 身份，只授予 `dev/KNOWLEDGE_CORE/<service>.dynamic.yaml` 的写权限，禁止复用应用 reader 或 Nacos 管理员。明文模板位于 `deploy/nacos/`，只包含非敏感配置；`configctl validate/encrypt --service <service>` 在发布前校验服务绑定，实际凭据和 KEK 仅从受控 Secret/环境注入。

k3s 的项目 PostgreSQL database 为 `knowledge_core_dev`。Identity、Knowledge、Collaboration 使用独立 role 和各自拥有的 schema；由于当前启动 migration 会幂等执行 `CREATE SCHEMA IF NOT EXISTS`，三个 role 需要该 database 的 `CONNECT,CREATE`，但不获得其他 schema 的对象权限。

代码当前强制的生产约束包括：

- Gateway 的公开 base URL 必须与 public listener TLS 一致；production 的 Collaboration RPC client 必须使用验证开启的双向 mTLS，公开协作地址必须为 `wss`。
- Knowledge 的非 development 环境要求 Collaboration RPC 双向 mTLS，并禁止自动创建对象存储 bucket。
- Collaboration production 要求非空精确 Origin、RPC 双向 mTLS、PostgreSQL 验证 TLS、`rediss`、Knowledge RPC 双向 mTLS，以及 NATS TLS。公开 WebSocket TLS 可由服务或明确信任的 ingress 终止。

公共 option 还支持 Go 服务的 PostgreSQL、Redis、NATS、Kitex、Hertz 和 OTLP TLS。生产部署必须为实际网络边界启用 TLS/mTLS；尚未由环境模式强制的传输不能因为默认 development 配置可启动就视为生产安全。

## 9. 输入安全、错误和观测

Gateway 中间件顺序为 tracing、metrics、依赖注入、panic recovery、access log、安全头、CORS、全局限流和可选认证；Studio 路由再施加强制认证。可信代理 CIDR 为空时不解析转发地址，只有直接对端属于配置 CIDR 时才接受代理链。

Redis 固定窗口限流默认全局 `300/min`，注册/登录额外 `20/min`。Redis 或身份复核异常时失败关闭。

Go 服务日志统一使用 `log/slog` JSON 和 `pkg/log` 脱敏，trace 使用 W3C TraceContext/Baggage。Hertz、Kitex、GORM、Redis 与 NATS 从入口 context 延续 deadline、request ID 和 trace；SQL 参数、Redis key、Authorization、Cookie、payload 和原始错误文本不得进入日志或 metric label。

公开链路以 Higress 的 edge span 开始；Higress 内建 OpenTelemetry tracer，并向 Gateway 注入 W3C `traceparent`/`tracestate`。Gateway 的入口 server span 覆盖 Gateway 响应完成；Kitex/Volo client/server、数据库、缓存和 JetStream producer/consumer 作为同一 W3C trace 的子 span。HTTP 响应不会创建反向 span，闭环由 Higress 和 Gateway 父 span 的结束时间表达。`/metrics`、`/livez`、`/readyz`、`/health/live`、`/health/ready`、RPC `Ping`/`Live` 及 WebSocket ping/pong 完全抑制 trace，异常只通过日志和 metrics 保留。JetStream 重投递不为每次 attempt 创建新 span，而是保留一次逻辑消费 span并记录有限 attempt/parking 事件。collector 端对错误、停车/DLQ 和超过 1 秒的 trace 100% 保留，其余成功 trace 保留 10%；Compose decision wait 为 15 分钟，k3s dev 为 30 秒。

本地 Compose 与 k3s dev 都把 OTel Collector 放在应用/Higress 与 Tempo 之间。Collector 通过 OTLP (`4317/4318`) 接收 trace，过滤、采样后统一以 OTLP 写入 Tempo。Go/Rust 服务只通过环境变量注入 OTLP endpoint，生产 endpoint、TLS 和鉴权由部署平台提供。Gateway CORS 允许 `traceparent`/`tracestate`，但不允许浏览器 `baggage`；W3C baggage 在内部允许完整合法字段但总长度上限为 8 KiB，既不写入日志/span attribute，也不进入业务消息 payload。

六个服务各自持有独立 Prometheus registry，并在 admin listener 的 `/metrics` 暴露。标签只使用路由模板、RPC 方法、状态码、稳定业务码和依赖名等低基数字段。Go 出站 RPC 熔断暴露低基数 `knowledge_core_rpc_client_circuit_state{dependency,state}`，`state` 仅为 `closed`/`open`/`half_open`。Rust Collaboration 使用 `tracing` JSON、W3C TraceContext/Baggage 与可选 OTLP exporter；WebSocket、Volo、SQLx、Redis 和 NATS 延续 request context，且不记录 ticket、token、payload、DSN 或用户/文档 ID metric label。

面向日常排障和交接的链路追踪说明见 [`trace-architecture.md`](trace-architecture.md)。

## 10. 生命周期与 readiness

Go 长运行组件实现 `Name`、`Serve`、`Ready(context.Context)` 和 `Shutdown(context.Context)`。Runtime 并发启动组件，只有 listener 和 worker readiness 均完成后才把进程设为 serving。组件的长期运行 context 由组件自身持有并通过 `Shutdown` 取消，不继承只用于装配和 readiness 的 startup deadline。

服务 readiness 只证明本进程能接自己的活：本地 listener 加上本服务数据面。不对端 RPC 做 Ready 门闩。

- Identity：PostgreSQL、Redis。
- Knowledge：PostgreSQL、NATS、S3、ClamAV。
- Gateway：Redis。
- Collaboration：PostgreSQL、Redis ticket backend、NATS JetStream、actor/workers 和三个 listener。

出站 RPC 使用连续失败熔断（阈值 5、打开 5 秒、半开探测 1）：打开时 fail-fast 且不拨号；超时与连接错误记失败，业务码不记失败。Gateway 将熔断打开映射为 `gateway.dependency_unavailable`（503）；Knowledge 将 Identity/Collaboration 出站失败映射为 `knowledge.unavailable`；Collaboration 的 `authorize`/`project` 在熔断打开时返回 `40007 / collaboration.unavailable`，会话创建 fail-closed，Knowledge 不可用时不得放行未授权会话。Collaboration 启动和 supervisor 不再因 Knowledge `Live` 失败而 not-ready 或退出进程。Collaboration `Ping` 与 admin ready 共用完整应用 `HealthState`，启动中或任一受监督依赖/listener 失败时返回 `not_ready`。RPC serve task 正常返回、报错、panic 或被 abort 都会被标记为 stopped；非计划退出会立即停止 WebSocket 接入并触发进程失败。出站 RPC 地址为严格 `host:port`；非法地址、DNS 失败、空结果或超时 fail closed。Compose 只约束依赖进程启动顺序；最终可服务状态由应用级 readiness 判定。

退出时先把 readiness 设为失败，再按 component 注册逆序停止入口和 worker，等待在途请求，最后按逆序关闭 client、消息、缓存和数据库资源，并 flush telemetry。所有启动、shutdown、cleanup 和 worker operation 都有时间上限。若组件未安全退出，Go Runtime 保留依赖而不是提前关闭底层资源。

Collaboration 收到信号后先 not-ready，再停止 RPC 与 WebSocket 接入、关闭连接和 actor、停止 worker/subscription，最后关闭 Knowledge client、NATS、Redis、PostgreSQL 并 flush telemetry；进程级 timeout 限制整个 shutdown。NATS subscription 意外结束会 fail closed，由外部编排重启进程。

## 11. 生成、验证与交付

Collaboration production Dockerfile（`docker/collaboration/Dockerfile`）在镜像内执行
`cargo build --locked --release`，再将二进制拷入无特权 runtime 层；不依赖宿主机预编译
`.ci-artifacts`。

IDL 源位于 `idl/http/v1` 和 `idl/rpc/v1`。生成工具固定为 Kitex `v0.16.2`、Hertz `v0.9.7`、thriftgo `0.4.5`；生成文件所有权以 `scripts/generated-files.txt` 为准。Gateway handler 和未列入清单的 route middleware 保持手写/混合所有权。

```text
make generate
make generate-check
go run ./scripts/idlguard compat-git <merge-base> idl
```

`make ci` 同时执行 Go/Rust format、vet、golangci-lint、Clippy `-D warnings`、无缓存测试、release build、govulncheck、cargo-deny 和 Go/Rust 生成漂移检查。`make race`、`services/collaboration/interop` 的 `npm ci && npm run ci`、真实 PostgreSQL/Redis/NATS 测试以及 production image smoke 仍需按改动范围显式执行。真实依赖测试设置 `COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1`；缺少任一连接变量时必须失败，不能静默 skip。

开发交付只保留 `dev`，`main` 对开发者保持只读。`.github/workflows/pipeline.yml` 调用通用
Python CI 控制镜像，依次执行质量门禁、变更服务镜像构建、GitOps deploy 快照提交、Argo CD
健康检查和 dev 冒烟；镜像使用 Harbor registry cache，并在 runner 上复用稳定 Buildx builder
`ci-templates`，使 Go（`kc-go-mod`/`kc-go-build`）与 Rust（`kc-cargo-*`）的 BuildKit cache
mount 跨服务存活。编译并行度通过 `BUILD_JOBS` / BuildKit `max-parallelism` 限制为宿主机
CPU 的四分之三。只维护 `dev`/`previous` 临时 tag，不执行镜像扫描或签名。Rust 门禁对 Rust、
IDL、生成器、Makefile 和 workflow 变更启用，其他提交跳过 Rust 检查；`make ci` 不重复执行
release 编译，最终镜像构建仍负责该编译。冒烟后由 DeepSeek 根据限长、脱敏的代码变更上下文
生成分层功能摘要：共享/CI/构建变更写入 Shared changes，服务业务路径变更才进入
Service-specific changes；调用失败即停止。成功后仅 fast-forward `main`，并只创建一个
`v*` 聚合 tag 与对应 GitHub Release（不再创建各服务独立 tag）。GitOps 与源码
仓库均使用普通 fast-forward compare-and-swap push，检测到远端分支变化即停止。

Kubernetes 所有权分为三层：`deploy/k3s`（服务器 `/opt/k3s`）是公共基础设施真源；
应用仓库的 `deploy/<service>/base` 和 `deploy/<service>/overlay/dev` 由各服务自主
维护工作负载与日志、运行环境、超时等服务行为配置；私有
`deploy/Knowledge-Core` 保存基础设施连接配置、SOPS Secret、trust bundle 和镜像
digest。`deploy/` 是应用仓库部署模板的原样快照；`dev/<service>` 分别通过独立 Kustomization
引用服务 overlay 和连接配置，`dev/common` 统一提供运行时补丁与镜像覆盖，共享资源由
`dev/foundation` 持有。不再使用
集中式 `deploy/base` 与 `deploy/overlay/dev`。公共
Nacos、NATS、MinIO、ClamAV 位于独立 namespace，PostgreSQL 和 Redis 复用现有服务；
应用只使用项目级账号和前缀。

k3s 不保存 CI cache/PVC 或构建镜像，只保留 Argo CD；其只读 repository Secret、AppProject 和
ApplicationSet 由 GitOps 仓库声明。平台组件与六个应用服务各自拥有独立 Application，共享资源由
两个 foundation Application 管理；Secret 使用独立 `ksops-v1.0` source。CI 更新统一镜像覆盖后，
只等待受影响服务的 Application，再执行集群内冒烟。

### 当前不兼容基线的迁移记录

本次契约重构已明确批准不保留向后兼容层。相对基线 `0372116`，compat guard 预期失败，主要变化是认证路由改为 users/sessions 资源模型、成功响应移除 envelope、文档 ID 改为 UUID、Unix 秒时间改为 RFC3339、通用 document operation/status 方法改为明确的 CRUD/publication/member/version/attachment 端点。

迁移时必须重新生成所有 HTTP/RPC client，并让 Gateway、Identity、Knowledge、Collaboration 与调用方在同一发布窗口切换；旧 client 不得继续调用新服务。存在旧契约数据的环境应在切换前完成一次性数据转换或清空非生产数据，不做 dual-read、dual-write 或兼容代理。发布验证必须使用本文件第 4、5 节的新契约和强 ETag 语义。

## 12. 当前验证边界

当前自动化测试覆盖领域校验、用例、transport 映射、严格 HTTP 输入、JWT、配置、资源关闭，以及 Collaboration 的 commit-before-broadcast、actor 恢复、重复 update、版本恢复、投影/outbox、真实 PostgreSQL/Redis/NATS、双向 Kitex/Volo mTLS/metadata、Yjs fixture 和多实例 JetStream fanout/redelivery。仍需明确保留以下边界：

- Identity repository/migration 没有针对真实 PostgreSQL 的自动化集成测试。
- Knowledge repository/migration、事务、约束和 SQL cursor 没有针对真实 PostgreSQL 的自动化集成测试。
- 远端 CI 已配置；production image smoke 与完整 Compose 跨服务 WebSocket E2E 仍不是自动门禁。
- CI/CD workflow 已定义上述自动 GitOps 更新、dev smoke、`main` fast-forward、聚合
  `v*` tag 与分层 GitHub Release；首次启用前仍需在目标 GitHub、Harbor、Argo
  和 runner 环境配置凭据并完成端到端演练。
- 应用 Secret、trust bundle、四份 Nacos 加密动态文档以及 Argo CD repository Secret、
  AppProject/ApplicationSet 已配置，并已验证独立 Application 同步。
- 当前只有 development overlay；单节点集群不作为 production 环境。
- Node/Rust 性能基线、PostgreSQL/NATS stop/start 故障演练、备份恢复、切换/回滚和滚动升级尚未完成。
- 生产镜像最终 digest 只能在发布构建后记录，仓库内门禁不能替代部署环境证书、容量和网络策略验证。

因此，Rust 方案已形成可构建、可测试的当前代码实现，但尚不能据此宣称已完成生产切换或完整生产级验收。发布前仍需完成完整链路、性能、证书轮换、备份恢复、容量、故障与切换/回滚演练；Identity/Knowledge 的真实数据库验证继续作为单独缺口保留。
