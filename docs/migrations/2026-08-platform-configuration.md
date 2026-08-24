# Platform configuration migration

新增 Platform Go 服务（RPC `8885`、admin `8086`）和 PostgreSQL `platform` schema，用于替代站点业务配置的 Endpoint/Nacos 写入路径，并为邮件、AI 配置建立统一所有权。迁移只创建 Platform 自有的配置、审计、幂等、修订历史、outbox 和消费者投递表，不修改其他服务 schema。

发布顺序：

1. 在私有 GitOps 中创建 Platform PostgreSQL role、`knowledge-core-platform-secrets` 和 network policy；KEK 必须是新的 32-byte base64 值并可安全备份。
2. 部署 Platform，确认 migration、PostgreSQL/NATS readiness 和 `KNOWLEDGE_CORE_CONFIG` stream 契约。
3. 部署带 Platform RPC client 的 Gateway；`GET /api/v1/site-profile` 会读取默认快照，旧静态/Nacos 站点配置仅作为 Platform 不可用时的降级值。
4. 部署 Web 管理页，以 revision `0` 写入第一份 `site` 配置，再确认 delivery 为 `published`。
5. 邮件 namespace 由 Identity 的 durable JetStream consumer 自动热加载：先校验配置、探测 SMTP，再原子切换到 last-good；AI namespace 仍保留给后续消费者。

回滚 Gateway/Platform 不会删除表或密文。恢复旧版本 Gateway 后站点继续读取旧静态配置；不得删除 Platform KEK，否则历史敏感配置不可恢复。若事件进入 `parked`，先修复 NATS/stream 契约再由运维受控重放，不能直接把记录标成 published。
