# Knowledge Core - 产品规格文档

> 一个懂你过去、整理你现在、陪你走向未来的个人 AI 成长操作系统。

## 产品定位

个人知识中台 + 灵魂伴侣 Agent。用户将学习笔记、日记、照片、经历、项目记录等全部沉淀到系统中，系统通过 AI 理解、整理、分析这些数据，形成真正懂用户的个人 Agent。

## 核心价值

1. **记录个人数据** — 学习、生活、经历、照片、想法、目标统一保存，形成个人长期数据资产
2. **理解用户状态** — 分析学过什么、掌握程度、关注方向、情绪变化、反复出现的问题
3. **陪伴并引导成长** — 像长期伙伴一样理解用户，帮助复盘过去、整理现在、规划未来

## 与普通笔记软件的本质差异

| 普通笔记软件 | Knowledge Core |
|---|---|
| 我存了什么？ | 我是谁？我经历了什么？我学会了什么？我接下来该走向哪里？ |

## 系统架构（五层）

```
个人数据层
  笔记 / 日记 / 照片 / 文件 / 学习记录 / 项目经历
    ↓
结构化记忆层
  标签 / 时间 / 主题 / 人物 / 情绪 / 目标 / 知识点
    ↓
知识分析层
  学习掌握度 / 知识图谱 / 情绪趋势 / 目标进度 / 行为模式
    ↓
AI Agent 层
  聊天陪伴 / 学习教练 / 生活复盘 / 项目助手 / 主动建议
    ↓
展示与操作层
  Web 前端 / 桌面客户端 / 文件管理 / 任务系统
```

## 产品形态

- **Web 前端**：知识库浏览、时间线、学习分析、AI 总结、数据看板
- **桌面客户端**（v2）：本地文件管理、笔记导入、照片整理、快捷记录、Agent 操作电脑

---

# 第一阶段：用户知识管理

## 技术路线

第一阶段采用 **Hertz API 网关 + Kitex 微服务**。浏览器和桌面客户端只访问 Hertz，服务间同步调用统一使用 Kitex Thrift RPC，异步任务和领域事件使用 NATS。跨服务基础设施通过 `internal` 下的稳定接口接入，首批 adapter 为 PostgreSQL、Redis、NATS 和 Etcd。

核心约束：

- PostgreSQL 是业务数据最终来源，Markdown 仅作为导入/导出格式。
- 每项业务数据只有一个服务 owner；服务之间禁止通过 SQL 读取或修改其他服务的数据。
- 强一致业务在所属服务的本地事务内完成，跨服务流程使用 RPC 或 Outbox 事件，不引入分布式事务。
- 对外 HTTP、WebSocket 协议与内部 Thrift IDL 分离，传输对象不得直接下沉为领域模型。
- JSON 统一使用 `github.com/bytedance/sonic`；Hertz 保持默认 Sonic binding/rendering，业务代码通过 `internal/codec/json` 使用 JSON。
- 所有服务输出 JSON 结构化日志，并透传 `request_id`、`trace_id` 和调用目标。
- 四个服务均以 Cobra 根命令运行，由统一生命周期捕获退出信号、摘除 readiness、排空传输层并关闭资源；不提供独立 `serve` 或 `migrate` 子命令。
- OpenTelemetry 使用 W3C 上下文传播和 OTLP gRPC 导出；endpoint 为空时关闭导出，采样采用 ParentBased ratio 策略。
- Provider 或连接配置变更通过滚动重启生效，不支持进程内热切换。

### 服务架构

```text
Web / Desktop
      |
      | HTTP / WebSocket
      v
gateway (Hertz)
      |
      | Kitex + Thrift
      +-------------------+-------------------+
      v                   v                   v
identity            knowledge            platform
      |                   |                   |
      +-------------------+-------------------+
                          |
      SQL DB / Redis / NATS / Etcd / OpenTelemetry
```

| 服务 | 对外形态 | 职责 | 持久化 owner |
|---|---|---|---|
| `gateway` | Hertz HTTP / WebSocket | 路由、参数适配、JWT 校验、权限前置检查、限流、响应与错误映射、协作连接管理 | 不持有业务表；仅持有 `gateway:*` Redis 投影和限流键 |
| `identity` | Kitex RPC | 注册、登录、Token 刷新与撤销、用户资料、角色和状态管理 | PostgreSQL `identity` schema、`identity:*` Redis 键 |
| `knowledge` | Kitex RPC | 文档、正文块、操作日志、发布快照、分类、标签、评论、搜索和协作事务 | PostgreSQL `knowledge` schema、`knowledge:*` Redis 键 |
| `platform` | Kitex RPC + NATS consumer | 站点设置、统计投影、导出任务、AI Provider 配置与连接测试 | PostgreSQL `platform` schema、`platform:*` Redis 键 |

### 请求与事件流

1. 公开或登录请求进入 `gateway`，由网关完成 HTTP 参数解析和身份上下文提取。
2. 网关通过 Kitex 调用对应 owner 服务；如请求已登录，则将 `request_id` 和原始 Access Token 放入 RPC metadata，Token 不得进入日志、指标或事件。
3. owner 服务再次执行资源级授权，在本地 PostgreSQL 事务内完成状态变更。
4. 需要跨服务传播的状态先写入本服务 Outbox，再由投递器发布到 NATS JetStream。
5. 统计和导出消费者按 CloudEvent `id` 幂等处理，失败时由 JetStream 重投，超过上限后进入死信 subject。
6. WebSocket 由网关终止连接；文档操作通过 Kitex 提交到知识服务，提交成功后再向客户端返回 `ack`。
7. 协作广播和 `presence` 使用非持久 NATS Core；断线重连以 `document_ops` 快照和增量记录恢复，不能依赖实时广播补历史。

## 技术栈

| 层 | 选型 | 理由 |
|---|---|---|
| HTTP 网关 | Go + Hertz | 承接公开 REST、Studio 管理 API 和 WebSocket，统一处理鉴权与协议适配 |
| 内部 RPC | Kitex + Thrift | IDL 优先的服务契约，支持中间件、超时、治理和业务异常 |
| 异步通信 | NATS JetStream + NATS Core | JetStream 承载可靠事件和任务，Core NATS 承载协作实时广播 |
| JSON 编解码 | `github.com/bytedance/sonic` | 统一 Hertz binding/rendering、事件和配置 JSON；配置与事件严格解码并启用 `UseNumber` |
| 数据访问 | `internal/database` 接口 + `database/sql` | 首个 adapter 使用 `pgx` stdlib，保留显式 SQL、事务和行级锁控制 |
| 前端 | React + Next.js + TypeScript | Markdown 生态成熟、SSR/SSG 支持 |
| 样式 | Tailwind CSS | 快速迭代，与 Next.js 搭配好 |
| Markdown 渲染 | remark + rehype 插件链 | 可扩展的 AST 转换 |
| 主数据库 | PostgreSQL 16 | 支持事务、行级锁、JSONB、全文搜索和协作数据一致性 |
| 缓存与投影 | Redis 7 | Token 会话、撤销投影、限流和短期缓存，按服务命名空间隔离 |
| 配置与服务发现 | Etcd | Kitex 注册发现、非敏感应用配置和 Kitex 治理配置，key 空间隔离 |
| 可观测性 | OpenTelemetry + Prometheus + JSON 日志 | 统一链路、指标和结构化日志上下文 |
| AI 接口 | OpenAI SDK / Anthropic SDK | 导入分析，后续可替换 |

### Monorepo 目标结构

```text
idl/
  http/v1/
  rpc/v1/
internal/
  codec/json/
  config/{env,etcd}/
  database/postgres/
  messaging/nats/
  cache/redis/
  discovery/etcd/
  observability/
  health/
  lifecycle/
  command/
kitex_gen/
services/
  gateway/{main.go,biz,internal}/
  identity/{main.go,internal}/
  knowledge/{main.go,internal}/
  platform/{main.go,internal}/
migrations/
  identity/postgres/
  knowledge/postgres/
  platform/postgres/
scripts/
  codegen.ps1
  codegen.sh
docker/infrastructure/
```

- 仓库使用一个 `go.mod`，服务进程入口统一位于 `services/<service>/main.go`；依赖通过普通构造函数显式装配，不使用 Wire/Fx。
- Thrift IDL 是内部 RPC 的唯一契约来源，生成代码统一放在 `kitex_gen/`，不得手工修改。
- HTTP IDL 放在 `idl/http/v1/`，内部 Thrift RPC IDL 放在 `idl/rpc/v1/`；Hertz 与 Kitex 生成代码只做传输适配，不写业务规则。
- `internal` 下的共享包只放 JSON、配置、数据库、缓存、消息、注册发现、可观测性和生命周期等基础设施能力，不承载业务规则。
- 各业务包只能导入自己的领域代码、共享基础设施接口和生成的 RPC 契约，不直接导入其他服务的 repository。
- 完整接口与装配规则见 [Hertz + Kitex 服务框架设计](./framework-design.md)。

## HTTP 与 RPC 契约

### HTTP 路径

- 公开和登录用户 API：`/api/v1/...`
- Studio 管理 API：`/api/v1/studio/...`
- Studio 协作 WebSocket：`/api/v1/studio/documents/:id/collab`
- 健康检查：`/health/live` 与 `/health/ready`

所有 JSON API 使用统一响应：

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "request_id": "01J..."
}
```

- `code` 为稳定数字错误码，`0` 表示成功；`1xxxx`、`2xxxx`、`3xxxx`、`4xxxx` 分别保留给网关、身份、知识和平台域。
- Kitex handler 使用 `BizStatusError` 返回业务异常，网关负责映射 HTTP 状态、对外 message 和数字错误码。
- 内部根因、SQL、堆栈、连接串和敏感 payload 不进入 HTTP 或 WebSocket 响应，只写入受控 JSON 日志。
- 文件下载成功时直接返回文件流；失败时返回统一 JSON 错误结构。

### RPC 契约

| Kitex 服务 | 主要能力 |
|---|---|
| `IdentityService` | 注册、登录、刷新、登出、个人资料、密码变更、Studio 用户管理 |
| `KnowledgeService` | 文档、块级操作、发布快照、搜索、分类、标签、评论和协作快照 |
| `PlatformService` | 设置、统计查询、导出任务、AI 连接测试 |

- 所有 RPC 必须设置超时并传递取消信号，不允许使用无截止时间的后台 context 发起外部调用。
- RPC middleware 统一注入日志、追踪、指标和调用方身份；业务 service 不依赖 Hertz request/response 类型。
- Kitex 服务通过 Etcd 注册发现：服务端注入 Kitex 原生 registry，客户端注入 Kitex 原生 resolver。调用方使用稳定服务名，不保存容器 IP。
- Etcd 同时承载非敏感应用配置和 Kitex 治理配置；数据库凭据、JWT 私钥和 API Key 等敏感值只通过环境变量或 Secret 注入。

## 数据所有权与迁移

### PostgreSQL schema

- 本地开发使用一个 `knowledge_core` database，并创建 `identity`、`knowledge`、`platform` 三个 schema。
- 每个服务使用独立数据库账号和 `search_path`，账号只拥有所属 schema 权限。
- 禁止跨 schema 查询、外键、事务和 migration；跨服务读写必须走 Kitex 或 NATS。
- 每个 schema 独立维护 Outbox 和已处理事件记录，保证投递与消费幂等。

### 迁移目录

所有数据库迁移文件放在仓库根目录 `migrations/`，按 owner 服务和 Provider 两级分目录：

```text
migrations/
  identity/
    postgres/
      000001_create_users.up.sql
      000001_create_users.down.sql
  knowledge/
    postgres/
      000001_create_documents.up.sql
      000001_create_documents.down.sql
  platform/
    postgres/
      000001_create_settings.up.sql
      000001_create_settings.down.sql
```

- 每个 Provider 目录独立递增编号，迁移必须提供配对的 `up` 和 `down` 文件。
- 每个服务只执行自己和当前 Provider 目录中的迁移，并在自己的 schema 中维护迁移版本。
- migration SQL 由 owner 服务嵌入二进制，并在本服务启动、repository 和网络监听建立前自动执行；失败或 dirty version 必须阻止服务进入 ready。
- 新数据库 Provider 必须实现对应 repository 方言并建立自己的完整 migration 链；接口抽象不消除 SQL 方言和数据迁移工作。
- migration 不得创建存储过程、数据库函数或业务状态触发器；时间戳、状态流转和派生字段由代码显式维护。
- 跨服务变更先保持事件和 RPC 契约向后兼容，再分别发布各服务迁移，不依赖固定启动顺序完成分布式事务。

## NATS 事件规范

- 可靠领域事件进入 `KC_EVENTS` stream，subject 格式为 `kc.events.<domain>.<aggregate>.<action>.v1`。
- 异步任务进入 `KC_TASKS` stream，subject 格式为 `kc.tasks.<domain>.<action>.v1`。
- 协作临时消息使用 `kc.realtime.documents.<document_id>`，不进入 JetStream。
- JetStream 采用 at-least-once 投递；消费者必须用 CloudEvent `id` 去重，不能假设消息只到达一次。
- 事件统一使用 `application/cloudevents+json`，至少包含 `specversion`、`id`、`source`、`type`、`time`、`subject` 和 `data`。
- 事件只能携带业务所需的最小数据，不得包含密码、Token、API Key 或完整敏感 payload。

首批领域事件：

| CloudEvent type | 生产者 | 主要消费者 |
|---|---|---|
| `com.knowledgecore.identity.user.registered.v1` | identity | platform 统计投影 |
| `com.knowledgecore.identity.token-version.changed.v1` | identity | gateway 撤销投影 |
| `com.knowledgecore.knowledge.document.published.v1` | knowledge | platform 统计投影 |
| `com.knowledgecore.knowledge.comment.approved.v1` | knowledge | platform 统计投影 |
| `com.knowledgecore.platform.export.requested.v1` | platform | platform 导出 worker |
| `com.knowledgecore.platform.export.completed.v1` | platform | gateway/Studio 查询 |

## 本地开发运行

基础脚手架已经提供四个服务入口以及 PostgreSQL、Redis、NATS JetStream、Etcd 的本地 Compose 环境。Identity 与 Knowledge 的 PostgreSQL migration 已嵌入服务并在启动时自动执行；Platform 的迁移链随对应业务切片建立，并遵循相同启动规则。

```powershell
copy .env.example .env
# 在 .env 中填写 KC_POSTGRES_PASSWORD 和 KC_DATABASE_DSN，并将运行时变量加载到各服务进程环境。

# 通过本地 Secret 管理器或安全密钥生成流程，将 Base64 编码的 Ed25519 私钥和公钥
# 分别注入 KC_IDENTITY_JWT_PRIVATE_KEY、KC_GATEWAY_JWT_PUBLIC_KEY 与 KC_KNOWLEDGE_JWT_PUBLIC_KEY。
# 首次启动 Identity 时还需通过 Secret 注入 KC_IDENTITY_BOOTSTRAP_ADMIN_USERNAME、
# KC_IDENTITY_BOOTSTRAP_ADMIN_EMAIL 与 KC_IDENTITY_BOOTSTRAP_ADMIN_PASSWORD；已有管理员后不再使用这些值。

docker compose -f docker/infrastructure/docker-compose.yml up -d postgres redis nats etcd

# 文档 MVP 启动 Identity、Knowledge 与 Gateway。
go run ./services/identity
go run ./services/knowledge
go run ./services/gateway
```

服务入口是无额外子命令的 Cobra 根命令，所以上述运行方式保持不变；`Ctrl+C`、`SIGINT` 和 `SIGTERM` 均进入统一优雅退出流程。日志默认以 JSON 写入 stderr。设置 `KC_OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4317` 可启用 OTLP gRPC trace 导出，`KC_OTEL_TRACE_SAMPLE_RATIO` 控制入口采样率。

认证 MVP 验证：

```powershell
$body = @{ username = "alice"; email = "alice@example.com"; password = "correct-password" } | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/api/v1/auth/register -ContentType application/json -Body $body

$body = @{ identifier = "alice"; password = "correct-password" } | ConvertTo-Json
$login = Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/api/v1/auth/login -ContentType application/json -Body $body
Invoke-RestMethod -Uri http://127.0.0.1:8080/api/v1/users/me -Headers @{ Authorization = "Bearer $($login.data.access_token)" }
```

本地默认基础设施：

```text
PostgreSQL: localhost:5432/knowledge_core
Redis:      localhost:6379
NATS:       localhost:4222
JetStream:  enabled
Etcd:       localhost:2379
```

- `.env` 只保存本地覆盖且不得提交；仓库配置文件只包含非敏感默认值。
- 数据库密码、Ed25519 私钥、AI API Key 等敏感值通过环境变量或 Secret 注入。
- `identity` 持有 JWT 私钥，网关和业务服务只配置公钥。
- readiness 必须检查本服务必要依赖是否就绪；liveness 只检查进程是否存活。

## 文档存储模型

本节数据全部由 `knowledge` 管理，表位于 PostgreSQL `knowledge` schema。其他服务只能通过 `KnowledgeService` RPC 或知识域事件访问相关能力。

### documents

- 保存文档元数据：标题、摘要、slug、分类、作者、发布状态、当前版本、发布时间。
- `status` 使用 `draft` / `published`。
- `current_version` 是编辑态文档版本，每个成功 op 递增一次。
- `search_vector` 是 PostgreSQL generated tsvector，用于全文搜索。

### document_blocks

- 保存正文块：`block_id, document_id, parent_id, position_key, type, content_json, text_content, version, updated_by, updated_at`。
- `content_json` 使用 JSONB，当前 MVP 以 paragraph 块为主。
- 同一文档内不同块可以并发编辑；同一块版本不匹配返回冲突。

### document_ops

- 保存协作操作日志：`op_id, document_id, actor_id, base_document_version, block_id, op_type, payload_json, document_version, block_version, created_at`。
- `op_id` 全局唯一，用于幂等提交。重复提交同一 `op_id` 返回原始 ack，不重复修改正文。

### document_revisions

- 保存发布快照或手动快照。
- 前台公开详情只读取最新已发布 revision，继续编辑草稿不会影响公开内容。

## Markdown 导入/导出

Markdown 不是在线编辑的最终存储格式。

- 导入：`Markdown -> blocks_v1 JSONB`。
- 导出：`blocks/revision -> Markdown`。
- frontmatter 可作为导入/导出的边界格式，但不作为在线编辑主链路。

## 用户系统

用户、凭据、Refresh Token 和登录安全状态由 `identity` 管理，表位于 PostgreSQL `identity` schema。

### 用户角色

| 角色 | 权限 | 默认账号 |
|---|---|---|
| 管理员 | 全部权限：笔记 CRUD、导入/导出、用户管理、系统设置、前台内容发布 | 首次启动自动创建 |
| 普通用户 | 前台浏览已发布文章、注册/登录、个人资料修改、评论（v2） | 注册创建 |

### 用户模型

```go
type User struct {
    ID        int64     `json:"id"`
    Username  string    `json:"username"`
    Email     string    `json:"email"`
    Password  string    `json:"-"`           // bcrypt 哈希
    Role      string    `json:"role"`         // "admin" | "user"
    Status    string    `json:"status"`       // "active" | "disabled"
    TokenVersion int64  `json:"token_version"`
    Avatar    string    `json:"avatar"`       // 头像 URL
    Bio       string    `json:"bio"`          // 个人简介
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

### 认证方式

- Access Token 使用 Ed25519 JWT，默认有效期 15 分钟；只有 `identity` 持有签名私钥。
- `gateway` 和业务服务持有公钥并校验签名、过期时间及必要 claims；网关额外将 Token 中的 `token_version` 与撤销投影比较，再把原始 Token 透传给 RPC 服务进行二次签名校验和资源级授权。
- Refresh Token 明文只返回给客户端，服务端只保存 SHA-256 hash。
- `identity:*` Redis 命名空间保存活跃 refresh token 会话元数据，PostgreSQL `identity.refresh_tokens` 保留审计与 Redis 故障降级。
- Refresh 时即使 Redis 命中也会强校验 PostgreSQL 中的撤销、过期、用户状态和 `token_version`。
- 修改密码、禁用用户、角色或状态变化会在同一身份服务事务内递增 `token_version`、撤销全部 refresh token，并写入 Outbox。
- 身份服务发布 `token-version.changed` CloudEvent；网关消费后更新 `gateway:auth:version:<user_id>` 撤销投影，使已签发 Access Token 秒级失效。
- Studio 和其他写接口无法读取撤销投影时必须失败关闭并返回服务暂不可用；公开匿名读取不受影响。

### API 路由

```
POST   /api/v1/auth/register      # 注册（公开）
POST   /api/v1/auth/login         # 登录（公开）
POST   /api/v1/auth/refresh       # 刷新 Token（公开）
POST   /api/v1/auth/logout        # 登出（需登录）

GET    /api/v1/users/me            # 当前用户信息（需登录）
PUT    /api/v1/users/me            # 更新个人资料（需登录）
PUT    /api/v1/users/me/password   # 修改密码（需登录）

GET    /api/v1/studio/users              # 用户列表（仅 admin）
GET    /api/v1/studio/users/:id          # 用户详情（仅 admin）
PATCH  /api/v1/studio/users/:id          # 修改用户资料/角色/状态（仅 admin）
DELETE /api/v1/studio/users/:id          # 禁用用户（仅 admin）
PUT    /api/v1/studio/users/:id/password # 重置密码（仅 admin）
```

当前认证 MVP 已实现 `register`、`login` 和 `users/me`。`refresh`、`logout`、个人资料修改和 Studio 用户管理仍属于后续切片。

### 前台与 Studio 的用户体验区分

- **前台**：注册/登录后可浏览已发布文章，个人中心可修改头像和简介
- **Studio**：仅管理员可访问（前端路由 `/studio`），使用管理员账号登录
- 前台登录和 Studio 登录共用身份服务，通过 Role 区分权限

### 安全策略

- 密码 bcrypt 加密存储
- 注册时邮箱验证（v2，MVP 阶段可跳过）
- 登录失败 5 次后锁定 15 分钟，计数和锁定规则由身份服务执行
- Refresh Token 撤销与 `token_version` 事件投影共同支持强制登出
- Ed25519 私钥、数据库密码和第三方凭据只能通过环境变量或 Secret 注入，不写入配置文件、日志和事件

## 评论系统

评论由 `knowledge` 管理，和文档归属同一事务边界，表位于 PostgreSQL `knowledge` schema。

### 数据模型

```go
type Comment struct {
    ID         int64     `json:"id"`
    DocumentID int64     `json:"document_id"`
    UserID     int64     `json:"user_id"`
    ParentID   int64     `json:"parent_id"`       // 0 = 顶级评论
    Content    string    `json:"content"`         // Markdown 格式
    Status     string    `json:"status"`          // "pending" | "approved" | "rejected"
    CreatedAt  time.Time `json:"created_at"`
    UpdatedAt  time.Time `json:"updated_at"`
}
```

### 评论规则

| 规则 | 说明 |
|---|---|
| 发评论 | 需登录，Markdown 格式，限 2000 字 |
| 评论状态 | 新评论默认 `pending`，管理员审核后变为 `approved` |
| 审核机制 | 管理员可在 Studio 批量审核/拒绝，前端只显示 `approved` 评论 |
| 作者高亮 | 文章作者（管理员）的评论带特殊标识 |
| 删除评论 | 管理员可删除任何评论，用户只能删除自己的评论 |
| v2 扩展 | 嵌套回复（ParentID）、点赞、举报 |

### API 路由

```
GET    /api/v1/documents/:id/comments          # 获取评论（仅 approved，公开）
POST   /api/v1/documents/:id/comments          # 发表评论（需登录）
DELETE /api/v1/comments/:id                    # 删除评论（本人或 admin）

GET    /api/v1/studio/comments?status=pending   # 待审核评论列表（仅 admin）
PUT    /api/v1/studio/comments/:id/approve      # 审核通过（仅 admin）
PUT    /api/v1/studio/comments/:id/reject       # 审核拒绝（仅 admin）
DELETE /api/v1/studio/comments/:id              # 删除评论（仅 admin）
```

### 前端展示

- 文章阅读页底部显示评论列表（按时间倒序）
- 每条评论显示：头像、用户名、时间、内容、操作
- 管理员评论显示“作者”标签
- 未登录用户显示登录提示，隐藏输入框

## Studio 设置

站点设置和 AI Provider 配置由 `platform` 管理，表位于 PostgreSQL `platform` schema。AI API Key 只通过环境变量或部署平台 Secret 注入，配置、数据库、事件和日志均不保存明文。

### 设置项

```go
type SiteSettings struct {
    SiteName        string `json:"site_name"`         // 站点名称
    SiteDescription string `json:"site_description"` // 站点描述
    SiteURL         string `json:"site_url"`          // 站点地址
    AdminEmail      string `json:"admin_email"`      // 管理员邮箱
    AllowRegister   bool   `json:"allow_register"`   // 是否允许注册
    CommentModerate bool   `json:"comment_moderate"`  // 是否开启评论审核
    PostsPerPage    int    `json:"posts_per_page"`    // 每页文章数
    Theme           string `json:"theme"`             // 主题: "light" | "dark" | "auto"
    AIProvider      string `json:"ai_provider"`       // AI 服务商: "openai" | "anthropic"
    AIModel         string `json:"ai_model"`          // 模型名称
    APIKeyConfigured bool  `json:"api_key_configured"` // 仅表示 Secret 是否已配置
    APIBaseURL      string `json:"api_base_url"`     // 自定义 API 地址
}
```

### API 路由

```
GET  /api/v1/studio/settings         # 获取设置（仅 admin，返回 Secret 是否已配置）
PUT  /api/v1/studio/settings         # 更新设置（仅 admin）
POST /api/v1/studio/settings/test-ai # 测试 AI 连接（仅 admin）
```

### 设置页面分区

| 分区 | 内容 |
|---|---|
| 基本设置 | 站点名称、描述、URL、管理员邮箱、开关注册 |
| 评论设置 | 是否开启审核、每页评论数 |
| 外观设置 | 每页文章数、主题选择 |
| AI 设置 | 服务商选择、模型名称、API 地址、Secret 配置状态、测试连接按钮 |

## 数据看板

数据看板由 `platform` 管理。平台服务消费身份域和知识域事件形成最终一致的统计投影，不通过跨 schema SQL 实时聚合。

### 统计指标

| 指标 | 说明 | 时间维度 |
|---|---|---|
| 总访问量 | PV/UV | 今日 / 7日 / 30日 |
| 文章总数 | 已发布文章 | 累计 |
| 评论总数 | 已通过评论 | 累计 / 今日新增 |
| 用户总数 | 注册用户 | 累计 / 今日新增 |
| 热门文章 Top 10 | 按浏览量排序 | 7日 / 30日 / 全部 |
| 文章趋势 | 每日新增文章折线图 | 近 30 天 |
| 访问趋势 | 每日 PV 折线图 | 近 30 天 |
| 用户增长 | 每日新注册用户 | 近 30 天 |

### API 路由

```
GET /api/v1/studio/dashboard/overview   # 总览统计
GET /api/v1/studio/dashboard/trends     # 趋势数据（articles, visits, users, ?days=30）
GET /api/v1/studio/dashboard/top-articles?limit=10&period=7d # 热门文章
```

## 标签与分类管理

分类和标签与文档共同由 `knowledge` 管理，表位于 PostgreSQL `knowledge` schema。

### 数据模型

```go
type Category struct {
    ID       int64  `json:"id"`
    Name     string `json:"name"`
    Slug     string `json:"slug"`      // URL 友好标识
    Path     string `json:"path"`      // 层级路径，如 tech/ai
    ParentID int64  `json:"parent_id"` // 支持层级分类
    Sort     int    `json:"sort"`                    // 排序权重
}

type Tag struct {
    ID   int64  `json:"id"`
    Name string `json:"name"`
    Slug string `json:"slug"`
}
```

### 管理功能

| 操作 | 分类 | 标签 |
|---|---|---|
| 创建 | 仅管理员 | 仅管理员 |
| 编辑 | 仅管理员 | 仅管理员 |
| 删除 | 仅管理员；仅允许无子分类且无文档引用时删除 | 仅管理员；删除前必须无文档引用 |
| 合并 | 后续阶段 | 后续阶段 |
| 重命名 | 仅管理员（自动更新 slug） | 仅管理员 |

### API 路由

```
# 分类
GET    /api/v1/categories                # 公开分类列表（仅含已发布内容关联）
GET    /api/v1/studio/categories          # 分类列表（admin）
POST   /api/v1/studio/categories          # 创建分类
PATCH  /api/v1/studio/categories/:id      # 更新分类
DELETE /api/v1/studio/categories/:id      # 删除分类

# 标签
GET    /api/v1/tags                      # 公开标签列表（仅含已发布内容关联）
GET    /api/v1/studio/tags                # 标签列表（admin）
POST   /api/v1/studio/tags                # 创建标签
PATCH  /api/v1/studio/tags/:id            # 更新标签
DELETE /api/v1/studio/tags/:id            # 删除标签
```

## 文章编辑器

编辑器数据和协作事务由 `knowledge` 管理；`gateway` 只负责 HTTP/WebSocket 协议适配和连接生命周期。

### 编辑器功能

- 块级文档编辑器，内部数据格式为 blocks JSON。
- 支持 Markdown 导入/导出，但在线编辑不直接写 Markdown 文件。
- 支持 metadata 编辑（分类、标签、标题、摘要、发布状态）
- 工具栏：加粗、斜体、标题(H1-H3)、代码块、链接、图片、引用、列表、表格
- 自动保存通过 `POST /api/v1/studio/documents/:id/ops` 或 WebSocket `op` 消息提交。
- 多人实时协作使用 WebSocket：同文档不同块可以并发编辑，同一块版本冲突返回 conflict。
- 发布/草稿状态通过 `PATCH /api/v1/studio/documents/:id` 修改 `status`。
- AI 辅助（摘要、标签、续写）属于后续阶段。

### 数据模型扩展

```go
type Document struct {
    ID             int64
    Slug           string
    Title          string
    Summary        string
    CategoryID     int64
    Status         string // "draft" | "published"
    AuthorID       int64
    CurrentVersion int64
    PublishedAt    *time.Time
}

type DocumentBlock struct {
    BlockID     string
    DocumentID   int64
    ParentID     string
    PositionKey  string
    Type         string
    ContentJSON  string
    TextContent  string
    Version      int64
}
```

### API 路由

```
GET    /api/v1/documents                         # 公开已发布文档列表
GET    /api/v1/documents/:id                     # 公开文档详情（读取 published revision）

GET    /api/v1/studio/documents                   # Studio 文档列表
POST   /api/v1/studio/documents                   # 创建文档
GET    /api/v1/studio/documents/:id               # 当前编辑态详情（metadata + blocks）
PATCH  /api/v1/studio/documents/:id               # 更新 metadata、blocks 或 status
DELETE /api/v1/studio/documents/:id               # 删除文档
POST   /api/v1/studio/documents/:id/ops           # HTTP 块级操作提交
GET    /api/v1/studio/documents/:id/collab        # WebSocket 协作通道
```

WebSocket 消息类型固定为：`hello`、`snapshot`、`op`、`ack`、`conflict`、`presence`、`error`。

- `op` 必须携带全局唯一 `op_id` 和基础版本；知识服务只在事务提交成功后返回 `ack`。
- 重复 `op_id` 返回首次提交的原始 `ack`，不重复修改文档或发布广播。
- `conflict` 返回当前块版本和安全冲突快照；`error` 使用数字 `code`、安全 `message` 和 `request_id`。
- 网关实例通过 `kc.realtime.documents.<document_id>` 广播已提交操作和 presence；消息丢失后客户端通过 snapshot/ops 恢复。

## 导出功能

导出由 `platform` 管理。平台服务通过 `KnowledgeService` RPC 获取有权限访问的文档快照，不读取 `knowledge` schema。

### 支持的导出格式

| 格式 | 说明 | 批量 |
|---|---|---|
| Markdown (.md) | 原始 Markdown + frontmatter | 支持 |
| PDF | 渲染后的文章（含样式） | 单篇 |
| JSON | 结构化数据（frontmatter + content） | 支持 |
| ZIP | 批量打包为 Markdown 文件集合 | 支持 |

### API 路由

```
GET    /api/v1/studio/export/markdown/:id       # 同步导出单篇 Markdown
GET    /api/v1/studio/export/pdf/:id            # 同步导出单篇 PDF
GET    /api/v1/studio/export/json/:id           # 同步导出单篇 JSON
POST   /api/v1/studio/export/batch              # 创建批量导出任务，返回 202 + task_id
GET    /api/v1/studio/export/tasks/:task_id     # 查询导出任务状态
GET    /api/v1/studio/export/download/:task_id  # 下载已完成的导出文件
```

### 导出流程

1. 用户选择文章并选择导出格式。
2. 单篇导出由平台服务同步调用知识服务获取快照并返回文件流。
3. 批量导出在 `platform.export_jobs` 创建任务和 Outbox 记录，提交后返回 `202 Accepted` 与 `task_id`。
4. Outbox 发布 `export.requested` 任务，导出 worker 幂等生成 ZIP 并更新任务状态。
5. 客户端轮询任务状态，完成后通过下载接口获取文件；失败状态返回稳定错误码和可重试标志。

## MVP 范围（第一阶段）

### 必做（最小闭环）

1. Hertz `gateway` 与 `identity`、`knowledge`、`platform` 三个 Kitex Thrift 服务的单模块 Monorepo 骨架
2. PostgreSQL `identity`、`knowledge`、`platform` schema 及 `migrations/<service>/postgres` 独立迁移链
3. `internal` 基础设施接口及 PostgreSQL、Redis、NATS、Etcd 首批 adapter
4. Ed25519 JWT、Refresh Token、撤销事件投影和 Studio 权限控制
5. PostgreSQL 文档主存储（documents、document_blocks、document_ops、document_revisions）
6. Web 端文档列表浏览、PostgreSQL 全文搜索和公开阅读
7. 基于 published revision 的公开详情渲染
8. Studio 文档 CRUD、块级 ops 提交、发布和取消发布
9. 前台用户个人中心
10. 文章评论系统（前台浏览 + Studio 审核）
11. Studio 设置（站点配置、AI API、SEO）
12. 事件驱动的数据看板（访问量、文章趋势、用户增长）
13. 标签与分类管理（Studio CRUD）
14. 块级协作编辑器（HTTP ops + WebSocket + NATS 实时广播）
15. 导出功能（Markdown/PDF/JSON/ZIP，批量任务后续接入）
16. Sonic JSON codec、JSON 日志、OpenTelemetry tracing、Prometheus metrics 和健康检查

### 暂不做（后续阶段）

- AI Agent 陪伴人格
- 学习掌握度分析
- 时间线视图
- 桌面客户端
- 主动提醒/复盘
- 知识图谱可视化
- 邮箱验证
- 嵌套回复（楼中楼）
- 评论点赞/举报
- 友链功能
- 字符级 CRDT/OT
- 分类/标签合并
- AI 摘要、AI 标签、AI 续写
- 文件系统作为在线编辑主存储

## 设计原则

1. **服务 owner 优先** — 每项能力、数据、迁移和事件只有一个 owner，禁止跨 schema SQL 和跨服务内部实现依赖。
2. **本地事务优先** — 在线编辑主链路使用知识服务的 PostgreSQL 事务和行级锁；跨服务流程接受显式最终一致性。
3. **契约优先** — 外部 HTTP、内部 Thrift 和 CloudEvents 分别版本化，升级先保持向后兼容，再发布生产者和消费者。
4. **幂等与可恢复** — 文档操作、Outbox 投递、JetStream 消费和导出任务均有稳定幂等键；实时消息丢失可由持久化状态恢复。
5. **安全默认关闭** — 身份状态不可验证、Studio 撤销投影不可用或资源权限不明确时拒绝请求，不降级绕过鉴权。
6. **可观测性内建** — HTTP、RPC、事件和数据库调用统一关联 request/trace 上下文，错误根因进入脱敏 JSON 日志。
7. **渐进式 AI** — AI 辅助但不强制，所有 AI 生成内容用户可审阅、修改和撤销。
8. **块级协作优先** — 先解决多人协作的幂等、冲突和发布快照，再扩展更细粒度协同。
9. **开放导入导出** — Markdown 是系统边界格式，方便迁移和备份，但不是在线编辑数据源。
10. **基础设施隔离** — 业务层依赖稳定接口；连接切换收敛到配置，跨 Provider 切换显式处理 adapter、方言、迁移和数据搬迁。
