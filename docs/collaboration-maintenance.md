# Collaboration 运维恢复

Collaboration 的后台发布和 JetStream 消费都采用有界重试。超过 8 次仍失败时，事件不会被静默丢弃：outbox 记录进入 `parked_at`，消费 poison message 进入受限 `KNOWLEDGE_CORE_EVENTS_PARKING` stream。停车不是自动重试，必须由值班人员确认原因后 redrive。

## 查看收敛状态

Admin `/metrics` 暴露以下无文档/用户标签的指标：

- `knowledge_core_collaboration_outbox_pending`、`outbox_parked`、`outbox_oldest_age_seconds`
- `knowledge_core_collaboration_projection_pending`、`projection_oldest_age_seconds`
- `knowledge_core_collaboration_worker_operations_total`

dev 告警以 60 秒为收敛预警、5 分钟为严重告警。停车写入失败是 critical；源消息只有在停车 PubAck 成功后才会 TERM。

## Outbox redrive

必须在受限维护 Pod 中执行，并显式给出操作者和确认标志：

```text
knowledge-core-collaboration maintenance outbox-redrive \
  --operator oncall@example.com --limit 20 --confirm
```

只需恢复一条事件时追加 `--event-id <uuid>`。命令在一个 PostgreSQL 事务中锁定 `parked_at IS NOT NULL` 的事件，重置其 retry budget、保留原 event id/payload，并写入 `collaboration.maintenance_actions` 审计行；已发布事件永远不会被 redrive。发布 worker 随后按正常 outbox 流程处理，JetStream 的 event id 保持幂等。

## JetStream parking redrive

```text
knowledge-core-collaboration maintenance parking-redrive \
  --operator oncall@example.com --limit 20 --confirm
```

命令使用 durable `parking-redrive` consumer，逐条恢复原 subject/`Nats-Msg-Id`，只有原 subject 的 JetStream PubAck 成功后才 ACK parking 消息；重复执行不会因网络重试制造新的业务事件。原 subject 缺失、不是 Collaboration 协议 subject 或停车消息没有原始元数据时，命令 fail closed，不 ACK 该消息。

维护命令不会通过公开 admin HTTP 暴露；生产中应使用短生命周期、最小权限的 Job，并把操作者身份写入审计与发布记录。
