# Attachment 服务设计（第一阶段）

Attachment 是图片、音频、视频、文档、压缩包及普通文件的统一文件服务。它不把资源绑定到某个文档；业务服务通过 Attachment RPC 管理由 Attachment 持久化的引用关系，因此同一资源可以被文章、站点首图和头像复用，且不会跨服务直写数据库。

## 上传与状态

1. `CreateAttachment` 校验安全 MIME 白名单、文件名和大小，创建 opaque object key，向 MinIO 创建 multipart upload，并返回 16MiB 分片的外部签名 URL。
2. 客户端并发上传分片后调用 `CompleteAttachment`。该调用只完成 multipart，不读取对象内容。
3. 扫描 worker 单次流读取对象，同时计算 SHA-256、大小和 MIME，并将对象发送给 ClamAV。只有 clean 且声明值一致时状态才会进入 `ready`。
4. `ready` 或 `rejected` 可以进入 `trashed`；存在引用时拒绝 trash。恢复只允许 `trashed` 资源。

状态：`pending_upload -> scanning -> ready|rejected -> trashed -> deleted`。感染对象不会进入可恢复的 ready 状态。

## 安全边界

- 单文件上限 1GiB，单用户活动配额 10GiB，最多 64 个分片。
- 可执行文件、脚本和未知高风险类型不在 v1 白名单中。
- bucket `knowledge-core-attachments` 为 private；浏览器只拿到短期 multipart/download 签名 URL。
- 外部 S3 hostname：`knowledge-core-attachments.happyladysauce.local`，由 Higress 透传到 MinIO API。
- 关键状态变更使用 Attachment 数据库事务和 durable worker，不依赖当前共享 NATS ACL；非关键事件待后续独立凭据和权限矩阵完成后再接入。

## 后续集成顺序

1. Gateway 增加 Attachment HTTP façade 和旧文档附件兼容窗口。
2. Knowledge 将富文本图片从任意 `src` 收敛为 `attachmentId`，发布时校验绑定集合均为 ready。
3. Identity 迁移头像到 Attachment；站点 singleton 配置迁移首图与焦点位置。
4. Web 接入 Tiptap/Yjs、分片上传、断点恢复和 attachment URL 解析。
5. 增加 1GiB 上传/扫描集成测试、ClamAV 压测和 orphan reconciliation。
