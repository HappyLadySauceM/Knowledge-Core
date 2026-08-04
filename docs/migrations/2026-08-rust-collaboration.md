# Rust Collaboration 切换记录

> 状态：实施中。此记录描述已批准的破坏性迁移边界；验证结果尚未完成前，不代表生产切换已经发生。

## 契约变更

- `DocumentDetailData` 删除 field 5 `websocket_url` 和 field 6 `fragment`，field ID 不复用。
- Gateway 新增 `POST /api/v1/studio/documents/:document_id/collaboration-sessions`。客户端必须先创建短期 session，再把固定协议和一次性 ticket 放入 `Sec-WebSocket-Protocol`。
- WebSocket 路径改为 `/v1/documents/{document_id}`；不再支持 Hocuspocus token refresh 扩展、匿名公开协作或旧 `/collaboration` 行为。
- Gateway、Knowledge 和 Collaboration 的版本、清理、授权与投影调用统一切换到生成的 Thrift client/server。旧 internal HTTP listener 不保留兼容层。
- Knowledge RPC 新增独立 `Live`：`Ping` 保持 readiness 语义，Collaboration 启动探测使用 `Live`，避免 Knowledge 与 Collaboration 的 readiness 冷启动环。
- `go run ./scripts/idlguard compat-git d0b96df70be36e1db602d68ca2c26c8f09f36a1a idl` 已执行；唯一不兼容项是上述两个 `DocumentDetailData` 字段删除。`Live` 与 Collaboration RPC 新增项没有产生额外兼容告警。

客户端必须在同一发布窗口升级，不能把旧文档详情中的 WebSocket 字段作为协作入口。

## 数据与发布

现有 `collaboration` schema、Yjs update、快照、版本和任务数据不迁移。切换窗口按以下顺序执行：

1. 关闭 Gateway collaboration session route，并暂停会产生权限事件的成员写入；等待当前最大 ticket TTL 过期。
2. 在有界时间内排空旧 Node 连接、Knowledge/Collaboration outbox 和 worker，并备份旧 `collaboration` schema。
3. 删除旧 schema，由 Rust migration 从空 schema 建立新表。
4. 移除旧共享 stream：重建 `KNOWLEDGE_CORE_EVENTS`，只保留 update/invalidation subject、24 小时 max age 和 1 GiB max bytes；新建 `KNOWLEDGE_CORE_PERMISSIONS`，只保留 permission subject、24 小时 max age 且 `max_bytes=-1`。NATS 不允许两个 stream subject 重叠，整个变更必须在上述无写入窗口内完成。
5. 同时部署新客户端、Gateway、Knowledge 和 Rust Collaboration，并确保 `COLLABORATION_NATS_STREAM` 与 `COLLABORATION_NATS_PERMISSION_STREAM` 指向两个不同 stream。
6. 验证两套 stream 契约、Etcd 注册、双向 Thrift mTLS、session ticket、双客户端编辑、投影、版本、恢复、权限失效、metrics 和 shutdown。
7. 全部门禁通过后重新开放成员写入和 session route。

每个 Collaboration 副本必须设置唯一且重启后稳定的 `COLLABORATION_INSTANCE_ID`。它决定 JetStream durable consumer identity；临时或重复 ID 会破坏副本 fanout 或未 ACK redelivery。NATS subscription 意外结束时当前进程会 fail closed 并转为 not-ready，由外部编排重启，不承诺进程内自愈。

## 回滚限制

回滚必须先关闭 session route，再整体回滚客户端、Gateway、Knowledge 和 Collaboration。旧 Node 服务只能配合切换前备份或空 schema 启动；Rust 切换后产生的数据不保证回迁到旧 schema，因此切换窗口的最大数据损失边界是 Rust 服务开放后至整体回滚之间的新协作数据。

## 当前自动化证据

- Go：全包无缓存测试、vet 与 build 已通过；Knowledge `Live`、JetStream PubAck/retry/dedup outbox 和调用 context/deadline 有定向回归测试。
- Rust：最终本地工作区的 workspace 单元/集成测试、Clippy `-D warnings`、release build 和无警告 cargo-deny 已通过；Rust library 测试为 82/82。
- 互操作：Go -> Rust Collaboration 与 Rust -> Go Knowledge 覆盖 TTHeader、BizStatus、deadline、request ID、trace、token metadata、双向 mTLS 和 `Live`；Node/Yjs fixture 为 6/6，audit 为 0。self-hosted fallback 已统一从 `tools/interop` 构建 Go fixture。
- 真实依赖：PostgreSQL、Redis、NATS 和 Etcd 测试已在本地隔离容器网络中以 `COLLABORATION_TEST_REQUIRE_REAL_DEPENDENCIES=1` 通过；缺少任一对应连接变量会失败而不是静默 skip。两个 JetStream instance fanout、稳定 durable 未 ACK redelivery、actor 回收恢复和重复 update 的临时 NATS 实测为 3/3。
- 失效行为：permission 与 document invalidation JSON 经 worker/actor 路由分别关闭活跃连接为 `4403` 和 `4409`；队列饱和、解析或 ACK 失败不会静默确认。
- 权限竞态：permission revision 只关闭旧 session；三个固定 subject 分属 document 与 permission stream。permission stream 没有 size eviction，只按 24 小时 max age 清理；新 durable 回放全部时间保留历史，并等待 ACK floor 的 stream sequence 越过 consumer 创建后的 permission stream `last_sequence` 快照。retention 收缩时只有 pending 与 ack-pending 同时为零才接受空集合追平；有界 registry watermark 继续防止旧 ticket 在事件后重新建立权限。
- Readiness：Knowledge `Ping` 保持 readiness 语义，`Live` 只返回 `knowledge/live` 且解除冷启动环；Collaboration `Ping` 读取与 admin 相同的完整应用 readiness，而不是只检查 Etcd 注册。RPC serve task 的任何非计划退出以及 Etcd registration key/value/lease 所有权丢失都会立即 fail closed；RPC exit 与最终 ready commit 由同一 gate 原子协调，commit 前再次验证 listener。其余六个 RPC 在 not-ready 时先返回 `40007 / collaboration.unavailable`，不触发 Knowledge、ticket、store 或 actor 副作用。
- 镜像门禁：本地重建 production binary，校验运行 UID/GID `10001:10001`、runtime 无 Node/npm、OpenSSL 3 动态链接完整、只读根文件系统和非法配置 fail-fast，并清理本轮临时镜像。
- `.github/ci/Dockerfile` 必须保持与基线完全一致；它是远端 runner 镜像，不是 Rust Collaboration production image。

## 尚待发布验证

- 最终本地工作区的 `make ci`、`make race`、生成漂移、`git diff --check`、govulncheck 和 cargo-deny 已通过。远端 rootless builder 的功能阶段已有通过记录，但尚无单次完整 `.github/ci/run.sh` 结果：GitHub RustSec、Docker Hub token 和 Debian mirror 的外部网络失败先后阻塞执行，最后一次停在 production image 的 `apt-get`。2026-08-03 已决定暂停远端环境操作；生产发布前仍须在网络稳定时重新取得完整结果。
- Node/Rust 的 p95 延迟、单连接内存和连接密度基线。
- PostgreSQL/NATS stop/start、完整 Compose/跨服务 WebSocket E2E、备份恢复以及切换/回滚演练。
- 发布构建产生的 production image 最终 digest。
- Identity 与 Knowledge repository 的真实 PostgreSQL 测试不在本次 Rust Collaboration 范围内，缺口继续保留。
