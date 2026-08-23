# Attachment service migration

新增 `attachment` Go 服务（RPC 8884、admin 8085）及 `attachment` PostgreSQL schema。迁移只创建 Attachment 自有表，不修改已应用的 Knowledge migration：

- `attachment.attachments`
- `attachment.scan_jobs`
- `attachment.references`

部署需要创建 private MinIO bucket `knowledge-core-attachments`，并将 `knowledge-core-attachments.happyladysauce.local` 指向 MinIO API。旧的 Knowledge 文档附件接口仍作为兼容窗口保留；新客户端必须使用 Attachment multipart API。
