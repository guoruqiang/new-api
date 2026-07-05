# 与 upstream/main 的功能差异

> 对比范围：当前分支相对 `upstream/main`。
>
> 本文只记录功能、行为、入口和可见 UI 层面的差异；README、lockfile、纯格式化或构建产物不展开。

## 总览

当前分支相对 upstream/main 的主要新增和调整可以分为五类：

1. **登录/注册入口收敛**：登录页以邮箱/用户名登录为主，只保留“已绑定微信账号登录”；注册页收敛为邮箱/用户名注册主路径，不再展示 OAuth / Passkey 等快捷入口。
2. **微信登录策略收紧**：微信登录不再自动创建新用户，只允许已存在且已绑定微信的用户登录。
3. **注册与账号校验增强**：新增用户名格式限制；邮箱白名单从完整域名匹配改为逐级域名后缀匹配；登录失败提示改为更泛化文案。
4. **新增 OAuth2 / OIDC 授权服务器能力**：本站可以作为 OAuth2/OIDC Provider，让第三方客户端通过本站授权登录并访问受 scope 控制的资源。该能力不同于 upstream 已有的“自定义 OAuth 登录源”。
5. **新增支付/充值后自动切换分组能力**：管理员可配置累计充值金额阈值，用户普通充值成功后自动切换到对应分组。

此外还有两个较小差异：

- 首页内容区增加顶部留白，用于改善嵌入 iframe 时顶部过近的问题。
- 新增 Docker 自动构建发布工作流，属于发布流程增强，不影响业务逻辑。

明确不包含：

- 顶部导航“点我聊天 / Chat Now”入口已经移除，不再作为本分支差异。
- upstream 已有的“自定义 OAuth”是接入外部 OAuth/OIDC 身份提供商；本文第 4 节的 OAuth2/OIDC 授权服务器是本站作为身份提供方，两者不是同一个功能。

## 1. 登录/注册入口收敛

### 差异结论

upstream 的登录/注册页仍保留更多 OAuth、Passkey 等入口。当前分支把账号体系入口收敛为：

- 登录页：邮箱/用户名 + 密码为主入口。
- 微信登录：仍显示，但明确用于“已绑定微信账号”。
- 注册页：直接展示邮箱/用户名注册表单。
- OAuth / Passkey：不再作为登录页或注册页入口展示。

### 对应实现

- default 登录页：[`web/default/src/features/auth/sign-in/components/user-auth-form.tsx`](../web/default/src/features/auth/sign-in/components/user-auth-form.tsx)
- default 注册页：[`web/default/src/features/auth/sign-up/components/sign-up-form.tsx`](../web/default/src/features/auth/sign-up/components/sign-up-form.tsx)
- classic 登录页：[`web/classic/src/components/auth/LoginForm.jsx`](../web/classic/src/components/auth/LoginForm.jsx)
- classic 注册页：[`web/classic/src/components/auth/RegisterForm.jsx`](../web/classic/src/components/auth/RegisterForm.jsx)
- 对应前端翻译：`web/default/src/i18n/locales/*.json`、`web/classic/src/i18n/locales/*.json`

### 行为变化

- 用户进入登录页后直接看到邮箱/用户名登录表单。
- 微信登录按钮保留，但文案强调“已绑定”。
- 用户进入注册页后直接看到账号注册表单，不再先选择 OAuth、Passkey 或微信注册方式。

## 2. 微信登录策略收紧

### 差异结论

upstream 的微信登录流程允许未绑定微信的用户自动创建账号。当前分支移除了该自动注册路径：

- `wechatId` 已绑定现有用户：允许登录。
- `wechatId` 未绑定任何用户：直接返回失败，不再创建新账号。

### 对应实现

- 微信登录控制器：[`controller/wechat.go`](../controller/wechat.go)

### 行为变化

这会把账号创建路径收敛为“先邮箱/用户名注册，再绑定微信”，避免用户只通过微信登录就隐式生成新账号。

## 3. 注册与账号校验增强

### 3.1 用户名格式限制

#### 差异结论

注册时新增用户名格式限制，只允许：

- 英文字母
- 数字
- 下划线 `_`
- 中划线 `-`

#### 对应实现

- 用户创建逻辑：[`model/user.go`](../model/user.go)

#### 行为变化

不符合 `^[a-zA-Z0-9_-]+$` 的用户名会在后端被拒绝。

### 3.2 邮箱白名单改为逐级域名匹配

#### 差异结论

upstream 的邮箱白名单更偏向完整域名匹配。当前分支改为逐级检查域名后缀。

例如白名单包含 `edu.cn` 时：

- `edu.cn` 命中
- `school.edu.cn` 命中
- `mail.school.edu.cn` 也命中

#### 对应实现

- 域名匹配逻辑：[`controller/misc.go`](../controller/misc.go)

#### 行为变化

邮箱白名单更适合教育邮箱、组织邮箱等存在多级子域名的场景。

### 3.3 注册页教育邮箱提示

#### 差异结论

default 注册表单顶部新增教育邮箱注册提示，用来提前告知用户注册限制。

#### 对应实现

- default 注册页：[`web/default/src/features/auth/sign-up/components/sign-up-form.tsx`](../web/default/src/features/auth/sign-up/components/sign-up-form.tsx)

### 3.4 登录失败提示泛化

#### 差异结论

用户名/密码登录失败时，不再直接暴露“用户已被封禁”等具体原因，而是统一返回更泛化的用户名/密码错误提示。

#### 对应实现

- 登录校验逻辑：[`model/user.go`](../model/user.go)

#### 行为变化

前端无法通过错误文案区分“密码错误”和“账号被禁用”，减少账号状态泄露。

## 4. 新增 OAuth2 / OIDC 授权服务器

### 差异结论

当前分支新增了一套站内 OAuth2 / OIDC 授权服务器能力。它让当前站点可以作为 OAuth2/OIDC Provider，供第三方客户端发起授权登录。

这不是 upstream 已有的“自定义 OAuth”。两者区别是：

- upstream 自定义 OAuth：本站作为 Client，接入外部 GitHub Enterprise、GitLab、Keycloak 等身份提供方。
- 当前新增 OAuth 授权服务器：本站作为 Provider，给 Cherry Studio 或其他第三方客户端提供授权登录。

该能力对应功能分支：`feature/oauth2-authorization-server`。

### 对应实现

- OAuth2/OIDC 路由：[`router/oauth-router.go`](../router/oauth-router.go)
- 授权、换 token、撤销、userinfo、JWKS、discovery 控制器：[`controller/oauth2.go`](../controller/oauth2.go)
- OAuth2/OIDC 核心服务：[`service/oauth2.go`](../service/oauth2.go)
- OAuth access token 中间件：[`middleware/oauth2.go`](../middleware/oauth2.go)
- OAuth client、authorization code、access token、refresh token 数据模型：[`model/oauth2.go`](../model/oauth2.go)
- 数据库迁移接入：[`model/main.go`](../model/main.go)
- OAuth scope 保护的用户资源 API：[`router/api-router.go`](../router/api-router.go)
- OAuth 授权服务器配置：[`setting/system_setting/oauth_server.go`](../setting/system_setting/oauth_server.go)
- default 管理后台配置页：[`web/default/src/features/system-settings/auth/oauth-server-section.tsx`](../web/default/src/features/system-settings/auth/oauth-server-section.tsx)
- default 身份验证菜单注册：[`web/default/src/features/system-settings/auth/section-registry.tsx`](../web/default/src/features/system-settings/auth/section-registry.tsx)

### 暴露端点

- `GET /oauth2/auth`：授权码流程入口。
- `POST /oauth2/token`：使用 authorization code 或 refresh token 换取 token。
- `POST /oauth2/revoke`：撤销 access token 或 refresh token。
- `GET /oauth2/userinfo`：按 scope 返回用户身份信息。
- `GET /oauth2/jwks`：暴露 ID Token 签名公钥。
- `GET /.well-known/openid-configuration`：OIDC discovery 文档。
- `GET /api/v1/oauth/tokens`：OAuth access token 保护的用户令牌列表。
- `GET /api/v1/oauth/balance`：OAuth access token 保护的用户余额信息。

### 核心行为

1. 第三方客户端访问 `/oauth2/auth` 发起授权请求。
2. 未登录用户会被重定向到本站登录页，登录后继续授权流程。
3. 授权码流程支持 PKCE，当前只支持 `S256`。
4. `/oauth2/token` 支持 `authorization_code` 和 `refresh_token`。
5. authorization code、access token、refresh token 均以 HMAC hash 入库，不保存明文。
6. 请求包含 `openid` scope 时签发 RS256 ID Token，并通过 JWKS 暴露公钥。
7. `/oauth2/userinfo` 根据 `openid`、`profile`、`email` 等 scope 返回用户信息。
8. `balance:read`、`tokens:read` 等 scope 可用于访问额外用户资源。

### 管理后台配置项

在 default 管理后台新增：

`系统设置 -> 身份验证 -> OAuth 授权服务器`

可配置：

- 是否启用 OAuth 授权服务器。
- Client Name。
- Client ID。
- Client Secret。
- Redirect URIs。
- Allowed Scopes。
- 是否为 Public Client。
- 是否要求 PKCE。

默认值保持兼容原来的 Cherry Studio public client：

- Client ID：`2a348c87-bae1-4756-a62f-b2e97200fd6d`
- Redirect URI：`cherrystudio://oauth/callback`
- Public Client：开启
- Require PKCE：开启

关闭该功能后，授权、token、userinfo、discovery、JWKS 以及 OAuth access token 保护的资源接口都会拒绝使用。

## 5. 新增支付/充值后自动切换分组

### 差异结论

当前分支新增“普通充值成功后自动切换用户分组”的能力。管理员可以配置累计充值金额阈值和目标分组，用户充值成功后系统按规则自动切换其分组。

该能力对应功能分支：`feature/topup-auto-switch-group`。

### 对应实现

- 支付配置结构：[`setting/operation_setting/payment_setting.go`](../setting/operation_setting/payment_setting.go)
- 自动切换分组策略：[`model/payment_group_policy.go`](../model/payment_group_policy.go)
- 充值完成后应用切换策略：[`model/topup.go`](../model/topup.go)
- 管理端配置接口：[`controller/option.go`](../controller/option.go)
- 管理端路由：[`router/api-router.go`](../router/api-router.go)
- default 支付设置 UI：[`web/default/src/features/system-settings/integrations/payment-settings-section.tsx`](../web/default/src/features/system-settings/integrations/payment-settings-section.tsx)
- default 设置 API 类型与请求封装：[`web/default/src/features/system-settings/api.ts`](../web/default/src/features/system-settings/api.ts)、[`web/default/src/features/system-settings/types.ts`](../web/default/src/features/system-settings/types.ts)

### 管理后台配置项

在 default 管理后台支付设置中新增“充值后自动切换分组”相关配置：

- 是否启用自动切换。
- 是否只统计启用后的新充值。
- 基础分组。
- 阈值金额。
- 目标分组。

### 核心行为

1. 管理员开启“充值后自动切换分组”。
2. 管理员配置基础分组、累计充值阈值和目标分组。
3. 用户普通充值成功后，系统统计该用户成功充值总额。
4. 充值金额统一折算到 USD 口径，避免不同支付渠道金额单位不一致。
5. 系统选择不超过累计充值金额的最高阈值规则，并把用户切换到对应分组。
6. 如果启用“仅统计新充值”，系统只统计启用时间之后的充值。
7. 自动切换只作用于配置链路内的基础分组和目标分组，避免覆盖不相关的手动分组。
8. 如果用户已有有效订阅升级分组，普通充值自动切换不会覆盖订阅升级分组。

## 6. 首页顶部留白调整

### 差异结论

首页内容区增加顶部间距，用于改善嵌入 iframe 时页面内容离顶部太近的问题。

### 对应实现

- default 首页：[`web/default/src/features/home/index.tsx`](../web/default/src/features/home/index.tsx)
- classic 首页：[`web/classic/src/pages/Home/index.jsx`](../web/classic/src/pages/Home/index.jsx)

### 行为变化

首页正文区域与顶部导航之间保留更稳定的垂直距离。对于 URL/iframe 类型首页，default 前端也预留了顶部空间，避免 iframe 内容贴近页面顶部。

## 7. Docker 自动构建发布工作流

### 差异结论

当前分支新增 Docker 自动构建与推送工作流，用于发布流程自动化。

### 对应实现

- Docker 构建工作流：[`/.github/workflows/docker-build.yml`](../.github/workflows/docker-build.yml)

### 核心行为

- 支持 tag push 触发。
- 支持 `workflow_dispatch` 手动触发。
- 登录 Docker Hub。
- 生成镜像标签。
- 构建并推送 Docker 镜像。

该项是工程化能力增强，不改变运行时业务逻辑。

## 最终结论

当前分支相对 upstream/main 的核心新增功能是：

1. **OAuth2 / OIDC 授权服务器**：本站可作为 OAuth/OIDC Provider，支持第三方客户端授权登录，并提供 userinfo、JWKS、discovery、余额和令牌列表等受 scope 控制的资源。
2. **充值后自动切换分组**：用户普通充值成功后，可按累计充值金额自动切换到配置的目标分组。

主要行为调整是：

1. 登录/注册入口收敛为邮箱/用户名主路径，微信仅作为已绑定账号登录方式。
2. 微信登录不再自动注册新用户。
3. 注册用户名、邮箱白名单和登录失败提示策略更严格。
4. 首页增加顶部留白，改善嵌入页面的视觉间距。

发布流程增强是：

1. 新增 Docker 自动构建发布工作流。
