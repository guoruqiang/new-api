# OAuth2 Authorization Server TODO List

## Goal

为项目补一套可供第三方客户端接入的 OAuth2/OIDC 授权流程，优先支持如下场景：

- Authorization Code + PKCE
- OIDC scopes: `openid profile email`
- Refresh token / `offline_access`
- 自定义业务 scopes: `balance:read`、`usage:read`、`tokens:read`、`tokens:write`
- 兼容类似 Cherry Studio 的回调方式，例如：`cherrystudio://oauth/callback`

## MVP Scope

首期以最小可用能力为目标，优先支持：

- `GET /oauth2/auth`
- `POST /oauth2/token`
- `GET /oauth2/userinfo`
- `GET /.well-known/openid-configuration`
- `GET /oauth2/jwks`

## Todo

### 1. 需求与协议边界

- [x] 明确首期只支持 Authorization Code + PKCE，不支持 Implicit / Device Code / Client Credentials
- [x] 明确是否要求完整 OIDC discovery 与 ID Token
- [x] 明确 scope 语义与资源访问权限映射
- [x] 明确第三方客户端类型：public client / confidential client

### 2. 数据模型设计

- [x] 设计 `oauth_clients` 表：`client_id`、`client_secret`、`redirect_uris`、`allowed_scopes`、`is_public`、`require_pkce`、状态等
- [x] 设计 `oauth_authorization_codes` 表：授权码、用户、客户端、redirect_uri、scopes、PKCE 参数、过期时间、使用状态
- [x] 设计 `oauth_access_tokens` 表：token、用户、客户端、scopes、过期时间、撤销状态
- [x] 设计 `oauth_refresh_tokens` 表：refresh token、关联 access token / grant、过期时间、撤销状态
- [x] 确保模型与迁移兼容 SQLite / MySQL / PostgreSQL

### 3. 路由与分层

- [x] 在根路径挂载 OAuth2/OIDC 路由，而不是放到 `/api` 下
- [x] 按项目分层新增 Router / Controller / Service / Model
- [x] 规划授权端点、令牌端点、userinfo、discovery、jwks 的职责边界

### 4. 授权流程与登录衔接

- [x] 设计未登录时进入 `/oauth2/auth` 的处理流程
- [x] 设计已登录时的授权确认页/自动授权策略
- [ ] 设计用户拒绝授权时的错误回调
- [ ] 评估当前 session 登录态对 OAuth 浏览器跳转场景的适配性
- [x] 评估 `SameSite=Strict` 对授权流程的影响

### 5. Authorization Code + PKCE

- [x] 校验 `client_id`
- [x] 精确校验 `redirect_uri`
- [x] 校验 `response_type=code`
- [x] 校验与保存 `state`
- [x] 校验 `code_challenge` / `code_challenge_method=S256`
- [x] 生成短时效、一次性授权码
- [x] 成功后重定向回客户端并附带 `code` 与 `state`

### 6. Token 交换与刷新

- [x] 实现 `/oauth2/token` 的 authorization_code 流程
- [x] 使用 `code_verifier` 校验 PKCE
- [x] 签发 access token
- [x] 按 `offline_access` 签发 refresh token
- [x] 如启用 OIDC，则签发 ID Token
- [x] 规划 refresh_token 刷新流程与轮换策略

### 7. OIDC 能力

- [x] 实现 `/.well-known/openid-configuration`
- [x] 实现 `/oauth2/userinfo`
- [x] 实现 `/oauth2/jwks`
- [x] 明确 ID Token 的 claims：`sub`、`iss`、`aud`、`exp`、`iat`、`email`、`name` 等

### 8. 资源访问与 scope 校验

- [x] 设计 Bearer Token 认证中间件或复用现有认证体系扩展
- [ ] 为 `balance:read`、`usage:read`、`tokens:read`、`tokens:write` 建立权限校验
- [ ] 确认哪些现有接口需要暴露为 OAuth 受保护资源

### 9. 客户端管理

- [ ] 设计管理端客户端创建/编辑/禁用能力
- [ ] 支持配置允许的 redirect URIs 与 scopes
- [ ] 支持 public client 场景下无 client secret

### 10. 安全与测试

- [ ] 确保 redirect_uri 不能模糊匹配
- [ ] 确保授权码单次使用且短时有效
- [ ] 确保 token 可撤销、可过期、可审计
- [ ] 增加关键流程测试：授权成功、拒绝授权、PKCE 校验失败、redirect_uri 不匹配、code 复用、refresh token 刷新
- [ ] 验证跨数据库兼容性

## Current Status

- 当前实现先以文档中的固定 public client（Cherry Studio 回调）落地最小闭环。
- 用户拒绝授权回调、管理端客户端管理、更多受保护资源暴露仍待继续补完。
- `usage:read` / `tokens:write` scope 已纳入 access token scope 体系，但对应资源接口尚未继续扩展。

## Notes

- 现有 `/api/oauth/:provider` 是“作为 OAuth 客户端接第三方登录”，不是“作为 OAuth2 授权服务器给第三方接入”。
- 本次工作需要补的是一套新的 Authorization Server / OIDC Provider 流程。
- 首期目标应以可用、标准、最小闭环为主，避免一次性铺开过多非必要协议能力。
