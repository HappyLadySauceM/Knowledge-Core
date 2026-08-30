# Platform 运行时配置

Platform 是网页管理员可写业务配置的唯一数据所有者。当前 namespace 为 `site`、`email`、`ai`；Gateway 只暴露 HTTP façade，Nacos 继续管理监听地址、依赖 endpoint、证书路径、连接池和进程行为等部署配置。

## 写入契约

- 管理员通过 `GET /api/v1/admin/configuration/:namespace` 读取快照，通过 `PUT` 写入新修订。
- `PUT` 必须携带强 `If-Match: "<revision>"` 和 1–128 个可见 ASCII 字符组成的 `Idempotency-Key`。初次写入使用修订 `0`。
- 同一管理员、namespace 和幂等键在 24 小时内可安全重放；请求摘要不同会返回冲突。修订不匹配返回 `412`，调用方必须重新读取并人工合并。
- 管理端只能看见敏感字段是否已配置，不能读取明文。省略已配置的敏感字段会保留当前值；显式传空字符串表示清除。
- `site` 立即影响公开 `GET /api/v1/site-profile`。Gateway 在 Platform 暂不可用时保留旧静态站点配置作为可用性降级路径。

## 数据模型与加密

`platform` schema 包含：

| 表 | 用途 |
| --- | --- |
| `configurations` | 每个 environment/namespace 的最新快照和单调修订 |
| `configuration_revisions` | 不可变的历史修订，用于消费者按 revision 精确读取 |
| `config_audit` | actor、前后摘要和变更字段；不保存敏感明文 |
| `config_idempotency` | 24 小时请求摘要与结果修订 |
| `config_outbox` | 与配置写入同事务创建的可靠事件 |
| `config_deliveries` | 每个消费者的 validating/retrying/applied/rejected/parked 状态和 last-good revision |

公开值存为 JSONB。SMTP password 与 AI API key 使用随机数据密钥和 AES-256-GCM 信封加密；平台 KEK 只从 `PLATFORM_ENCRYPTION_KEK` 注入，不写入数据库、配置文件、HTTP/RPC 响应、事件、日志或 trace。AAD 绑定 environment、namespace 和 revision，因此密文不能跨坐标替换。

## 配置同步

一次 `PUT` 在同一个 PostgreSQL 事务内完成快照、审计、幂等记录和 outbox。后台 worker 使用 `FOR UPDATE SKIP LOCKED` 领取消息，收到 JetStream server PubAck 后才标记 `published_at`，并以 outbox message ID 作为 NATS deduplication ID。

事件契约：

- stream：`KNOWLEDGE_CORE_CONFIG`
- subject：`platform.config.changed.v1`
- message type：`platform.config.changed`
- aggregate：`<environment>:<namespace>`，aggregate version 等于配置 revision
- payload 仅含坐标、修订和快照摘要，不含配置值或敏感值

发布失败按 1、2、4…秒指数退避，最多 8 次后进入 `parked`。管理员页面通过 `/deliveries/:revision` 展示 `pending`、`published` 或 `parked`；Platform 内部 RPC 还提供消费者精确 revision 快照、状态查询和应用回报。`GetConsumerState` 在 namespace 尚未写入时返回 `DesiredRevision=0` 的空闲状态，而不是 404。邮件消费者使用 durable 名称 `identity-email-config-v1`，启动和每 60 秒按 desired/applied 水位对账，SMTP 探测失败只保留 last-good 配置并按退避重试，达到上限后进入 dead-letter/parked。

## 部署前置与验证边界

Platform 需要独立 PostgreSQL role/schema、NATS 项目凭据、JWT 公钥和 32-byte base64 KEK。Kubernetes Secret 名为 `knowledge-core-platform-secrets`，必须由私有 GitOps/SOPS 或集群 Secret manager 提供，应用仓库不存放具体值。

当前自动化覆盖字段校验、敏感值遮蔽、稳定请求摘要、JetStream stream 契约、服务令牌传播、消费者状态单调性，以及 outbox 成功、重试和停放。尚缺真实 PostgreSQL 的事务/并发/回滚故障注入、真实 NATS stop/start、真实 SMTP STARTTLS/AUTH 闭环和跨 Pod 重复/乱序/崩溃恢复测试；完成这些证据前，跨服务同步可靠性门禁不能标为生产 ready。
