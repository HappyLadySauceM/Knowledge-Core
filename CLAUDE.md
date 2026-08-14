# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Knowledge Core 是知识协作后端：**一个 Go module + 一个 Rust workspace**，四个服务 Gateway / Identity / Knowledge / Collaboration。Go module 路径为 `github.com/HappyLadySauce/Knowledge-Core`。

## 权威文档（本文件只做导航，冲突时以下列为准）

- **[AGENTS.md](AGENTS.md)** —— 编码 Agent 必须遵守的强约束（git 分支策略、架构边界、Go/错误/日志/指标契约、IDL、质量门禁、禁止的反模式）。**不要在 CLAUDE.md 里复述这些规则；有疑问直接读 AGENTS.md。**
- [docs/framework-design.md](docs/framework-design.md) —— 架构与运行时契约的基准；公共运行时/配置/契约/生命周期变化时同步更新。
- [docs/rust-collaboration-design.md](docs/rust-collaboration-design.md) —— Rust Collaboration 设计基线。
- [docs/trace-architecture.md](docs/trace-architecture.md) —— 追踪/采样架构。
- [README.md](README.md) —— 公开 HTTP/WS API、端口表、Compose 快速开始、本地运行方式。
- `.agents/skills/knowledge-core-project/` —— 生成的项目技能与分主题参考（environment/architecture/services/workflows/conventions/constraints）。

## 项目技能 Harness（每个任务都要遵守）

- 任务**开始和结束**都调用 `$maintain-project-skill`；跨服务状态变更、MQ 发布/消费、补偿或 MQ 选型时调用 `$design-distributed-transactions`。
- 在规划或编辑前先读 `.agents/skills/knowledge-core-project/` 下与任务相关的参考。
- 技能文件由 `.skill-constructor/` 与任务结束 hook 生成：**不要手改生成的技能文件，也不要自动 stage/commit harness 更新**。通过 `.skill-constructor/manifest.json`（配合 CLI）修改。
- `.cursor/hooks.json`、`.codex/hooks.json`、`.reasonix/settings.json` 是各 IDE 的 skill-constructor 钩子。

## 常用命令

改动完成后的门禁（同时覆盖 Go 和 Rust）：

```bash
make ci        # = check + generate-check：fmt/vet/lint、无缓存 test、build、govulncheck、cargo deny，以及 Go/Rust 生成漂移检查
make race      # Go race 检测；改动并发/goroutine/生命周期/组件编排时必须额外跑
make tidy      # 新增/升级依赖后规整 go.mod / go.sum
```

细粒度目标（见 `make help`）：`fmt` `fmt-check` `vet` `lint`（`line` 是别名）`test` `build` `vuln` `supply-chain` `generate` `generate-check` `go-ci` `rust-ci`。

- **只改了 Go、想跳过 Rust 门禁**：`make ci KC_RUST_GATE=0`（跑 Go 门禁 + Go 生成漂移检查）。
- **编译并行度**：`BUILD_JOBS`（默认 = 宿主 CPU 的 3/4）通过 `GOMAXPROCS` / `CARGO_BUILD_JOBS` 传入。
- Node 互操作 fixture 不在 Go 发现范围内（见 `GO_PACKAGES`），需在 `services/collaboration/interop/` 用 Node ≥ 24.18.1 手动 `npm ci && npm run ci`。

单个测试：

```bash
go test ./services/knowledge/internal/logic -run TestName -count=1   # 单个 Go 包/用例
cd services/collaboration && cargo test -p knowledge-core-collaboration <name> --locked   # 单个 Rust 用例
```

本地单独运行某个 Go 服务（顶层已无共享 `config/`，静态配置在各服务 `etc/config.yaml`）：

```bash
go run ./services/identity  --config services/identity/etc/config.yaml
go run ./services/knowledge --config services/knowledge/etc/config.yaml
go run ./services/gateway   --config services/gateway/etc/config.yaml
cd services/collaboration && cargo run --locked -p knowledge-core-collaboration   # 需先设 COLLABORATION_POSTGRES_URL 等环境变量
```

整栈本地依赖用 Compose（细节见 README）：`docker compose -f docker/infrastructure/docker-compose.yml up -d --build`。

代码生成与 IDL：

```bash
make generate           # 通过 scripts/codegen.sh|ps1 重生成 Hertz(hz update) 与 Kitex 代码
make generate-check     # 生成并在漂移时失败
go run ./scripts/idlguard compat-git <merge-base> idl   # IDL 变更的兼容检查（make ci 不含此项）
```

## 架构总览（需要跨文件才能理解的部分）

**两层分离（最重要的边界）**：`pkg/` 只放与业务无关、可跨服务复用的能力，**绝不能 import `services/*`**；服务私有的配置、领域模型、用例、传输适配都留在 `services/<service>/internal/`。

**Go 服务装配方向**（`gateway`/`identity`/`knowledge` 同构）：`main.go`（只创建并执行应用命令）→ `spec.go`（只声明 `coreapp.Spec`：Name/Config/RuntimeOptions/Register）→ `internal/config` → `internal/context`（`ServiceContext` 显式构造并持有依赖）→ `internal/{domain,model,logic,repository,transport,client}`。`pkg/app.Runtime` 只管进程级日志/追踪/健康/指标/生命周期编排，**不能被当成服务定位器**。分层职责：`domain` 领域对象+校验、`model` 仅 GORM 持久化模型、`logic` 依赖小接口的用例、`repository` 持久化+事务+存储错误映射、`transport` 只做协议校验/编排/安全错误映射（不承载领域规则）。

**四个服务与数据归属**（谁拥有什么数据是关键，不要越界直连别人的库）：

| 服务 | 语言/传输 | 拥有的数据 | 外部依赖 |
| --- | --- | --- | --- |
| Gateway | Go / Hertz HTTP edge (`:8080`) | 无自有库 | Redis，经 typed client 调 Identity/Knowledge/Collaboration RPC |
| Identity | Go / Kitex RPC (`:8881`) | 用户、认证（PostgreSQL + Redis） | — |
| Knowledge | Go / Kitex RPC (`:8882`) | 文档元数据、成员权限、发布投影、附件、回收站、配额、outbox（PostgreSQL `knowledge` schema；SQL 迁移在 `internal/migration/migrations/`） | S3/MinIO、ClamAV、NATS；调 Identity/Collaboration RPC |
| Collaboration | **Rust** / Volo Thrift RPC (`:8883`) + WebSocket (`:8091`, y-sync + awareness, Yrs CRDT) | Yjs update/快照/版本/投影 job/outbox（PostgreSQL `collaboration` schema） | Redis、NATS；经 Knowledge RPC 复核权限 |

关键边界：**Knowledge 不保存任何 Yjs 状态**（归 Collaboration）；**Collaboration 不直连 Identity/Knowledge 数据库**，而是通过生成的 Knowledge Thrift RPC 取权限并提交投影，session ticket 短期、单次使用。

**IDL 与生成代码**：契约源是 `idl/http/v1/gateway.thrift` 与 `idl/rpc/v1/*.thrift`。生成输出（`kitex_gen/`、`services/collaboration/src/generated/`）**完全由生成器拥有，禁止手改**；所有权唯一清单是 [scripts/generated-files.txt](scripts/generated-files.txt)（文件头 generated 注释不能覆盖该清单的判定）。Gateway handler 与未列入清单的 route middleware 是手写/混合所有权。固定工具版本：Kitex `v0.16.2`、Hertz(hz) `v0.9.7`、thriftgo `0.4.5`、rustc `1.97.1`（`rust-toolchain.toml`）；Hertz 只允许 `hz update`，禁止 `hz new`。

**跨服务消息（NATS JetStream）**：subject 固定为 `collaboration.documents.updated`、`collaboration.documents.invalidated`（属 document stream，默认名 `KNOWLEDGE_CORE_EVENTS`）与 `knowledge.permissions.changed`（独占 permission stream，默认名 `KNOWLEDGE_CORE_PERMISSIONS`）。每个 Collaboration 副本需配置唯一且重启后稳定的 `COLLABORATION_INSTANCE_ID` 以保持 durable consumer 重投递语义。subject/stream/consumer 契约漂移会拒绝 ready（细节见 README「Compose 快速开始」段与设计文档）。

## 高频硬约束速记（完整规则见 AGENTS.md）

- **Git**：`dev` 是唯一开发分支，所有改动直接落 `dev`；禁止创建 `feature/*`、`fix/*` 等其它分支；`main` 对开发者只读；禁止 force-push / rebase / 改写共享历史。是否 commit/push 需当前任务明确授权。
- **不要**在 `pkg/` 反向依赖服务、在 transport 承载业务规则、让 logic 直接依赖具体 GORM/Redis 类型、用全局 Viper/pflag 或可变 singleton、开无 owner/无上限的后台 goroutine。
- 优先复用 `pkg/{auth,error,log,metadata,trace,metrics,codec}` 现有契约；HTTP JSON 统一走 `pkg/codec/json` 并严格拒绝未知字段，不要用 `encoding/json` 绕过。
- 构造函数返回 `(T, error)` 并校验依赖；错误用 `%w` 保留 cause、`errors.Is/As` 判断；跨 Kitex/Hertz 边界用 `pkg/error` 稳定 code/key/kind，内部错误不外泄。`os.Exit` 只允许在进程入口。
- Secret 只经环境变量/部署平台注入，绝不写入 YAML、源码、日志、metric label 或 span attribute；metric label 只用有界低基数值。
- 仓库文本用 LF（仅 `.bat`/`.cmd` 用 CRLF）；遇到整文件 diff 先查行尾/编码。

## 部署 / GitOps（简述）

`.github/workflows/pipeline.yml` 是唯一 workflow：`dev` push 先跑 `make ci` + Collaboration Node 互操作门禁，再按变更服务从 Harbor 缓存构建镜像、更新 GitOps 快照、等 Argo CD 同步与 dev 冒烟通过后，DeepSeek 生成中文变更摘要，最后 fast-forward `main` 并打单一聚合 tag `vMAJOR.MINOR.PATCH`（读根目录 `VERSION`，GitHub Release 标题同为该版本号）。共享基础设施与集群拓扑/凭据由私有仓库 `k3s-home-deploy`（服务器 `/opt/k3s`）声明；本仓库的应用部署模板在 `deploy/<service>/`（各服务自维护 `base/` 与 `overlay/dev/`），不记录集群凭据。
