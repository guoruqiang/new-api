# 与 upstream/main 的主要功能与行为差异（含对应实现）

> 对比范围：`upstream/main...HEAD`
>
> 说明：
> 1. 本文只保留当前分支相对上游主线的功能、行为、入口和 UI 可见差异。
> 2. README、lockfile、以及仅换行/格式层面的变更不展开。
> 3. 每一项都补充对应实现位置和核心逻辑，便于后续核对。

## 总结

当前分支相对 `upstream/main` 的主要差异可以概括为：

1. 登录页改为“邮箱/用户名优先”，微信登录仅限已绑定用户；但其他 OAuth / Passkey 登录入口并未删除。
2. 微信登录不再允许未绑定用户自动注册，只允许已存在且已绑定的用户登录。
3. 注册页被收敛为邮箱注册主路径，并新增“仅限教育邮箱注册”的前端提示。
4. 后端注册校验增加用户名正则限制，邮箱白名单改为逐级域名匹配；同时登录失败提示语也被统一为更泛化的文案。
5. 顶部导航新增“点我聊天”，Playground 改成仅管理员可见，首页内容区增加了顶部留白。
6. 新增 Docker 自动构建发布工作流。

## 1. 登录入口调整

### 变更结论

登录页把邮箱/用户名登录放到了最前面，作为默认推荐入口；微信登录仍保留，但被明确标注为仅适用于已绑定微信的账号。同时，其他 OAuth / Passkey 登录方式并没有被删除，只是在视觉优先级上被放到了邮箱登录之后。

### 对应实现

- 登录页按钮顺序调整：[`web/src/components/auth/LoginForm.jsx:520-658`](../web/src/components/auth/LoginForm.jsx#L520-L658)
- 微信按钮文案改为“使用 微信 登录（已绑定微信）”：[`web/src/components/auth/LoginForm.jsx:538-550`](../web/src/components/auth/LoginForm.jsx#L538-L550)
- GitHub / Discord / OIDC / LinuxDO / 自定义 OAuth / Telegram / Passkey 仍保留条件渲染：[`web/src/components/auth/LoginForm.jsx:553-658`](../web/src/components/auth/LoginForm.jsx#L553-L658)

### 核心逻辑

- 邮箱/用户名登录按钮被直接放在登录卡片最上方。
- 微信登录按钮继续显示，但文案显式强调“已绑定微信”。
- 其余登录方式仍然受各自状态开关控制，只要后端状态开启，入口仍会显示。

因此，准确说法应是：

- 登录页强化了邮箱/用户名登录和微信登录
- 但并没有彻底移除其他登录方式

## 2. 微信登录策略调整

### 变更结论

微信登录逻辑已经收紧为：

- 只有已经绑定微信的现有用户可以登录
- 未绑定微信的用户不能再通过微信自动注册账号

### 对应实现

- 微信登录主逻辑：[`controller/wechat.go:55-107`](../controller/wechat.go#L55-L107)

### 核心逻辑

`WeChatAuth` 的核心分支已经变成：

1. 先通过验证码换取 `wechatId`
2. 如果 `wechatId` 已经存在，则根据微信 ID 反查用户并执行登录
3. 如果 `wechatId` 不存在，则直接返回失败信息，不再创建新用户

也就是说，上游原本的“未绑定微信时自动创建账号”路径已经被移除，账号创建流程被收敛为“先邮箱注册、后绑定微信”。

## 3. 注册入口与注册校验调整

### 3.1 注册页被固定为邮箱注册主路径

#### 变更结论

注册页现在直接渲染邮箱注册表单，不再保留原先“先展示其他注册方式，再切邮箱注册”的主路径。

#### 对应实现

- 固定直接渲染邮箱注册表单：[`web/src/components/auth/RegisterForm.jsx:797-799`](../web/src/components/auth/RegisterForm.jsx#L797-L799)
- “其他注册选项”入口整段被注释掉：[`web/src/components/auth/RegisterForm.jsx:705-727`](../web/src/components/auth/RegisterForm.jsx#L705-L727)

#### 核心逻辑

原本 `showEmailRegister` / `hasOAuthRegisterOptions` 对页面主显示路径的控制，已经被替换为直接渲染 `renderEmailRegisterForm()`。这意味着从产品入口层面看，邮箱注册已经成为默认且唯一的主展示路径。

### 3.2 注册页新增教育邮箱提示

#### 变更结论

注册表单顶部新增“本站仅限教育邮箱（如 edu.cn）注册”的提示文案。

#### 对应实现

- 提示文案位置：[`web/src/components/auth/RegisterForm.jsx:411-414`](../web/src/components/auth/RegisterForm.jsx#L411-L414)
- 邮箱注册表单顶部同样展示该提示：[`web/src/components/auth/RegisterForm.jsx:579-582`](../web/src/components/auth/RegisterForm.jsx#L579-L582)

#### 核心逻辑

这是一个前端可见引导变化，用于在用户提交前就提前说明注册限制，和后端邮箱白名单限制形成前后呼应。

### 3.3 用户名新增正则校验

#### 变更结论

注册时新增用户名格式限制，只允许字母、数字、下划线 `_` 和中划线 `-`。

#### 对应实现

- 用户创建逻辑中的用户名正则校验：[`model/user.go:380-397`](../model/user.go#L380-L397)

#### 核心逻辑

在 `User.Insert` 中，用户密码处理后会先执行正则校验：

- 正则：`^[a-zA-Z0-9_-]+$`
- 不满足则直接返回错误
- 通过后才继续后续注册流程

这意味着用户名格式限制是在后端真正落地的，而不仅仅是前端提示。

### 3.4 邮箱白名单校验改为逐级域名匹配

#### 变更结论

邮箱白名单逻辑不再只是完整域名等值匹配，而是改成逐级检查域名后缀。

#### 对应实现

- 域名逐级检查函数：[`controller/misc.go:231-243`](../controller/misc.go#L231-L243)
- 在发送邮箱验证码时调用该逻辑：[`controller/misc.go:265-270`](../controller/misc.go#L265-L270)

#### 核心逻辑

`isDomainAllowed` 会把域名按 `.` 切分后逐级拼接并与白名单比较。例如：

- `a.b.edu.cn`
- 会依次检查 `a.b.edu.cn`、`b.edu.cn`、`edu.cn`

只要其中任一层级命中白名单，就视为允许。相比上游的“完整域名必须完全相等”，这会让白名单判断更接近真实使用场景。

### 3.5 登录失败提示语被统一为更泛化的错误文案

#### 变更结论

用户名/密码登录失败时，错误提示不再直接返回“或用户已被封禁”，而是统一成更泛化的用户名/密码错误提示。

#### 对应实现

- 登录校验失败返回文案：[`model/user.go:611-615`](../model/user.go#L611-L615)

#### 核心逻辑

`ValidateAndFill` 中，只要密码不匹配，或者用户状态不是启用状态，都会统一返回同一条错误信息。这使前端无法再从该提示中直接区分“密码错误”和“用户被封禁”。

## 4. 入口与页面可见性调整

### 4.1 顶部导航新增“点我聊天”

#### 变更结论

顶部导航新增“点我聊天”入口，直接跳转到 `/console/chat/0`。

#### 对应实现

- 顶部导航配置：[`web/src/hooks/common/useNavigation.js:47-56`](../web/src/hooks/common/useNavigation.js#L47-L56)

#### 核心逻辑

在导航项数组中直接插入了一个新的菜单项，目标地址为聊天页。这个变化本质上是把聊天能力前置，缩短从首页进入核心使用场景的路径。

### 4.2 Playground 改成仅管理员可见

#### 变更结论

侧边栏中的“操练场”不再对普通用户默认显示，仅管理员可见。

#### 对应实现

- 侧边栏可见性控制：[`web/src/components/layout/SiderBar.jsx:204-210`](../web/src/components/layout/SiderBar.jsx#L204-L210)

#### 核心逻辑

`playground` 菜单项新增了 `className: isAdmin() ? '' : 'tableHiddle'`，也就是非管理员会直接被隐藏入口。功能本身仍存在，但入口权限发生了变化。

### 4.3 首页内容区增加顶部留白

#### 变更结论

首页内容区域增加了顶部间距，属于一个较小但实际存在的 UI 差异。

#### 对应实现

- 首页容器样式调整：[`web/src/pages/Home/index.jsx:337-345`](../web/src/pages/Home/index.jsx#L337-L345)

#### 核心逻辑

主容器从 `overflow-x-hidden w-full` 改成 `overflow-x-hidden w-full pt-[40px]`，因此页面在垂直方向上会多出一段顶部空白。

## 5. Docker 自动构建发布工作流

### 变更结论

项目新增了单独的 Docker 自动构建与推送工作流。

### 对应实现

- 新增工作流文件：[`/.github/workflows/docker-build.yml:1-51`](../.github/workflows/docker-build.yml#L1-L51)

### 核心逻辑

该工作流的执行逻辑是：

1. 在 tag push 时触发
2. 也支持手动触发 `workflow_dispatch`
3. 登录 Docker Hub
4. 生成镜像标签
5. 构建并推送 Docker 镜像

这属于工程化和发布流程上的增强，不影响业务功能，但属于相对上游明确存在的行为差异。

## 结论

如果按 `upstream/main...HEAD` 来看，当前分支相对上游主线的更准确结论应当是：

1. 登录页强化了邮箱/用户名登录，并明确微信仅用于已绑定用户登录。
2. 其他 OAuth / Passkey 登录入口并未被彻底删除，因此“只保留邮箱和微信登录”这一说法并不准确。
3. 注册页被收敛为邮箱注册主路径，并新增教育邮箱提示。
4. 注册后端增加了用户名正则限制，并优化了邮箱白名单的域名匹配逻辑。
5. 登录失败提示语被统一为更泛化的文案。
6. 顶部导航新增聊天入口，Playground 改成仅管理员可见，首页有轻微 UI 调整。
7. 新增 Docker 自动构建与发布工作流。

这版内容更适合作为“当前分支相对 upstream/main 的准确功能差异说明”。