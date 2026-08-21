# 认证生产化（2026-08）

## 已实现

- 注册与验证邮件在 Identity PostgreSQL 同一事务内创建。
- 邮箱验证、密码重置、账户停用分别与用户状态和会话撤销保持原子性。
- Refresh Token 使用严格轮换；并发刷新在 10 秒宽限内返回同一后继令牌，超时复用触发会话重放撤销。
- Refresh、操作令牌和邮件载荷支持独立配置密钥；未配置新密钥时保留旧派生密钥作为迁移兼容路径。
- 邮件 outbox 具备稳定消息 ID、租约领取、指数退避、8 次后 parked、过期租约回收和失败日志。
- Gateway 为邮箱验证、密码重置及 Token 提交增加独立匿名限流。
- Web BFF 增加同源 Origin 校验、Access 过期自动刷新、会话查询、Studio 入口保护和 URL Token 预填。

## 运行前置

- SMTP 使用 `mail.happyladysauce.cn:587` STARTTLS，账号和密码只能通过 Kubernetes Secret/SOPS 注入。
- Maddy TLS 证书必须续期；当前证书过期后不得关闭证书校验。
- 发送域的 SPF、DKIM、DMARC 和 `27.13.22.141` 的 PTR 必须修正并通过真实收件测试。
- dev k3s 端到端验收收件人为 `13452552349@163.com`。

## 验证

Core 使用 `make ci`、`make race`；Web 使用 lint、typecheck、unit、build 和 Playwright smoke。当前仍缺少真实 PostgreSQL repository 集成测试和真实 Maddy 发送验收，不能将单元测试结果视为邮件基础设施已上线。
