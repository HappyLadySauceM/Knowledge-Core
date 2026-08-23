# Attachment service migration

新增 `attachment` Go 服务（RPC 8884、admin 8085）及 `attachment` PostgreSQL schema。迁移只创建 Attachment 自有表，不修改已应用的 Knowledge migration：

- `attachment.attachments`
- `attachment.scan_jobs`
- `attachment.references`

后续 migration 增加扫描租约/停放字段，以及按 `owner_id + idempotency_key` 的部分唯一索引和请求摘要。重复创建请求会在数据库事务内复用原记录；并发输入创建出的临时 multipart 在输出复用时执行补偿 abort。

部署需要创建 private MinIO bucket `knowledge-core-attachments`，并将 `knowledge-core-attachments.happyladysauce.local` 指向 MinIO API。Gateway 已通过 Attachment RPC 提供通用 HTTP façade；旧的 Knowledge 文档附件接口仍作为兼容窗口保留，新客户端必须使用 Attachment multipart API。Gateway 的 Attachment RPC 地址已加入静态 ConfigMap 和 Nacos 动态配置，ApplicationSet 会在 GitOps revision 更新后发布 attachment Deployment/Service。
