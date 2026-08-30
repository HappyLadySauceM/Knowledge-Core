# Knowledge Core Agent 协作规范

本文适用于整个仓库（Go module + Rust workspace，六个服务：Gateway、Identity、Knowledge、Attachment、Collaboration、Platform）。详细架构、运行时契约和当前能力以 [`docs/framework-design.md`](docs/framework-design.md) 为准，Rust Collaboration 的设计基线见 [`docs/rust-collaboration-design.md`](docs/rust-collaboration-design.md)；本文只记录编码 Agent 必须遵守的强约束。不要把规划中或 IDL 中尚未落地的能力描述为已经实现。

## Git 与分支

- `dev` 是唯一开发分支。开始修改前先确认当前分支是 `dev`；若不是，应切换到已有的 `dev`，切换可能覆盖未提交改动时立即停止并报告。
- 所有代码、测试、文档和修复都直接在 `dev` 上完成。禁止创建任何其他本地或远端开发分支，包括 `feature/*`、`fix/*`、`chore/*` 和 `release/*`。
- `main` 对开发者只读：禁止在 `main` 上修改、提交或手工 push，也禁止绕过质量门禁。自动 promotion 只能由 `.github/workflows/pipeline.yml` 在 dev 部署冒烟、DeepSeek 摘要和 compare-and-swap 门禁全部通过后执行；本项目明确不启用镜像扫描和签名。
- 禁止 force-push、改写或 rebase 共享的 `dev`、`main` 历史。未来的发布自动化只允许 fast-forward `main`，随后为同一 SHA 创建版本 tag 与 GitHub Release；发生分叉或 ref 已推进时必须停止，不得自行丢弃提交。
- 已存在的历史 feature 分支不再使用，也不得删除、重命名或改写。
- 提交只能落到 `dev`；是否执行 commit、push 或 GitHub 操作，仍须以当前任务的明确授权为准。

## 架构边界

- `pkg/` 只承载与业务无关、可跨服务复用的能力，绝不能导入 `services/*`。服务私有配置、领域模型、用例和传输适配必须留在对应的 `services/<service>/`。
- 服务保持 `main/spec -> internal/config -> internal/context -> domain/logic/repository/transport` 的装配方向。`main.go` 只创建并执行应用命令，`spec.go` 只声明应用规格，不放业务逻辑。
- `ServiceContext` 显式构造并持有服务依赖。`pkg/app.Runtime` 只负责进程级日志、追踪、健康、指标与生命周期编排，不得被扩展为服务定位器。
- 禁止全局 Viper/pflag、可变 package singleton、隐藏式依赖注入和跨包随意取用数据库、Redis 或 NATS client。
- 业务逻辑依赖本服务定义的小接口；repository 负责持久化、事务和存储错误映射；transport/handler 只负责协议校验、调用编排与安全的错误映射，不承载领域规则。

### Identity

- `internal/domain` 持有领域对象与校验；`internal/model` 只持有 GORM 持久化模型；两者不得泄漏到不属于它们的边界。
- `internal/logic` 按用例组织业务行为并依赖小型 repository/security 接口；`internal/repository` 使用 `db.WithContext(ctx)` 实现持久化和领域/存储模型转换。
- Kitex RPC 与 Hertz admin 只做传输适配、观测和生命周期管理，不在 handler 中复制业务规则。

### Gateway

- Gateway 是 HTTP edge：负责严格请求校验、鉴权、安全中间件、限流、公开错误映射和对上游 typed client 的调用编排；不得直连任何业务服务数据库或复制其领域规则。
- `internal/client` 封装 Identity、Knowledge、Collaboration、Attachment、Platform RPC，`internal/middleware` 承载跨路由安全策略，`internal/context` 显式装配 Redis、上游 client 与 HTTP components。
- Gateway handler 与 route middleware 的所有权以生成清单为准；文件头中的 generated 注释不能覆盖 `scripts/generated-files.txt` 的判定。

### Knowledge

- Knowledge 拥有文档元数据、文件夹、成员权限、发布/公开投影、回收站、配额和 outbox，并在迁移窗口内保留旧文档附件兼容能力；不拥有通用附件对象，也不保存 Yjs 二进制状态。数据在 PostgreSQL `knowledge` schema，SQL 迁移放 `internal/migration/migrations/`。
- `internal/domain`（document、richtext）与 `internal/model`（GORM）边界同 Identity；`internal/logic` 依赖小型 repository 接口，`internal/repository` 负责持久化、幂等与存储错误映射，`internal/storage`（S3）、`internal/scanner`（ClamAV）、`internal/worker` 承载附件与后台任务。
- 通过 `internal/client` 调用 Identity 与 Collaboration RPC；`internal/transport/rpc` 只做传输适配，不在 handler 中复制业务规则。

### Attachment

- Attachment 拥有通用文件元数据、multipart 上传、扫描状态、引用与回收生命周期，数据在 PostgreSQL `attachment` schema，对象字节位于 S3/MinIO，并使用 ClamAV 扫描。
- 不复制文档权限或 Collaboration 领域规则；Gateway 通过 typed RPC 调用 Attachment，业务服务只通过稳定契约引用附件。

### Collaboration

- Rust workspace（`services/collaboration/`）：Volo Thrift RPC（`:8883`）与 WebSocket（`:8091`，y-sync + awareness，Yrs CRDT）；`src/generated/`（mod.rs、volo_gen.rs）完全由生成器拥有，禁止手改。
- 拥有 PostgreSQL `collaboration` schema（Yjs update、snapshot、version、projection job、outbox）；依赖 Redis、NATS 与 Knowledge RPC。不复制文档权限规则：创建 session 时经 Knowledge RPC 复核访问级别，ticket 短期、单次使用。
- Rust 门禁：rustfmt、clippy 必须 `-D warnings`、`cargo deny`（advisories/bans/licenses/sources）；工具链固定在 `rust-toolchain.toml`（1.97.1）。
- Node 互操作 fixture 在 `services/collaboration/interop/`（yjs ↔ yrs，`npm run ci` = format:check + `node --test` + audit），不属于 Go 包发现范围（GO_PACKAGES）。

### Platform

- Platform 拥有带修订控制的站点、邮件和 AI 业务配置、敏感值加密、审计、幂等与 outbox，数据在 PostgreSQL `platform` schema，并通过 NATS 发布只含坐标的可靠变更事件。
- 不拥有进程监听地址、证书或连接池等静态运行时配置；这些仍由各服务配置和部署平台管理。

## Go、依赖与调用链

- 构造函数校验必需依赖并返回 `(T, error)`；不要新增 `Must*`、用 panic 表达业务失败，或在公共包中调用 `os.Exit`。`os.Exit` 只允许出现在进程入口。
- 错误必须提供操作上下文并使用 `%w` 保留 cause；用 `errors.Is/As` 判断。跨 Kitex/Hertz 边界使用 `pkg/error` 的稳定 code/key/kind 契约，未知内部错误不得向外泄漏 SQL、地址、堆栈或 cause。
- `context.Context`、deadline、request ID 和 trace 必须从入口贯穿 logic、repository、RPC、数据库与缓存调用。不得用 `context.Background()` 逃避请求取消；仅允许在有明确超时的 shutdown/cleanup 边界使用独立 context。
- 每个 goroutine 必须有明确 owner、停止条件、错误回收和等待路径；禁止无边界后台 goroutine。
- HTTP JSON 统一使用 `pkg/codec/json`。入口必须严格拒绝未知字段、多余 JSON 值和非法数字；禁止用 `encoding/json`、`PureJSON` 或 `stdjson` build tag 绕过公共 codec。
- 优先复用 `pkg/auth`、`pkg/error`、`pkg/log`、`pkg/metadata`、`pkg/trace`、`pkg/metrics` 等现有契约，不得在服务内创建语义相同但不兼容的旁路实现。
- 新增或升级依赖必须有明确必要性；随后执行 `make tidy`，并审查 `go.mod`、`go.sum`，不得夹带无关升级。

## 安全与可观测性

- Secret 只能由环境变量或部署平台 Secret manager 注入。密码、DSN、JWT、私钥、Authorization/Cookie、API key 不得写入 YAML、源码、测试快照、日志、metric label 或 span attribute。
- 日志统一走 `pkg/log` 与 `log/slog`，使用传入的 context 和稳定、低基数字段；不得记录完整 DTO、SQL 参数、Redis key 或消息 payload。
- metric label 只能使用路由模板、RPC 方法、状态码、稳定业务码和依赖名等有界值；禁止用户 ID、request/trace ID、原始 URL、错误文本等高基数值。
- 外部请求的 request ID 与 W3C trace context 必须按公共 middleware 规则处理；不得信任客户端自报的日志身份字段。
- JWT 校验、身份复核、可信代理判断和限流等安全决策在依赖异常时必须 fail closed，不能为保持可用性静默放行。

## 资源与生命周期

- 资源成功创建后立即调用 `Runtime.AddCleanup`；注册失败时同步关闭刚创建的资源。cleanup 只能执行一次，并按注册逆序运行。
- Go 长运行服务必须完整实现 `Name`、`Serve`、`Ready(context.Context)`、`Shutdown(context.Context)`。启动 goroutine 不等于 ready；`Ready` 必须证明 listener 和必要的外部注册确实可用。
- component 按逆序关闭，readiness 必须先转为不服务，再停止入口并等待在途请求，最后关闭依赖和 flush telemetry。
- 所有启动、readiness、shutdown 和 cleanup 等待都必须有上限。组件尚未安全退出时不得提前关闭其数据库、缓存或 registry 依赖。

## IDL 与生成代码

- 契约源是 `idl/http/v1/gateway.thrift` 与 `idl/rpc/v1/*.thrift`（identity、knowledge、collaboration、platform、common）；生成输出不是契约源。
- `scripts/generated-files.txt` 是生成器所有权的唯一清单。清单内文件禁止手改，`kitex_gen/` 与 `services/collaboration/src/generated/` 完全由生成器拥有；Gateway handler 和未列入清单的 route middleware 保持手写/混合所有权。
- 生成只通过仓库脚本或 `make generate` 完成（`codegen.sh`/`codegen.ps1`，`-Scope Go|Rust` 可分别检查）。Hertz 只允许 `hz update`，严禁 `hz new`；使用仓库固定的 Kitex `v0.16.2`、Hertz（hz CLI）`v0.9.7`、thriftgo `0.4.5`、rustc `1.97.1`（`rust-toolchain.toml`）。
- IDL 变更必须同时重新生成、审查 wire/API 兼容性，并额外执行：

  ```text
  go run ./scripts/idlguard compat-git <merge-base> idl
  ```

  `make ci` 当前不包含该兼容检查。非兼容契约变更必须先获得明确批准并记录迁移方案。

## 测试与质量门禁

- 测试与实现同包就近放置，覆盖成功路径、输入边界、依赖失败、错误映射、回滚/关闭和并发语义。优先使用可控同步原语，避免依赖任意 sleep 的脆弱测试。
- 每次改动完成后运行 `make ci`（= tidy + ensure-ci-tools + check + generate-check + build）；它先整理 go.mod/go.sum 并补齐缺失或过低的 CI 工具（本机更高版本直接接受），再跑 Go 与 Rust 的格式、vet/clippy、lint、无缓存测试、build、漏洞/供应链检查（govulncheck、cargo deny）以及 Go/Rust 生成漂移检查。
- 修改 Go 并发、goroutine、组件编排或资源生命周期时额外运行 `make race`（仅覆盖 Go 包）。
- `services/collaboration/interop/` 的 Node fixture 需在 Node >= 24.18.1 环境显式执行 `npm ci && npm run ci`（format:check、`node --test`、audit）；仓库不提供对应的根 Make 目标。
- IDL 变更除 `make ci` 外还必须运行上一节的 `compat-git` 检查。
- 修复格式差异应运行仓库格式命令并审查实际语义差异，不得接受整文件无语义的行尾重写。

## 文档与文本

- 公共运行时、配置、契约、依赖边界或生命周期发生变化时，同步更新 [`docs/framework-design.md`](docs/framework-design.md)；Rust Collaboration 相关变化同步更新 [`docs/rust-collaboration-design.md`](docs/rust-collaboration-design.md)；面向用户的启动方式或可用 API 变化时同步更新 `README.md`。
- 文档只描述代码中已存在且已验证的行为；未实现能力必须明确标记，不得用未来时规划冒充当前能力。
- 仓库文本统一使用 LF；只有 `.bat`、`.cmd` 使用 CRLF。发现整文件 diff 时先检查行尾和编码。

## 禁止的反模式

- `pkg/` 反向依赖服务、transport 承载业务规则、logic 直接依赖具体 GORM/Redis 类型。
- 全局配置/连接、服务定位器、无 owner goroutine、无上限重试或等待。
- panic/os.Exit 代替错误、丢弃 error/cause、向客户端返回内部错误细节。
- 手改生成文件、绕过公共 JSON/error/log/trace 契约、在指标中使用高基数或敏感标签。
- 在 `dev` 之外开发、直接修改 `main`、创建额外开发分支或改写共享历史。

<!-- skill-constructor:start -->
## Project Skill Harness

- This repository is enrolled. Read only task-relevant English generated references under
  `.agents/skills/<project>-project/references/en/` before planning or editing.
- Do not read `references/zh/` unless `$sync-project-skill-locales` reports locale drift. Humans may
  open the Chinese copies; they are translations, not the agent default.
- Invoke `$maintain-project-skill` only when the user asks to maintain project knowledge, required
  facts are unresolved or in conflict, or the task changes architecture, constraints, conventions,
  environment, services, or workflows.
- During those domain tasks, do not record, render, or edit the generated project skill until the
  original work is finished. Then update English facts, render, and invoke
  `$sync-project-skill-locales` when `skill-constructor locales` is not fully synced.
- Do not begin requested work while required manifest facts are unknown, candidate, or conflict.
- Ask the user to resolve every onboarding question; never infer constraints without evidence.
- Invoke `$design-distributed-transactions` for cross-service state changes, MQ publishing or
  consumption, compensation, or MQ selection, and do not complete while its reliability gates fail.
- Continue until the post-task status is `ready` or `skipped`, even when the host's Stop hook is
  advisory. Ordinary implementation edits do not require a rescan.
- Keep `.skill-constructor/manifest.json` and the generated project Skill directory under version
  control, and include their synchronized updates with the related engineering commit unless this
  repository records a verified manifest exception.
- When the generated project Skill lists managed project documents, review and synchronize those
  whose configured domains intersect the completed task; they remain human-editable files.
- Never edit generated English project-skill files or stage, commit, or push harness updates without
  an explicit user request.
<!-- skill-constructor:end -->
