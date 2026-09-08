# Knowledge-Core API 文档使用说明

本目录保存 Gateway HTTP 和 Collaboration WebSocket 的 API 文档产物。HTTP
契约使用 OpenAPI 3.1.0 描述，WebSocket 契约使用 AsyncAPI 3.0.0 描述。

## 文档内容

| 文件 | 用途 |
| --- | --- |
| `openapi.yaml` / `openapi.json` | Gateway HTTP 的机器可读契约 |
| `http.html` | Gateway HTTP 的中文只读说明页 |
| `asyncapi.yaml` / `asyncapi.json` | Collaboration WebSocket 的机器可读契约 |
| `websocket.html` | WebSocket 握手、消息和关闭码说明页 |
| `index.html` | 文档首页 |
| `style.css` | 说明页样式 |
| `assets.go` | 将上述静态资源嵌入 Gateway 二进制的 Go 包 |

除 `README.md` 外，文档资源由生成器维护。不要直接修改生成的 YAML、JSON、HTML
或 CSS；契约和说明应修改对应输入后重新生成。

## 生成文档

在仓库根目录执行：

```bash
make api-docs
```

生成器的输入源为：

- `idl/http/v1/gateway.thrift`：HTTP 路径、方法和 Thrift 数据结构。
- `services/gateway/internal/apidocs/metadata.yaml`：接口标题、描述、标签、鉴权、成功状态、错误状态和响应头。
- `services/gateway/internal/apidocs/websocket.yaml`：WebSocket 地址、子协议、消息说明和关闭码。

生成结果会写入本目录。提交前执行漂移检查：

```bash
make api-docs-check
```

`make generate` 也会在 Go 代码生成完成后生成 API 文档；`make generate-go-check`
会同时校验 Go 生成代码和 API 文档。文档生成文件清单维护在
`scripts/generated-files.txt`。

## 本地访问

文档默认关闭。使用本地配置启动 Gateway 时，可以通过环境变量临时启用：

```bash
GATEWAY_API_DOCS_ENABLED=true \
  go run ./services/gateway --config services/gateway/etc/config.yaml
```

Gateway Admin listener 默认监听 `:8082`，启动后访问：

```text
http://127.0.0.1:8082/docs/
```

机器可读文档地址：

```text
http://127.0.0.1:8082/docs/openapi.yaml
http://127.0.0.1:8082/docs/openapi.json
http://127.0.0.1:8082/docs/asyncapi.yaml
http://127.0.0.1:8082/docs/asyncapi.json
```

也可以直接下载 JSON：

```bash
curl http://127.0.0.1:8082/docs/openapi.json
```

文档页面是只读页面，不提供在线调用（Try it out）。

## 集群访问

Kubernetes Gateway Service 同时暴露业务端口 `8080` 和 Admin 端口 `8082`。开发环境
已在 `deploy/gateway/overlay/dev/config.yaml` 和
`deploy/nacos/gateway.dynamic.yaml` 中启用 API 文档。

集群内可通过 Gateway Service 的 Admin 端口访问：

```text
http://knowledge-core-gateway.knowledge-core-dev.svc.cluster.local:8082/docs/
```

从本地通过端口转发访问：

```bash
kubectl -n knowledge-core-dev \
  port-forward svc/knowledge-core-gateway 8082:8082
```

然后打开 `http://127.0.0.1:8082/docs/`。公网业务端口 `8080` 不注册这些文档路由；
生产或其他环境需要显式设置 `api_docs.enabled: true` 或
`GATEWAY_API_DOCS_ENABLED=true`，并应限制 Admin 端口的网络访问范围。

## 更新流程

修改 IDL 或文档元数据后，按以下顺序检查：

```bash
make api-docs
make api-docs-check
make generate-go-check
```

确认 `api/openapi.*`、`api/asyncapi.*` 和说明页的差异符合预期，再提交变更。运行时
不依赖容器中的文件挂载，`assets.go` 会在编译时把文档资源嵌入 Gateway 二进制。
