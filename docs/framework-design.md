# Knowledge Core - Hertz + Kitex 服务框架设计

> 状态：基础框架 v0.1 已实现。四个 Cobra 进程入口、统一生命周期、IDL/代码生成、`internal` 基础设施接口与首批 PostgreSQL/Redis/NATS/Etcd adapter 已落地；统一 JSON 日志和 HTTP/Kitex OpenTelemetry tracing 已接入。Identity 已完成用户注册、凭据校验、Ed25519 Access Token、用户查询、首次管理员初始化和首条 PostgreSQL migration；Knowledge 已完成文档、块操作、发布 revision 的 PostgreSQL 存储及迁移。Gateway 已接通认证与文档 HTTP API。Refresh Token、注销、撤销事件投影、NATS 协作、其他业务用例和 Prometheus metrics 仍按本文继续实现。

## 1. 设计目标

Knowledge Core 使用单 Go module Monorepo，运行时拆分为 `gateway`、`identity`、`knowledge`、`platform` 四个进程。对外 HTTP/WebSocket 由 Hertz 承载，服务间同步调用使用 Kitex + Thrift，可靠事件与实时广播通过消息基础设施完成。

框架需要满足以下目标：

1. 业务层只依赖稳定接口，不依赖 PostgreSQL、Redis、NATS、Etcd 客户端类型。
2. 同一基础设施 Provider 更换地址、账号或集群时，只修改连接配置并滚动重启。
3. 更换不同 Provider 时，装配入口和业务用例保持稳定，变化收敛在 adapter、SQL 方言、迁移与数据搬迁层。
4. HTTP、RPC、事件、配置统一使用可追踪的上下文、稳定错误模型和明确生命周期。
5. JSON 编解码统一使用 `github.com/bytedance/sonic`，业务代码不散落具体 JSON 库调用。

不做以下承诺：

- 不把不同关系型数据库的 SQL 方言、锁语义和索引能力伪装成完全一致。
- 不提供进程内 Provider 热切换；Provider 或连接配置变化通过滚动重启生效。
- 不承诺消息 exactly-once；公共语义是 at-least-once、显式 ack/nack、幂等消费和死信处理。
- 不设计跨服务数据库事务；跨服务一致性使用 Outbox、可靠事件和补偿流程。

## 2. 运行时拓扑

```text
Web / Desktop
      |
      | HTTP / WebSocket
      v
gateway (Hertz)
      |
      | Kitex + Thrift + Etcd discovery
      +----------------+----------------+
      v                v                v
identity          knowledge          platform
      |                |                |
      +----------------+----------------+
                       |
       SQL DB / Redis / NATS / Etcd / OTel
```

| 进程 | 传输入口 | 主要职责 |
|---|---|---|
| `gateway` | Hertz HTTP/WebSocket、NATS consumer | 外部协议适配、JWT 前置校验、限流、错误映射、协作连接 |
| `identity` | Kitex RPC、NATS publisher | 认证、Token、用户、角色和状态 |
| `knowledge` | Kitex RPC、NATS publisher/consumer | 文档、块操作、发布、分类、标签、评论和搜索 |
| `platform` | Kitex RPC、NATS consumer | 设置、统计投影、导出任务和 AI Provider 管理 |

认证与用户资料属于同一身份一致性边界，原先可能拆分的 `auth`、`user` 仅作为 `identity` 内部模块，不建立独立进程或跨模块 RPC。

Etcd 同时承担三类职责，但 key 空间必须隔离：Kitex 注册发现、应用非敏感配置、Kitex 治理配置。数据库凭据、JWT 私钥、AI API Key 等敏感值只从环境变量或部署平台 Secret 注入。

## 3. 目录约定

```text
.
|-- go.mod
|-- idl/
|   |-- http/
|   |   `-- v1/
|   |       `-- gateway.thrift
|   `-- rpc/
|       `-- v1/
|           |-- common.thrift
|           |-- identity.thrift
|           |-- knowledge.thrift
|           `-- platform.thrift
|-- kitex_gen/                       # Kitex 生成代码，不手工修改
|-- services/
|   |-- gateway/
|   |   |-- main.go
|   |   |-- biz/                     # Hertz 生成的 handler/model/router 适配层
|   |   `-- internal/
|   |       |-- app/
|   |       |-- client/
|   |       |-- middleware/
|   |       `-- bootstrap/
|   |-- identity/
|   |   |-- main.go
|   |   `-- internal/
|   |       |-- app/
|   |       |-- domain/
|   |       |-- repository/
|   |       |-- transport/kitex/
|   |       `-- bootstrap/
|   |-- knowledge/
|   |   |-- main.go
|   |   `-- internal/
|   |       |-- app/
|   |       |-- domain/
|   |       |-- repository/
|   |       |-- transport/kitex/
|   |       `-- bootstrap/
|   `-- platform/
|       |-- main.go
|       `-- internal/
|           |-- app/
|           |-- domain/
|           |-- repository/
|           |-- transport/kitex/
|           `-- bootstrap/
|-- internal/
|   |-- command/
|   |-- codec/json/
|   |-- config/
|   |   |-- env/
|   |   `-- etcd/
|   |-- database/
|   |   `-- postgres/
|   |-- messaging/
|   |   `-- nats/
|   |-- cache/
|   |   `-- redis/
|   |-- discovery/
|   |   `-- etcd/
|   |-- observability/
|   |-- health/
|   `-- lifecycle/
|-- migrations/
|   |-- identity/postgres/
|   |-- knowledge/postgres/
|   `-- platform/postgres/
|-- scripts/
|   |-- codegen.ps1
|   `-- codegen.sh
`-- docker/infrastructure/
```

旧 `api/`、`pkg/`、`proto/` 及 `auth`、`user` 空服务目录已清理。目录树中的 `app`、`domain`、`repository`、RPC client、迁移命令和具体迁移 SQL 随对应业务切片建立，不预先创建空包。

## 4. 依赖方向

```text
transport (Hertz / Kitex / consumer)
                 |
                 v
          application use case
                 |
                 v
              domain
                 ^
                 |
repository interface / internal interface
                 ^
                 |
adapter (PostgreSQL / Redis / NATS / Etcd)
```

必须遵守以下规则：

1. `domain` 只包含实体、值对象、领域规则和领域错误，不导入 Hertz、Kitex 或具体基础设施 SDK。
2. `app` 编排用例、事务与权限，只依赖本服务 repository 接口和 `internal` 的小接口。
3. repository 接口由使用它的服务定义；SQL 实现位于同一服务的 `repository/<provider>`，不放入公共 `internal` 包。
4. `internal` 只提供跨服务基础能力、接口和首批 adapter，不包含用户、文档、评论等业务概念。
5. Hertz/Kitex 生成代码和 handler 只做参数校验、DTO 映射、上下文传递和错误映射，不实现业务规则。
6. 一个服务不得导入另一个服务的 `internal` 包，也不得直接访问另一个服务的数据表。
7. `kitex_gen` 和 Hertz 生成文件不得手工修改；所有可重复生成步骤由 `scripts/codegen.*` 固化。

## 5. Internal 基础设施接口

以下代码表达接口边界，最终实现时可根据 Go 编译约束微调命名，但不得扩大业务层对具体 SDK 的依赖。

### 5.1 关系型数据库

数据库抽象仅覆盖关系型 SQL 的公共执行与事务能力。首个 adapter 是 PostgreSQL，底层使用 `database/sql` + `pgx` stdlib。

```go
package database

type Config struct {
    DSN             string
    MaxOpenConns    int
    MaxIdleConns    int
    ConnMaxLifetime time.Duration
    ConnMaxIdleTime time.Duration
}

type Provider interface {
    Name() string
    Open(ctx context.Context, cfg Config) (DB, error)
}

type Executor interface {
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
    QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type DB interface {
    Executor
    BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error)
    PingContext(ctx context.Context) error
    Close() error
}

type Tx interface {
    Executor
    Commit() error
    Rollback() error
}
```

约束：

- `Provider` 负责驱动注册、连接池创建、连接参数与健康检查，不执行 schema 迁移。
- repository 可以接收 `database.Executor`，从而同时工作于连接池与事务。
- SQL、扫描和方言差异属于服务自己的 `repository/<provider>`。
- 禁止在业务层类型中出现 `*sql.DB`、`pgxpool.Pool` 或 Provider 客户端。
- 事务必须由 application 层明确划定；不得在 repository 内部隐式开启跨步骤事务。

### 5.2 可靠消息与实时总线

可靠领域事件/任务与临时实时广播语义不同，必须拆成两个接口。

```go
package messaging

type Message struct {
    ID          string
    Subject     string
    ContentType string
    Headers     map[string]string
    Body        []byte
}

type PublishOptions struct {
    DeduplicationID string
}

type DurableBroker interface {
    Publish(ctx context.Context, msg Message, opts PublishOptions) error
    Subscribe(ctx context.Context, cfg ConsumerConfig, handler Handler) (Subscription, error)
    Close() error
}

type Delivery interface {
    Message() Message
    Attempt() int
    Ack(ctx context.Context) error
    Nack(ctx context.Context, delay time.Duration) error
    Term(ctx context.Context, reason string) error
}

type Handler func(context.Context, Delivery)

type Subscription interface {
    Stop(ctx context.Context) error
}

type RealtimeBus interface {
    Publish(ctx context.Context, subject string, payload []byte) error
    Subscribe(ctx context.Context, subject string, handler RealtimeHandler) (Subscription, error)
    Close() error
}
```

公共语义：

- `DurableBroker` 至少提供 at-least-once；handler 成功后显式 `Ack`。
- 可重试错误执行 `Nack`，退避和最大投递次数由 `ConsumerConfig` 配置。
- 不可重试消息执行 `Term`，adapter 将其写入约定的死信 subject，并保留失败原因元数据。
- handler 返回时仍未 ack/nack/term 的 delivery 按可重试失败处理，防止消息被静默确认。
- 消费者必须按 `Message.ID` 持久化去重，不能依靠 Broker 去重代替业务幂等。
- 领域状态与 Outbox 在同一数据库事务内提交；Broker publish 不参与业务数据库事务。
- `RealtimeBus` 不保证持久化、重放和送达，不能用于恢复文档历史或执行业务任务。

首个 adapter 使用 NATS JetStream 实现 `DurableBroker`，使用 NATS Core 实现 `RealtimeBus`。

### 5.3 缓存

```go
package cache

var ErrNotFound = errors.New("cache: key not found")

type KVStore interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
    SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
    Delete(ctx context.Context, keys ...string) error
    Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
    Ping(ctx context.Context) error
    Close() error
}
```

- 首个 adapter 是 Redis。
- key 必须由各服务使用固定命名空间构造，例如 `identity:`、`knowledge:`、`platform:`、`gateway:`。
- 缓存不得成为业务事实的唯一来源；Redis 故障时按各用例的一致性要求明确 fail-open 或 fail-closed。
- 不在通用接口中暴露 Redis Lua、Stream、Pub/Sub 或分布式锁。确有需要时定义由具体用例拥有的窄接口。

### 5.4 配置源

```go
package config

type Snapshot map[string][]byte
type ChangeHandler func(context.Context, Snapshot) error

type Source interface {
    Name() string
    Load(ctx context.Context) (Snapshot, error)
    Close() error
}

type WatchSource interface {
    Source
    Watch(ctx context.Context, onChange ChangeHandler) error
}
```

配置模块负责合并 Source、解码为服务私有的强类型 `Config`、执行校验并发布不可变快照。只有 Etcd 等动态来源实现 `WatchSource`，环境变量和本地文件不需要伪造 watch 能力。业务代码不得按字符串 key 到处读取全局配置。

配置优先级从低到高为：

1. 代码内安全默认值。
2. 本地非敏感配置文件，仅用于开发环境。
3. Etcd common 配置。
4. Etcd service 配置。
5. 环境变量或部署平台 Secret。
6. 命令行参数，仅用于运维显式覆盖。

高优先级只覆盖明确设置的字段。合并后的配置必须一次性完成严格解码和校验；未知字段、类型错误和缺少必填项都应阻止服务进入 ready。

### 5.5 注册与发现

Kitex 注册与发现直接使用 Kitex 原生 `registry.Registry` 和 `discovery.Resolver` 契约，不再包一层自定义接口。`internal/discovery/etcd` 只负责：

- 根据 bootstrap 配置创建 Etcd client。
- 构造 `github.com/kitex-contrib/registry-etcd` 提供的 registry 和 resolver。
- 统一 service name、环境前缀、租约、认证与 TLS 配置。
- 将 resolver 注入 Kitex client，将 registry 注入 Kitex server。

首批服务名固定为：

```text
knowledge-core.identity
knowledge-core.knowledge
knowledge-core.platform
```

调用方使用服务名发现实例，不保存容器 IP 或单实例地址。显式直连地址仅允许用于本地诊断，并且必须通过配置开启。

### 5.6 JSON Codec

```go
package json

type DecodeOptions struct {
    DisallowUnknownFields bool
    UseNumber             bool
}

type Codec interface {
    Marshal(v any) ([]byte, error)
    Unmarshal(data []byte, v any) error
    Decode(r io.Reader, v any, opts DecodeOptions) error
}
```

默认实现封装 `github.com/bytedance/sonic`：

- 普通 HTTP 响应和内部 DTO 使用 `sonic.ConfigDefault`。
- Hertz 的 `ctx.JSON`、JSON binding 和渲染保持默认 Sonic 路径。
- 配置和事件 envelope 同时启用 `DisallowUnknownFields` 与 `UseNumber`，既拒绝版本契约之外的字段，也避免无类型数字先转成 `float64`。
- 业务代码通过 `internal/codec/json` 或传输层 helper 使用 JSON，不直接散落 `sonic.Marshal`、`sonic.Unmarshal`。
- 生产构建不得使用将 Hertz JSON 实现切回标准库的 `stdjson` build tag。
- 所有外部 JSON DTO 必须有显式字段 tag；不得依赖未导出字段或 map 迭代顺序。

Thrift RPC 使用 Kitex 生成的协议编解码，不经过 JSON codec。

### 5.7 可观测性、健康与生命周期

- `observability` 以标准库 `log/slog` 为唯一日志核心，应用日志及 Hertz/Kitex 框架日志共享同一 JSON handler、等级和输出目标。
- 固定日志字段包含 `service`、`environment`、`component`、`event`；有上下文时自动补充 `request_id`、`trace_id`、`span_id` 和 `user_id`。密码、Token、DSN、API Key、正文及 payload 按字段名统一脱敏。
- HTTP access log 只记录方法、IDL 路由模板、状态码、耗时和响应大小；Kitex access log 只记录调用角色、服务、方法、耗时、结果及稳定业务码，不记录请求/响应 DTO。
- OpenTelemetry 使用 W3C `traceparent`、`tracestate` 和 baggage，在 Hertz 与 Kitex metadata 间传播。配置绝对 OTLP gRPC URL 时启用批量导出；endpoint 为空时使用 no-op provider，不建立外部连接。
- trace sampling 使用 `ParentBased(TraceIDRatioBased)`；入口默认采样率为 `1`，子调用遵循父 span 的采样决定。span attribute 不保存完整 URL、query、Token、正文或连接串。
- `health` 分离 `/health/live` 与 `/health/ready`；liveness 不依赖外部组件，readiness 检查本服务必要依赖和配置状态。
- `command` 使用 Cobra 构建每个独立服务的根命令，由根命令统一捕获 `SIGINT`/`SIGTERM` 并取消服务上下文；当前不增加 `serve` 或 `migrate` 子命令。
- `lifecycle` 统一执行 ready、serve、drain、transport shutdown 和 resource close。传输层 drain 与资源关闭使用独立超时，不允许 adapter、Hertz 或 Kitex bootstrap 自行捕获进程信号。
- Token、密码、DSN、API Key、事件敏感 payload 和完整用户正文不得进入日志、指标 label 或 trace attribute。

## 6. 配置与 Etcd 约定

### 6.1 Key 空间

```text
/knowledge-core/<env>/config/common
/knowledge-core/<env>/config/<service>
/knowledge-core/<env>/kitex/client/<caller>/<callee>
/knowledge-core/<env>/kitex/server/<service>
/knowledge-core/<env>/registry/<service>/...
```

- `<env>` 使用 `local`、`dev`、`staging`、`prod` 等部署环境名。
- `<service>` 使用 `gateway`、`identity`、`knowledge`、`platform`。
- `config/common` 只保存所有服务都需要的非敏感配置。
- `config/<service>` 保存服务专属的非敏感配置。
- `kitex/` 保存超时、重试、熔断、限流等治理参数。
- `registry/` 由注册发现 adapter 管理，应用不得手工写实例记录。

Etcd endpoint、Etcd 认证凭据和 TLS bootstrap 信息不能从 Etcd 自身获取，必须由环境变量、Secret 或本地 bootstrap 文件提供。

### 6.2 静态与动态配置

| 类别 | 示例 | 生效方式 |
|---|---|---|
| 静态 | Provider 类型、DSN/endpoint、凭据、监听地址、服务名、TLS 身份、Etcd bootstrap | 校验后启动，变更需滚动重启 |
| 动态 | RPC 超时/重试/熔断、限流阈值、日志级别、功能开关 | Etcd watch，校验成功后原子替换快照 |
| 业务数据 | 站点设置、AI Provider 选择、用户偏好 | 由 owner 服务持久化，不进入共享 `internal` 配置 |

动态更新必须先完整解析和校验，再以不可变快照一次替换。更新失败时继续使用最后一个有效版本并上报指标；不得留下部分字段已更新的状态。

### 6.3 Provider 配置示例

```text
KC_ENV=local
KC_DATABASE_PROVIDER=postgres
KC_DATABASE_DSN=<secret>
KC_CACHE_PROVIDER=redis
KC_CACHE_ENDPOINTS=localhost:6379
KC_MESSAGING_DURABLE_PROVIDER=nats
KC_MESSAGING_REALTIME_PROVIDER=nats
KC_NATS_URL=nats://localhost:4222
KC_CONFIG_PROVIDER=etcd
KC_ETCD_ENDPOINTS=http://localhost:2379
KC_SHUTDOWN_TIMEOUT=10s
KC_LOG_LEVEL=info
KC_OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
KC_OTEL_TRACE_SAMPLE_RATIO=1
```

所有服务使用同一字段命名规则，但每个进程只解析自己需要的配置。连接配置不得通过 RPC 或事件传播。

## 7. 显式依赖装配

不使用 Wire、Fx 或运行时服务定位器。每个进程在 `internal/bootstrap` 中使用普通构造函数完成装配：

```go
func Build(ctx context.Context, cfg Config) (*Application, error) {
    // 1. internal adapters
    // 2. repositories and RPC clients
    // 3. application services
    // 4. Hertz/Kitex handlers and consumers
    // 5. lifecycle and health checks
}
```

Provider 选择只允许出现在 bootstrap/factory：

```go
func NewDatabaseProvider(name string) (database.Provider, error) {
    switch name {
    case "postgres":
        return postgres.NewProvider(), nil
    default:
        return nil, fmt.Errorf("unsupported database provider %q", name)
    }
}
```

禁止在 domain、app、handler 或 repository 的业务方法中根据 Provider 名称执行分支。无法用公共能力表达的方言差异，放在对应 `repository/<provider>` 实现中。

## 8. 启动与退出顺序

### 8.1 启动

1. Cobra 根命令建立进程信号上下文；解析环境变量与 bootstrap 配置，初始化最小 stderr logger。
2. 创建 Sonic codec、正式 logger 和 OpenTelemetry providers。
3. 使用已校验的数据库配置执行本服务、本 Provider 的嵌入式 up migration。
4. 连接 Etcd，读取 common/service/Kitex 配置，严格解码并校验强类型配置。
5. 创建数据库、缓存、消息和对象存储等必要 adapter，执行 `Ping`。
6. 创建 Kitex registry/resolver、RPC clients、repositories 和 application services。
7. 创建 Outbox publisher、消息 consumers 和 Hertz/Kitex transport handlers。
8. 注册生命周期与健康检查；启动后台 worker 和 consumer。
9. 启动 Kitex server 或 Hertz server。Kitex 服务使用 Etcd registry 发布实例。
10. 所有必需依赖与监听端口成功后将 readiness 置为 ready。

服务启动先执行本服务、本 Provider 的嵌入式数据库 migration，再初始化其他基础设施和传输层。任一步失败都必须按已创建资源的逆序关闭并以非零状态退出；migration 失败时服务不得进入 serving 状态。

### 8.2 退出

1. 收到退出信号后立即将 readiness 置为 not ready。
2. 停止接收新 HTTP/RPC 请求并注销 Kitex 实例。
3. 在超时内完成在途请求；关闭 WebSocket 接入。
4. 暂停消息拉取，等待已开始的 handler 完成 ack/nack。
5. 停止 Outbox publisher 和后台 worker。
6. 逆序关闭 RPC clients、Broker、cache、database 和 Etcd client。
7. 使用独立资源关闭超时 flush trace exporter 后退出；JSON 日志直接写入进程 stderr，不维护异步缓冲区。

所有 drain 步骤必须有独立超时，不能因单个外部依赖失联无限阻塞。

## 9. Hertz HTTP 规则

- HTTP IDL 位于 `idl/http/v1/`，只描述公开 API、Studio API、健康接口及请求/响应 DTO。
- Hertz 生成内容保留在 `services/gateway/biz/`；手写 application/client/bootstrap 代码放在 `internal/`。
- `services/gateway/biz/router/router.go` 是唯一手写注册入口，按固定顺序安装全局 middleware 后调用 `GeneratedRegister`；URL 与 handler 映射仍以 IDL 生成路由为唯一事实来源。
- 路由级认证/角色策略通过生成器保留的 middleware 函数挂载；受保护 API 不得只依赖 handler 内部检查。
- handler 负责 binding、字段级校验、身份上下文提取、RPC DTO 映射和统一响应，不访问数据库。
- middleware 顺序固定为 recovery、request ID、trace、access log、CORS、安全头、限流、认证、授权前置检查。
- 统一响应与产品规格一致；文件流和 WebSocket upgrade 不套 JSON envelope。
- 读取请求体时设置大小上限；JSON binding 错误返回稳定网关错误码，不回显原始请求体。
- WebSocket 只由 gateway 终止，持久化操作必须经 `KnowledgeService` RPC 成功后才返回 ack。

## 10. Kitex RPC 规则

- RPC Thrift IDL 位于 `idl/rpc/v1/`，公共标量、分页和业务状态结构放在 `common.thrift`。
- 每个 owner 服务只维护自己的 service definition；跨域复用通过 IDL include，不复制结构。
- 生成代码统一放 `kitex_gen/`；生成的 handler 骨架迁入对应服务的 `transport/kitex` 后只做适配。
- Kitex server 使用 Etcd registry；Kitex client 使用 Etcd resolver 和稳定服务名。
- 每个 client 必须配置 deadline、连接超时、重试边界和熔断策略。非幂等调用默认不自动重试。
- metadata 使用 persistent metainfo 传递 `x-request-id`、W3C trace/baggage、调用方身份和必要认证上下文；原始 Token 不得写入日志。
- 业务异常使用 Kitex `BizStatusError` 映射稳定错误码；基础设施错误在边界处转换，不泄漏 SQL、地址或堆栈。
- IDL 字段只追加不复用编号，删除字段时保留编号；破坏性变更新建版本或新方法。

## 11. 代码生成

`scripts/codegen.ps1` 与 `scripts/codegen.sh` 必须执行相同版本、相同参数的生成流程；两个脚本都通过 `--check`（PowerShell 为 `-Check`）在生成前后比较文件快照：

1. 校验 `hz`、`kitex` 和 Thrift 插件版本。
2. 从 `idl/http/v1/` 生成 Hertz model、handler 和 router。
3. 从 `idl/rpc/v1/` 生成 `kitex_gen/` 客户端和服务端契约。
4. 执行格式化，并检查生成结果没有超出约定目录。
5. CI 重新生成并比较生成目录快照，防止 IDL 与生成代码不一致，同时不受工作区中其他未提交修改影响。

生成器版本通过仓库工具依赖或脚本常量固定。生成代码评审关注 IDL 差异，禁止以手工修改生成文件修复问题。

## 12. 数据迁移规则

数据库迁移按 owner 服务和 Provider 双层隔离：

```text
migrations/<service>/<provider>/<version>_<name>.up.sql
migrations/<service>/<provider>/<version>_<name>.down.sql
```

例如：

```text
migrations/identity/postgres/000001_create_users.up.sql
migrations/identity/postgres/000001_create_users.down.sql
```

- 每个服务只能执行自己的目录，Provider 必须与运行时数据库 Provider 一致。
- 每条迁移提供配对的 up/down；不可逆变更必须在评审中显式说明恢复方式。
- migration SQL 通过服务所属的 Go package 嵌入二进制；服务启动时自动执行当前 Provider 的全部 up migration。
- 自动迁移必须在 repository、消息消费者和网络监听启动前完成；失败或 dirty version 必须阻止服务启动。
- 多实例并发启动依赖数据库 migration driver 的互斥锁串行执行，不自行实现分布式锁。
- 新 Provider 必须建立自己的完整 migration 链，不能直接假设 PostgreSQL migration 可运行。
- 跨版本发布遵循 expand/migrate/contract，先兼容旧代码，再回填数据，最后移除旧结构。

## 13. Provider 切换边界

### 13.1 仅更换连接

同一 Provider 内迁移到新实例或集群时，业务代码和 repository 不变。流程为：

1. 验证新集群版本、schema、账号权限和网络。
2. 执行同一 Provider migration，并完成数据同步或恢复。
3. 更新 Secret/连接 endpoint。
4. 分批滚动重启，观察 readiness、错误率、连接池与复制延迟。
5. 完成一致性校验后下线旧连接。

### 13.2 更换 Provider

例如 PostgreSQL 切换到另一种关系型数据库时，接口可以保护 application/domain，但仍必须完成：

1. 实现 `database.Provider` adapter。
2. 为各 owner 服务实现或验证 `repository/<provider>` SQL 方言。
3. 建立 `migrations/<service>/<provider>/` migration 链。
4. 设计全量复制、增量追平、校验和回滚窗口。
5. 运行 adapter contract tests、repository integration tests 和业务回归测试。
6. 更新 Provider 配置并滚动重启。

MQ 切换同理：新 adapter 必须证明 at-least-once、ack/nack、重投、死信、顺序边界和 header 映射符合公共契约。切换期间需要双写或桥接时，应作为独立迁移方案，不能塞入通用接口。

## 14. 验证门槛

当前基础框架已覆盖 Cobra 命令上下文、生命周期关闭顺序、日志脱敏、HTTP/Kitex trace 父子关系、Gateway 路由门面、认证/角色策略、配置、Sonic codec、健康检查、adapter 边界和 Kitex Ping handler 的单元测试。进入业务实现后还必须补齐：

日常开发统一使用根目录 Makefile：`make fmt` 修改格式，`make fmt-check`、`make vet`、`make lint` 和 `make test` 分别执行检查，`make check` 执行完整本地质量门槛，`make ci` 额外验证 Hertz/Kitex 生成代码没有漂移。`make line` 仅作为 `make lint` 的兼容别名。golangci-lint 由固定版本的 `go run` 调用，不在仓库内维护工具二进制。

Identity 使用 `KC_DATABASE_DSN` 在启动阶段自动应用嵌入二进制的 PostgreSQL migration；成功后才继续初始化 repository 和 Kitex server。migration 不属于通用 Makefile 目标。

- 每个共享 `internal` adapter 的 contract test，使用同一套用例验证接口语义。
- 每个 repository Provider 的真实数据库 integration test，覆盖事务回滚、唯一约束、分页和锁冲突。
- DurableBroker 测试覆盖重复投递、Nack 重投、最大次数、死信和 consumer 重启。
- Config 测试覆盖优先级、未知字段、动态更新失败保留旧快照和 Secret 不进入 Etcd。
- Hertz 测试覆盖 Sonic binding、统一错误、body 限制和中间件顺序。
- Kitex 测试覆盖 IDL 兼容、BizStatusError、deadline、metadata 和服务发现。
- 启停测试覆盖部分初始化失败的逆序清理和优雅退出超时。

在 contract tests 通过前，不得将新 adapter 标记为可用于生产切换。

## 15. 参考资料

- [Kitex 概览](https://www.cloudwego.io/zh/docs/kitex/overview/)
- [Hertz 文档](https://www.cloudwego.io/zh/docs/hertz/)
- [Kitex Etcd Registry](https://github.com/kitex-contrib/registry-etcd)
- [Sonic](https://github.com/bytedance/sonic)
