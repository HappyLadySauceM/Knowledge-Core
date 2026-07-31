# Knowledge Core 微服务框架设计

> 状态：框架 v0.1。本文描述公共运行时契约以及 Identity 首期纵向样例的边界。Identity 首期只交付 `Ping` 与 `Register`；IDL 中保留的 `Authenticate`、`GetUser` 返回稳定的未实现错误 `20009`。NATS 公共能力可以独立演进，但 Identity 首期不注册、不连接 NATS。

## 1. 目标与边界

Knowledge Core 使用单 Go module Monorepo。外部 HTTP 服务使用 Hertz，内部同步 RPC 使用 Kitex + Thrift，JSON 编解码统一使用 Sonic，关系数据库访问统一使用 GORM。

框架的职责是提供可复用的配置、日志、追踪、错误、健康、指标、资源连接和进程生命周期，不把业务概念放入公共包。每个服务仍拥有自己的配置扩展、领域模型、repository、logic 和传输适配。

首期明确不做以下工作：

- 不实现 Identity 的登录、Token、刷新令牌和用户查询业务；对应 RPC 返回 `identity.unimplemented`。
- 不让 Identity 因为仓库中存在 NATS option 或 adapter 就创建 NATS 连接。
- 不实现跨服务数据库事务或 exactly-once 消息语义。
- 不执行动态配置热更新；连接与 Provider 变化通过滚动重启生效。
- 不由公共库调用 `os.Exit` 或保存全局可变配置单例。应用服务不得依赖 panic 表达业务失败；`pkg/app` 只在组件 goroutine 边界把生命周期 panic 转为错误，Hertz 边界只负责恢复失控的 handler panic 并返回安全的 500。

## 2. 目录和职责

```text
pkg/
  app/        命令注册、组件编排、信号、ready/drain/close 生命周期
  option/     可跨服务复用的配置类型、默认值和校验
  log/        基于 log/slog 的结构化日志与框架适配
  error/      稳定错误定义、cause 链和 Kitex 边界映射
  trace/      OpenTelemetry provider、W3C 传播和传输层 instrumentation
  metadata/   request_id、trace_id 等上下文字段
  health/     liveness/readiness 注册表
  metrics/    独立 Prometheus registry、应用状态与传输指标中间件
  transport/  Kitex 等框架的可复用安全传输策略
  postgres/   GORM/PostgreSQL 连接与生命周期
  redis/      go-redis 连接、观测与生命周期
  etcd/       Etcd 注册发现、健康检查与注册生命周期保护
  nats/       NATS JetStream/Core NATS 能力（Identity 首期不使用）

services/identity/
  main.go                 只创建并运行 app 命令
  internal/config/        公共 option 组合与 Identity 私有 option
  internal/context/       ServiceContext，显式持有服务依赖
  internal/domain/        领域对象与校验
  internal/logic/         按用例组织的具体业务逻辑
  internal/repository/    持久化契约及 PostgreSQL/GORM 实现
  internal/migration/     Identity schema 的启动迁移和校验
  internal/transport/     Kitex 与 Hertz admin 边界
```

公共包不得反向引用 `services/*`。业务层优先依赖本服务定义的小接口，不直接依赖 Redis、Etcd、NATS 或 GORM 的全局实例。

## 3. 应用注册与依赖注入

`pkg/app` 通过泛型 `Spec[C]` 注册一个应用：

```go
type ConfigLoader[C any] interface {
    AddFlags(*pflag.FlagSet)
    Load(context.Context, *cobra.Command) (C, error)
}

type Spec[C any] struct {
    Name           string
    Config         ConfigLoader[C]
    RuntimeOptions func(C) (RuntimeOptions, error)
    Register       func(context.Context, C, *Runtime) error
}
```

服务入口直接把 Identity 的 config provider、公共 runtime options 和 `NewServiceContext` 注册到 `app.Spec`，再执行 Cobra command。`os.Exit` 只允许出现在进程入口，启动期错误通过 JSON bootstrap logger 输出，公共库通过 `error` 报告失败。Identity 不再额外维护一个只做转发的 `internal/bootstrap` 包。

`Runtime` 是进程级编排器，不是任意取用依赖的服务定位器。它只暴露 logger、trace、health、metrics，以及 `AddComponent`、`AddCleanup` 等生命周期入口。Identity 自己的 GORM DB、Redis、Etcd、repository 与应用服务由 `NewServiceContext` 显式构造并保存：

长运行组件必须实现完整的 `Name`、`Serve`、`Ready(ctx)`、`Shutdown(ctx)` 契约；仅启动 goroutine 不等于 ready，组件必须在 `Ready` 中证明 transport 和外部注册均已可用。

```text
Config
  -> logger / OTel / metrics
  -> PostgreSQL(GORM) -> migration -> UserRepository
  -> Redis
  -> Etcd registry
  -> Identity register logic
  -> Kitex RPC + Hertz admin components
```

构造函数返回错误，不使用 `Must*`、panic 或脱离父上下文的后台启动。配置也不再通过 `sync.Once` 或 package global 暴露；测试可为每个用例构造独立 Config 和 ServiceContext。

## 4. 公共配置模型

`pkg/option` 只放多个服务都能复用的配置，例如：

- `AppOptions`：服务名、环境和关闭超时。
- `LogOptions`、`TraceOptions`：日志等级、OTLP endpoint、采样率和传输安全。
- `KitexServerOptions`、`HertzServerOptions`：监听地址、超时和服务标识。
- `PostgreSQLOptions`、`RedisOptions`、`EtcdOptions`、`NATSOptions`、`TLSOptions`：连接、池和 TLS 参数。

Identity 私有值只在 `services/identity/internal/config/option.go` 定义，例如 bcrypt cost。`config.Config` 使用具名字段组合公共 option，避免匿名嵌入造成字段冲突，也避免把未来服务的配置强加给 Identity。

每个 command 使用独立的 Cobra flag set 和 Viper 实例。CLI 只接受必填的 `--config`，不为每个配置字段重复注册 flag。配置优先级固定为：

```text
默认值 < YAML 配置文件 < 环境变量
```

Identity 环境变量固定使用 `IDENTITY_` 前缀，并把配置路径转为大写下划线，例如 `app.environment` 对应 `IDENTITY_APP_ENVIRONMENT`、`postgres.password` 对应 `IDENTITY_POSTGRES_PASSWORD`、`rpc.tls.cert_file` 对应 `IDENTITY_RPC_TLS_CERT_FILE`。

加载器只接受 YAML，并严格拒绝未知字段。它不会在解析前替换 `${ENV}`，从而避免带有 `: `、`#`、引号或 `$` 的 Secret 改写 YAML 语法。显式存在的空环境变量仍高于配置文件；切片环境变量使用逗号分隔，`trace.headers` 使用 JSON object。密码、DSN、Token、私钥和鉴权 header 只从环境变量或部署平台 Secret 注入，日志中不得输出其值。

`services/identity/etc/config.yaml` 是 Identity 自己的非敏感配置文件，不包含 Secret 占位符。仓库不提供共享 `.env.example`；本地 shell、容器编排或部署平台按 `IDENTITY_*` 规则注入 Secret。其他服务必须在自己的 `services/<service>/etc/config.yaml` 中只组合实际使用的配置，不能依赖根级共享配置。

从仓库根目录启动 Identity 时必须显式指定服务配置：

```powershell
go run ./services/identity --config services/identity/etc/config.yaml
```

## 5. 日志、追踪与错误

### 5.1 日志

`pkg/log` 以标准库 `log/slog` 为唯一日志核心并固定输出 JSON。进程字段至少包含 `service`、`environment`，事件字段使用低基数的 `component`、`event`、`method`、`status` 等名称。

从 context 记录日志时自动补充可用的 `request_id`、`trace_id` 和 `span_id`。password、authorization、token、cookie、dsn、secret、api key 等敏感字段必须递归脱敏；不得记录完整请求/响应 DTO、SQL 参数或消息 payload。

### 5.2 OpenTelemetry

`pkg/trace` 管理进程唯一的 `TracerProvider`、W3C TraceContext/Baggage propagator 和 exporter 生命周期：

- OTLP gRPC endpoint 非空时使用 batch exporter。
- endpoint 为空时不建立外部连接，但仍生成有效的本地 trace/span ID。
- 采样使用 `ParentBased(TraceIDRatioBased)`，子调用遵循父 span 决定。
- Hertz、Kitex、GORM 与 Redis 从同一 context 延续 span；Redis instrumentation 禁止记录命令参数，NATS adapter 通过 message headers 延续同一 W3C 上下文。

`request_id` 标识单次入口请求，`trace_id` 标识跨组件链路；两者不能互相替代。外部调用方使用标准 `traceparent` 续接链路，不信任客户端自报的日志字段。

### 5.3 错误

`pkg/error`（导入时建议别名 `apperror`）区分：

- 数字业务码；
- 稳定的机器可读 key；
- kind（invalid、conflict、not_found、unavailable、internal 等）；
- 可安全返回的 message；
- 只留在服务内部的 cause。

错误对象不保存创建时的 trace 快照。Kitex/Hertz 边界在返回时从当前 context 加入 `request_id`、`trace_id` 等字段。未知错误统一映射为内部错误，原始 SQL、连接地址、堆栈和 cause 只进入脱敏日志。

Identity 错误码域为 `2xxxx`。`CodeUnimplemented = 20009` 对应稳定 key `identity.unimplemented`；`Authenticate` 与 `GetUser` 首期始终使用该错误，不伪造空成功响应。

### 5.4 Prometheus 指标

每个服务进程持有独立的 Prometheus registry，并在自己的 Hertz 管理端口固定暴露 `/metrics`。Identity 默认地址为 `http://127.0.0.1:8081/metrics`。该端点与 `/livez`、`/readyz` 共用管理 listener，但不得通过公网 ingress 暴露；生产环境由集群网络策略、防火墙或独立管理网络限制访问。

公共 registry 默认提供：

- `knowledge_core_app_info` 与 `knowledge_core_app_ready`；
- Go runtime、process 和 `database/sql` 连接池指标；
- Hertz 请求数量、耗时和并发数；
- Kitex 请求数量、耗时、并发数、结果类别和稳定业务码；
- Redis 连接池使用量、等待、命中、超时和连接回收指标。

指标标签只能使用服务名、环境、版本、路由模板、RPC 方法、状态码和稳定依赖名等有界值。request ID、trace ID、用户 ID、原始 URL、错误文本、数据库地址、Redis key 和消息正文禁止作为标签。`/metrics` 自身不创建 trace、不进入普通 HTTP 指标，也不写 access log；抓取状态由 `promhttp_metric_handler_*` 指标单独记录。Prometheus 不可用不会影响应用启动、readiness 或业务请求。

## 6. 传输与序列化

业务 RPC IDL 位于 `idl/rpc/v1`，生成代码统一提交到 `kitex_gen/`。Kitex 服务使用 TTHeader，以便传播 persistent metadata、BizStatusError、deadline 和 OTel context。非幂等 RPC 默认不自动重试。启用客户端 TLS/mTLS 时通过 `pkg/transport/kitex.ClientOptions` 同时安装 TLS dialer 与标准 Go transport，避免 Linux netpoll 直接处理 `tls.Conn` 的不兼容组合。

Hertz 承载外部 HTTP 或运维接口。Identity 首期 admin listener 提供：

- `/livez`：仅判断进程与 listener 是否存活；
- `/readyz`：判断 serving 状态及 PostgreSQL、Redis、Etcd 等必需依赖；
- `/metrics`：暴露独立 Prometheus registry。

HTTP JSON 必须经 Sonic 统一 codec。严格入口解码拒绝未知字段、多余 JSON 值和不合法数字；不得通过 `stdjson` build tag 或 `PureJSON` 绕过统一实现。Thrift RPC 使用 Kitex 生成的协议 codec，不经过 JSON。

## 7. 数据库与启动迁移

PostgreSQL 通过 GORM 打开，注册官方 OpenTelemetry tracing plugin，并取得底层 `sql.DB` 配置最大连接数、空闲连接数、连接最大生命周期和空闲时间。`pkg/metrics.RegisterDBStats` 将连接池饱和度、等待和连接 churn 注册到服务独立的 Prometheus registry。每次 repository 查询都使用 `db.WithContext(ctx)`，使 deadline、取消和数据库 span 与请求链路一致。GORM 日志保持参数化，tracing plugin 使用 `WithoutQueryVariables()`，不得把 SQL 参数写入日志或 span。Redis 使用 redisotel tracing 且关闭 command statement 采集；连接池 collector 直接注册到当前服务的 Prometheus registry，并随 Redis resource 注销。

Identity 在网络 listener 启动前执行 `AutoMigrate`：

1. 获取 PostgreSQL advisory transaction lock，保证多实例启动时只有一个迁移执行者。
2. 创建 `identity` schema 并在该 schema 内执行 GORM `AutoMigrate`。
3. 用显式 SQL 补齐 GORM 无法可靠表达的大小写不敏感唯一索引和命名 check constraints。
4. 查询 PostgreSQL catalog 校验索引、约束及定义，而不只判断同名对象是否存在。
5. 迁移失败则回滚、关闭已打开资源，服务不得进入 ready。

AutoMigrate 只用于首期已选定的增量策略，不删除废弃列，也不等价于完整的版本化 migration 系统。后续破坏性 schema 演进必须采用 expand/migrate/contract，并提供独立备份与回滚方案。

注册流程先规范化用户名和邮箱，校验密码字节长度，使用配置的 bcrypt cost 生成 hash，再由 GORM repository 写入。repository 根据 PostgreSQL constraint name 将用户名或邮箱唯一冲突映射为安全的 conflict 错误；响应永远不包含 password hash。

## 8. 资源与生命周期

资源按成功创建顺序注册 cleanup，失败或退出时逆序关闭。启动流程为：

1. 加载并校验配置。
2. 初始化 slog、OpenTelemetry、health 和 metrics。
3. 打开 PostgreSQL/GORM，Ping 并执行 Identity migration。
4. 打开并 Ping Redis。
5. 创建 Etcd client 与 Kitex registry。
6. 构建 ServiceContext、repository、logic 和 transport。
7. 在 ServiceContext 装配阶段使用标准 transporter 预绑定 Hertz listener，并预绑定 Kitex listener；Runtime 启动组件后等待 Hertz 进入 running 状态以及 Kitex 的 Etcd 注册成功屏障，所有组件都确认 ready 后才发布进程 ready。组件 `Serve`/`Ready`/`Shutdown` panic 都在生命周期边界转换为带 stack 的错误并进入统一回滚。

退出或任一 component 意外结束时：

1. 立即将 ready 设为 false。
2. 逆序停止 component：先让 Kitex 注销并停止接收请求；此时 Hertz admin 仍可响应且 `/readyz` 返回 503，随后再停止 admin listener。
3. 在统一 shutdown timeout 内等待在途请求；Identity 配置校验要求 `app.shutdown_timeout >= rpc.exit_wait_timeout + http.shutdown_timeout`。
4. 逆序关闭 Etcd、Redis、数据库等资源。
5. 最后 flush OpenTelemetry provider。

每个 cleanup 只执行一次，所有等待都有上限；预期的 `context.Canceled`、listener close 不作为异常启动失败上报。Runtime 会跟踪每个 `Serve` 的真实退出；若关闭超时后仍有组件运行或 `Shutdown` 尚未返回，则进程返回致命生命周期错误且不释放其依赖资源，避免在途请求访问已经关闭的数据库或 Redis。资源由随后的进程退出交还操作系统回收。

## 9. NATS 边界

`pkg/nats` 直接提供 JetStream durable 与 Core NATS realtime 的具体客户端、Delivery 和 Subscription 类型，不再声明能够替换任意消息中间件的伪通用接口。持久消息语义是 at-least-once、显式 ack/nack/term、幂等 message ID、有限重投、死信和 drain；trace 使用 `nats.Msg.Header` 传播 W3C headers。

这只是框架能力，不是所有服务的强制依赖。Identity 首期没有领域事件 publisher 或 consumer，因此其 Config 不注册 NATS option，ServiceContext 不持有 NATS client，启动和 readiness 也不检查 NATS。只有具体消息用例落地后才能把该连接加入对应服务。

## 10. 契约生成与兼容检查

代码生成工具固定为：

- Kitex `v0.16.2`
- Hertz `v0.9.7`
- thriftgo `0.4.5`

本机未安装固定版本时可执行：

```bash
go install github.com/cloudwego/kitex/tool/cmd/kitex@v0.16.2
go install github.com/cloudwego/hertz/cmd/hz@v0.9.7
go install github.com/cloudwego/thriftgo@v0.4.5
```

Windows：

```powershell
./scripts/codegen.ps1
./scripts/codegen.ps1 -Check
```

Linux/macOS：

```bash
bash ./scripts/codegen.sh
bash ./scripts/codegen.sh --check
```

默认命令通过 `scripts/idlguard services` 解析 AST，发现 `idl/rpc/v1` 下所有包含 service 的 Thrift 文件，清理生成器拥有的 `kitex_gen` 后重新生成。`scripts/generated-files.txt` 是当前 RPC 生成输出的唯一清单；`-Check`/`--check` 在临时目录重新生成并比较 SHA-256，不修改工作树。

`idlguard compat` 比较两个 IDL 目录，`idlguard compat-git` 可直接与 Git revision 比较。删除/重命名字段、改变编号/类型/requiredness、增加 required 字段、删除方法、改变返回值或 `api.*` annotation 都属于不兼容变更。

脚本保留 `-IncludeHertz`/`--hertz` 更新入口。当前 Gateway 业务生成骨架尚未纳入本次交付，脚本在 `services/gateway/biz/router/register.go` 不存在时会拒绝执行 `hz new`，防止生成器覆盖 `main.go`、`go.mod` 或并行开发的手写代码。Gateway transport 落地后，应先明确 Hertz model/router/handler 的所有权清单，再把其输出加入同一 drift check。

## 11. 部署与安全基线

Identity Dockerfile 使用多阶段构建、`CGO_ENABLED=0`、`-trimpath` 和无 shell 的 distroless nonroot 运行时。镜像把非敏感配置复制到 `/etc/knowledge-core/identity/config.yaml`，默认 CMD 显式传入该路径；生产可以用 ConfigMap 覆盖文件。容器暴露 RPC `8881` 与 admin `8081`，实际监听地址仍由配置决定。

数据库、Redis、Etcd 和 OTLP 认证材料必须通过环境变量或 Secret manager 注入，不写入 YAML、示例文件或镜像层。TLS/mTLS 配置一旦声明就必须完整校验，证书加载失败时启动失败，禁止静默降级到明文。

日志、metric label 和 span attribute 不得包含用户 ID、trace ID 以外的无界高基数字段，不得包含 password、hash、Token、DSN、SQL 参数或消息正文。

本地 Prometheus 由 infrastructure Compose 启动：

```powershell
$env:KC_POSTGRES_PASSWORD = "local-development-password"
docker compose -f docker/infrastructure/docker-compose.yml up -d
$env:IDENTITY_POSTGRES_PASSWORD = $env:KC_POSTGRES_PASSWORD
go run ./services/identity --config services/identity/etc/config.yaml
```

Prometheus 通过 `host.docker.internal:8081` 抓取宿主机上的 Identity；Compose 显式配置 `host-gateway` 以兼容 Linux Docker。启动 Identity 后，可在 `http://127.0.0.1:9090/targets` 确认 `knowledge-core-identity` 为 `UP`。本地 TSDB 默认保留 15 天；删除 named volume 才会清除历史指标。
