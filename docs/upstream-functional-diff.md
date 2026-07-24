# new-api 当前分支与 upstream/main 的功能差异

> 本文只记录需要在后续上游合并中继续保留的独立定制。上游目录结构、认证会话、通用 UI 和基础设施代码均以上游实现为准。

## 合并原则

- 以上游单前端目录 `web/` 为基础，不恢复已退役的 Classic 前端。
- 登录、OAuth 登录源、Passkey、微信登录和法律条款展示沿用上游认证架构。
- 独立业务只在上游当前扩展点保留小范围补丁。
- 数据库代码同时支持 SQLite、MySQL 和 PostgreSQL。

## 1. 注册与账号策略

### 教育邮箱白名单后缀匹配

邮箱白名单按完整域名或父级后缀匹配。例如白名单包含 `edu.cn` 时，`school.edu.cn` 和 `mail.school.edu.cn` 均可通过，但 `edu.glnc.cn` 不会命中。

- 后端校验：[`controller/misc.go`](../controller/misc.go)
- 注册页提示：[`web/src/features/auth/sign-up/components/sign-up-form.tsx`](../web/src/features/auth/sign-up/components/sign-up-form.tsx)

### 用户名格式限制

新用户名只允许英文字母、数字、下划线和中划线，规则为 `^[a-zA-Z0-9_-]+$`。

- 实现：[`model/user.go`](../model/user.go)

### 微信仅允许已绑定账号登录

微信身份未绑定现有用户时不再隐式创建账号，用户需要先通过正常注册流程创建账号并完成绑定。

- 实现：[`controller/wechat.go`](../controller/wechat.go)

## 2. OAuth2 / OIDC 授权服务器

本站除作为外部 OAuth 登录源的客户端外，还可以作为 OAuth2/OIDC Provider，供 Cherry Studio 等第三方客户端授权登录。

### 主要实现

- 路由：[`router/oauth-router.go`](../router/oauth-router.go)
- 控制器：[`controller/oauth2.go`](../controller/oauth2.go)
- 服务：[`service/oauth2.go`](../service/oauth2.go)
- OAuth access token 中间件：[`middleware/oauth2.go`](../middleware/oauth2.go)
- 数据模型与迁移：[`model/oauth2.go`](../model/oauth2.go)、[`model/main.go`](../model/main.go)
- 配置：[`setting/system_setting/oauth_server.go`](../setting/system_setting/oauth_server.go)
- 管理页面：[`web/src/features/system-settings/auth/oauth-server-section.tsx`](../web/src/features/system-settings/auth/oauth-server-section.tsx)

### 端点

- `GET /oauth2/auth`：前端授权入口；复用上游登录会话并调用受 Bearer 保护的授权 API。
- `POST /api/oauth2/auth`：登录用户创建授权码的内部 API。
- `POST /oauth2/token`：使用 authorization code 或 refresh token 换取令牌。
- `POST /oauth2/revoke`：撤销 access token 或 refresh token。
- `GET /oauth2/userinfo`：按 scope 返回用户信息。
- `GET /oauth2/jwks`：返回 ID Token 签名公钥。
- `GET /.well-known/openid-configuration`：OIDC discovery。
- `GET /api/v1/oauth/tokens`：读取用户令牌列表，需要 `tokens:read`。
- `GET /api/v1/oauth/balance`：读取余额，需要 `balance:read`。

授权码流程支持 PKCE S256；authorization code、access token 和 refresh token 均以摘要形式入库，不保存明文。

## 3. 充值后自动切换分组

管理员可以按普通充值的累计 USD 金额配置分组链。充值成功后，系统选择不超过累计金额的最高阈值，并将链内用户切换到对应目标分组。

### 主要实现

- 配置结构：[`setting/operation_setting/payment_setting.go`](../setting/operation_setting/payment_setting.go)
- 分组策略：[`model/payment_group_policy.go`](../model/payment_group_policy.go)
- 各支付渠道完成事务：[`model/topup.go`](../model/topup.go)
- 管理接口：[`controller/option.go`](../controller/option.go)
- 管理页面：[`web/src/features/system-settings/integrations/payment-settings-section.tsx`](../web/src/features/system-settings/integrations/payment-settings-section.tsx)

### 业务约束

- 可选择统计全部成功充值，或只统计功能启用后的充值。
- Stripe 按 `money` 计入 USD；其他普通充值按项目既有金额口径折算。
- 只切换基础分组和配置目标分组构成的链，不覆盖无关的手工分组。
- 有效订阅的升级分组优先；订阅结束后重新计算充值分组。
- 分组变更提交后使用上游数据库权威缓存刷新接口更新用户缓存。

## 4. Epay 完成时间

Epay 异步通知确认成功时同时写入 `CompleteTime`，保证“只统计启用后新充值”的时间过滤有可靠数据。

- 实现：[`controller/topup.go`](../controller/topup.go)

## 5. Docker 发布流程

本分支保留面向自有 Docker Hub 镜像的 tag 和手动发布工作流。

- 实现：[`.github/workflows/docker-build.yml`](../.github/workflows/docker-build.yml)

## 后续合并检查清单

1. 保留上游 `main.go`、认证中间件和前端登录组件的最新结构。
2. 确认 OAuth 授权入口仍通过上游 Bearer 会话完成认证。
3. 确认所有普通充值成功路径都会调用自动切组策略，且 Epay 写入完成时间。
4. 确认订阅升级分组仍高于充值分组。
5. 确认邮箱白名单继续按标签边界进行域名后缀匹配。
6. 运行后端测试、前端类型检查、定制文件 Lint 和生产构建。
