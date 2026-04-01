# NWAFUER OAuth / New-API 对接文档

这是一份给 `new-api` 后端开发直接使用的精简版接口文档。

目标：

1. Cherry Studio 点击 `登录 NWAFUER`
2. 走 OAuth 授权码登录
3. 登录成功后换取 `access_token`
4. 再用 `access_token` 获取用户 API Key
5. 用 API Key 调用 `/models` 和模型推理接口

## 固定参数

- `client_id`: `2a348c87-bae1-4756-a62f-b2e97200fd6d`
- `redirect_uri`: `cherrystudio://oauth/callback`
- `response_type`: `code`
- `scope`: `openid profile email offline_access balance:read usage:read tokens:read tokens:write`

## 需要实现的接口

后端至少需要提供 6 个接口：

1. `GET /oauth2/auth`
2. `POST /oauth2/token`
3. `POST /oauth2/revoke`
4. `GET /api/v1/oauth/tokens`
5. `GET /api/v1/oauth/balance`
6. `GET /models`

## 1. OAuth 授权接口

### 请求

`GET /oauth2/auth`

查询参数：

- `client_id`
- `redirect_uri`
- `response_type=code`
- `scope`
- `state`
- `code_challenge`
- `code_challenge_method=S256`

### 要求

- 支持标准 OAuth2 Authorization Code + PKCE
- 登录成功后重定向到：

```txt
cherrystudio://oauth/callback?code=AUTH_CODE&state=STATE
```

- 登录失败时重定向到：

```txt
cherrystudio://oauth/callback?error=access_denied&error_description=ERROR_MESSAGE
```

## 2. Token 交换接口

### 2.1 授权码换 token

`POST /oauth2/token`

请求头：

```http
Content-Type: application/x-www-form-urlencoded
```

请求体：

```txt
grant_type=authorization_code
client_id=2a348c87-bae1-4756-a62f-b2e97200fd6d
code=AUTH_CODE
redirect_uri=cherrystudio://oauth/callback
code_verifier=CODE_VERIFIER
```

返回：

```json
{
  "access_token": "access-token",
  "refresh_token": "refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600
}
```

### 2.2 refresh token 刷新

同一个接口：

`POST /oauth2/token`

请求体：

```txt
grant_type=refresh_token
refresh_token=REFRESH_TOKEN
client_id=2a348c87-bae1-4756-a62f-b2e97200fd6d
```

返回格式同上。

## 3. Token 撤销接口

`POST /oauth2/revoke`

请求头：

```http
Content-Type: application/x-www-form-urlencoded
```

请求体：

```txt
token=ACCESS_TOKEN
token_type_hint=access_token
```

说明：

- 这个接口建议实现
- 即使失败，客户端也会清本地状态

## 4. 获取 API Key 接口

登录成功后，客户端会用 `access_token` 请求：

`GET /api/v1/oauth/tokens`

请求头：

```http
Authorization: Bearer ACCESS_TOKEN
```

返回格式支持以下 3 种任意一种。

### 格式 A

```json
["sk-xxx", "sk-yyy"]
```

### 格式 B

```json
[
  { "key": "sk-xxx" },
  { "key": "sk-yyy" }
]
```

### 格式 C

```json
{
  "data": [
    { "token": "sk-xxx" },
    { "token": "sk-yyy" }
  ]
}
```

建议：

- 最简单就返回一个 key
- 如果系统支持多 key，也可以返回多个

## 5. 获取余额接口

`GET /api/v1/oauth/balance`

请求头：

```http
Authorization: Bearer ACCESS_TOKEN
```

返回格式必须为：

```json
{
  "success": true,
  "data": {
    "quota": 500000,
    "used_quota": 0
  }
}
```

说明：

- 客户端显示余额时使用公式：

```txt
balance = quota / 500000
```

- 也就是 `quota=500000` 会显示 `$1.00`

## 6. 获取模型列表接口

`GET /models`

请求头：

```http
Authorization: Bearer API_KEY
```

说明：

- 这里用的是上一步 `/api/v1/oauth/tokens` 返回的 API Key
- 不是 OAuth 的 `access_token`

### 推荐返回格式

```json
{
  "data": [
    {
      "id": "gpt-5.4",
      "name": "GPT 5.4",
      "display_name": "GPT 5.4",
      "owned_by": "NWAFUER",
      "supported_endpoint_types": ["openai-response"]
    },
    {
      "id": "deepseek-chat",
      "name": "deepseek-chat",
      "display_name": "DeepSeek Chat",
      "owned_by": "NWAFUER",
      "supported_endpoint_types": ["openai"]
    },
    {
      "id": "deepseek-reasoner",
      "name": "deepseek-reasoner",
      "display_name": "DeepSeek Reasoner",
      "owned_by": "NWAFUER",
      "supported_endpoint_types": ["openai"]
    }
  ]
}
```

## 模型字段要求

每个模型至少建议提供：

- `id`
- `name`
- `display_name`
- `owned_by`
- `supported_endpoint_types`

其中 `supported_endpoint_types` 很关键。

允许值：

- `openai`
- `openai-response`
- `anthropic`
- `gemini`
- `image-generation`

## 推荐模型映射

- `gpt-5`: `["openai-response"]`
- `gpt-5.4`: `["openai-response"]`
- `gpt-5.3-codex`: `["openai-response"]` 或 `["openai"]`
- `deepseek-chat`: `["openai"]`
- `deepseek-reasoner`: `["openai"]`

如果有 Claude 兼容模型：

- 返回 `["anthropic"]`

如果有 Gemini 兼容模型：

- 返回 `["gemini"]`

如果有绘图模型：

- 返回 `["image-generation"]`

## 当前建议至少支持的模型

为了和当前桌面端默认配置匹配，建议 `/models` 至少返回：

- `gpt-5`
- `gpt-5.4`
- `gpt-5.3-codex`
- `deepseek-chat`
- `deepseek-reasoner`

## 域名要求

当前客户端最省事的兼容方式是：以下接口能在同一个域名下访问：

- `/oauth2/auth`
- `/oauth2/token`
- `/oauth2/revoke`
- `/api/v1/oauth/tokens`
- `/api/v1/oauth/balance`

如果你们实际是前台域名和 API 域名分离，也可以通过网关或反向代理把这些路径统一暴露出来。

## 最小验收标准

后端完成后，应该至少能验证以下结果：

1. 点击 `登录 NWAFUER` 能打开登录页
2. 登录成功后能回调 `cherrystudio://oauth/callback`
3. 客户端能成功拿到 `access_token`
4. 客户端能成功拿到 API Key
5. 设置页能显示余额
6. 欢迎页登录后能自动拉取模型
7. `gpt-5.4` 可以正常对话
8. `deepseek-chat` 可以正常用于翻译

## 常见失败点

### 回调失败

检查：

- `redirect_uri` 是否登记为 `cherrystudio://oauth/callback`
- 授权完成后是否真的跳转了这个 URI

### 拿不到 API Key

检查：

- `/api/v1/oauth/tokens` 是否校验并接受 `Bearer access_token`
- 返回 JSON 是否符合文档要求

### 拉不到模型

检查：

- `/models` 是否返回 `{ "data": [...] }`
- 模型对象是否有有效 `id`
- 是否提供 `supported_endpoint_types`

### 模型能显示但不能调用

检查：

- `supported_endpoint_types` 是否和真实协议匹配
- `gpt-5.4` 是否应该走 `openai-response`
- `deepseek-chat` 是否应该走 `openai`
