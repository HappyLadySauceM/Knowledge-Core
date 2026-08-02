# Knowledge Core

Knowledge Core 是一个支持文档元数据、权限、附件、实时协作与版本恢复的知识协作后端。仓库采用单 Go module Monorepo，Collaboration 服务使用 Node.js/TypeScript。

当前实现包含四个服务：

- Gateway：公网 HTTP edge，负责严格输入校验、JWT、CORS、安全头、限流、错误映射和上游编排。
- Identity：用户注册、密码认证、账户锁定、Ed25519 JWT 签发与用户状态复核。
- Knowledge：文档元数据、成员权限、发布、附件、回收站、投影、配额和 outbox。
- Collaboration：Hocuspocus/Yjs WebSocket、持久化 update、快照、版本、恢复和多实例同步。

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
| 附件 | `/api/v1/studio/documents/:document_id/attachments` | 预签名上传、完成扫描和删除 |
| 回收站 | `/api/v1/studio/trash` | 删除文档列表与恢复 |
| 实时协作 | `ws://localhost:8091/collaboration` | Yjs 同步、权限复核、token 刷新和只读控制 |

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
    Gateway --> CollaborationInternal[Collaboration internal :8092]
    Collaboration --> KnowledgeInternal[Knowledge internal :8090]
    Identity --> PostgreSQL[(PostgreSQL)]
    Identity --> Redis[(Redis)]
    Knowledge --> PostgreSQL
    Knowledge --> S3[(S3 / MinIO)]
    Knowledge --> ClamAV[ClamAV]
    Knowledge --> NATS[(NATS)]
    Collaboration --> PostgreSQL
    Collaboration --> Redis
    Collaboration --> NATS
    Gateway --> Redis
    Gateway --> Etcd[(Etcd)]
    Identity --> Etcd
    Knowledge --> Etcd
```

Knowledge 不保存 Yjs update、快照或版本；这些数据属于 Collaboration。Collaboration 不直连 Identity 数据库，而是通过 Knowledge 的内部鉴权接口取得文档权限和用户信息。

## 环境要求

- Go `1.26.5`
- Node.js `24.18.1`（仅 Collaboration 本地开发）
- Docker Engine/Desktop 与 Compose v2
- GNU Make
- 修改 IDL 时使用 Kitex `v0.16.2`、Hertz `v0.9.7`、thriftgo `0.4.5`

主要端口：

| 服务 | 端口 |
| --- | --- |
| Gateway public/admin | `8080` / `8082` |
| Identity RPC/admin | `8881` / `8081` |
| Knowledge RPC/admin/internal | `8882` / `8083` / `8090` |
| Collaboration WebSocket/internal | `8091` / `8092` |
| PostgreSQL/Redis/Etcd/NATS | `5432` / `6379` / `2379` / `4222` |
| MinIO/console/ClamAV/Prometheus | `9000` / `9001` / `3310` / `9090` |

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

ClamAV 首次启动需要初始化病毒库，Knowledge 在 ClamAV、对象存储、NATS、数据库、Etcd、Identity 和 Collaboration 均可用后才会 ready。检查入口：

```powershell
Invoke-RestMethod http://127.0.0.1:8081/readyz
Invoke-RestMethod http://127.0.0.1:8082/readyz
Invoke-RestMethod http://127.0.0.1:8083/readyz
Invoke-RestMethod http://127.0.0.1:8080/health/ready
```

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

生产环境必须使用部署平台 Secret manager，并为外部 WebSocket、Gateway 到 Collaboration、Collaboration 内部 listener、Knowledge 内部 listener、PostgreSQL、Redis 和 NATS 配置代码要求的 TLS/mTLS。

## 本地开发

单独运行 Go 服务时显式指定各自 YAML：

```powershell
go run ./services/identity --config services/identity/etc/config.yaml
go run ./services/knowledge --config services/knowledge/etc/config.yaml
go run ./services/gateway --config services/gateway/etc/config.yaml
```

Collaboration：

```powershell
Set-Location services/collaboration
npm ci
npm run ci
npm start
```

`npm start` 运行已构建的 `dist/main.js`；开发监听可使用 `npm run dev`。

## 质量门禁

Go 全部门禁：

```text
make tidy
make ci
make race
```

Collaboration 门禁：

```text
cd services/collaboration
npm ci
npm run ci
```

远端 CI 使用 `.github/ci/run.sh`：Go 检查运行在仓库指定的 runner 镜像中，Collaboration 运行在独立的 `node:24.18.1-bookworm-slim` 容器中。二者均通过才允许自动创建 `dev -> main` promotion PR。

当前单元测试覆盖领域、逻辑、transport、严格输入、错误映射、Collaboration update ACK 顺序和关键生命周期行为。按照当前范围，Identity 与 Knowledge repository 尚未包含真实 PostgreSQL 集成测试；不要把现有 mock/单元测试等同于数据库兼容性验证。

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
