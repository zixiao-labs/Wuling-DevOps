# 身份认证与账号审批

本文档覆盖武陵 DevOps 实例的两个**强相关的运维主题**：

1. **GitHub OAuth 登录** — 让用户用 GitHub 账号登录（也可作为唯一身份源，不开放本地密码注册）。
2. **注册审批** — 任何新账号默认进入 `pending` 状态，必须由管理员在管理后台批准之后才能登录。

两个特性是独立开关、彼此正交，本文档分章节说明，最后给出两种推荐的部署模板。

---

## 1. 配置一览

| 环境变量 | 默认 | 含义 |
|---|---|---|
| `WULING_AUTH_REQUIRE_APPROVAL` | `true` | 新账号必须先被管理员批准才能登录。设为 `false` 时关闭审批，注册即登录。 |
| `WULING_AUTH_OAUTH_AUTO_APPROVE` | `false` | 当审批开关已开启时，是否信任 GitHub 身份并跳过审批队列（仅影响 OAuth 注册的新账号）。 |
| `WULING_OAUTH_GITHUB_CLIENT_ID` | _（空）_ | GitHub OAuth App 的 Client ID。空字符串表示不启用 OAuth；不会出现“使用 GitHub 登录”按钮，OAuth 接口会返回 503。 |
| `WULING_OAUTH_GITHUB_CLIENT_SECRET` | _（空）_ | GitHub OAuth App 的 Client Secret。 |
| `WULING_OAUTH_GITHUB_REDIRECT_URL` | _（空）_ | 必须与 GitHub OAuth App 上配置的 Callback URL **完全一致**（含协议、域名、路径）。范式：`https://<your-domain>/api/v1/auth/oauth/github/callback`。 |
| `WULING_OAUTH_GITHUB_SCOPES` | `read:user,user:email` | 向 GitHub 申请的 scope。逗号分隔，提交到 GitHub 时会被转成空格。 |
| `WULING_OAUTH_FRONTEND_BASE_URL` | `/` | 回调完成后浏览器要跳到的前端基准 URL。同域部署时 `/` 就够用；如果前后端跨域、或部署在子路径下，填完整 URL。 |

所有变量都通过 `caarlos0/env/v11` 读取，遵循全局规则：**生产环境必须从外部环境注入，禁止写到 git 里**。

---

## 2. 配置 GitHub OAuth

### 2.1 在 GitHub 创建 OAuth App

1. 打开 [github.com/settings/developers](https://github.com/settings/developers) → `OAuth Apps` → `New OAuth App`。
2. 字段填法：
   - **Application name**：自定义，会出现在用户授权页（推荐写实例名，比如 `武陵 DevOps · 紫霄实验室`）。
   - **Homepage URL**：你的实例首页，例如 `https://devops.example.com`。
   - **Authorization callback URL**：**必须**是 `https://<your-domain>/api/v1/auth/oauth/github/callback`。**绝不能**带尾部斜杠、查询串或 fragment——GitHub 会精确字符串匹配。
3. 创建后生成 Client ID 并点 `Generate a new client secret` 拿到 Secret。**Secret 只显示一次**，立即拷到你的密钥管理（`pass` / `1Password` / k8s secret / ...）。

### 2.2 填环境变量

最简版（`deploy/production/.env`）：

```bash
WULING_OAUTH_GITHUB_CLIENT_ID=Ov23liXXXXXXXXXXXXXX
# Paste the OAuth App client secret from GitHub here — NOT a Personal Access
# Token (PATs start with `ghp_` / `github_pat_` and won't work for OAuth).
WULING_OAUTH_GITHUB_CLIENT_SECRET=<your_github_oauth_client_secret>
WULING_OAUTH_GITHUB_REDIRECT_URL=https://devops.example.com/api/v1/auth/oauth/github/callback
# WULING_OAUTH_FRONTEND_BASE_URL=https://devops.example.com   # 同域部署可省略
```

### 2.3 验证流程

1. 浏览器打开 `https://devops.example.com/login`。
2. 应该看到 “**使用 GitHub 登录**” 按钮（如果没出现说明前端版本里没有新代码 / 没刷新缓存）。
3. 点击 → 302 跳到 `https://github.com/login/oauth/authorize?...`。
4. 在 GitHub 上点 `Authorize` → 302 跳回 `https://devops.example.com/api/v1/auth/oauth/github/callback?code=...&state=...`。
5. 武陵 DevOps 把你重定向到 `https://devops.example.com/oauth/callback`，并在 URL fragment 里塞 `access_token=...`。
6. 前端的 callback 页面读 fragment、清掉 URL、调 `/api/v1/auth/me`、把 token 存进 localStorage，然后跳到 `/orgs`。

### 2.4 错误排查

- **回到前端后看到 `error=bad_request, error_description=state mismatch`**：通常是浏览器禁用了第三方 cookie 或 `SameSite=Lax` 失效。检查 GitHub 是否把回调指到了同源域（武陵 DevOps）。`/api/v1/auth/oauth/github/start` 设置的 cookie `Path=/api/v1/auth`，跨子域不会带过来。
- **看到 `error=bad_gateway, GitHub token exchange failed: 401`**：CLIENT_ID 或 SECRET 错误，或者 OAuth App 的 Callback URL 与 `WULING_OAUTH_GITHUB_REDIRECT_URL` 不一致。
- **看到 `error=unavailable, GitHub OAuth is not configured on this server`**：环境变量没读进去；检查 `wuling-api` 容器的环境变量、`docker compose config` 输出。
- **每次都跳到 `/oauth/confirm-link`**：邮箱被本地账号占用。这是设计如此 —— 让用户显式选择 “关联到现有账号” 或 “创建新账号（带数字后缀）”，避免不同人的相同邮箱被默认合并。

### 2.5 安全要点

- OAuth state 和 PKCE verifier 存在用 `WULING_JWT_SECRET` 派生的 HMAC 签名 cookie 里（`HttpOnly` + `SameSite=Lax` + 10 分钟 TTL）。**当 JWT secret 漏了或被轮换，所有进行中的 OAuth 流程会失败 —— 这是预期行为。**
- 我们对 GitHub 同时发了 `code_challenge` 和 `code_verifier`（PKCE S256）。GitHub 当前不强制 PKCE，但 OAuth App 本身没坏 —— 加固免费就加固。
- **没有自动邮箱合并**：当 GitHub 返回的邮箱在本地已存在账号时，回调不会偷偷链上去，而是把待处理的链接信息塞进第二个签名 cookie，并跳到 `/oauth/confirm-link` 让用户显式确认。这是 issue #15 验收清单里的要求。

---

## 3. 配置注册审批

### 3.1 开关

```bash
# 默认值 = true。设为 false 完全关闭审批。
WULING_AUTH_REQUIRE_APPROVAL=true

# GitHub OAuth 的账号绕过审批（默认 false，所有人都要审）。
WULING_AUTH_OAUTH_AUTO_APPROVE=false
```

矩阵：

| Approval | OAuthAutoApprove | 密码注册 | GitHub OAuth 注册 |
|---|---|---|---|
| `false` | _（无意义）_ | 注册即登录 | 注册即登录 |
| `true` | `false` | 待审核 | 待审核 |
| `true` | `true` | 待审核 | 注册即登录 |

### 3.2 第一个管理员

新装的实例**没有人是管理员**。最常见的引导方式：

1. 先把 `WULING_AUTH_REQUIRE_APPROVAL` 暂时设为 `false`（或不设，保持默认 `true` 也行，下面解释）。
2. 自己注册第一个账号。
3. 如果走的是审批开启 → 你的账号是 `pending`，但因为没有管理员能批准你，得通过 SQL 手动越过这一步：
   ```sql
   UPDATE users
      SET is_admin = TRUE,
          approval_status = 'approved',
          approved_at = now()
    WHERE username = 'YOUR_USERNAME';
   ```
   （Docker Compose 用 `docker compose exec postgres psql -U wuling -d wuling`。）
4. 现在你以管理员身份登录。注意：`WULING_AUTH_REQUIRE_APPROVAL` 是**环境变量**，必须在部署/环境配置里设置（容器 env、systemd unit、`.env` 等），重启进程后生效，UI 里没有开关。`/admin/users` 只展示按现有 `WULING_AUTH_REQUIRE_APPROVAL` 配置产生的注册状态，并允许你批准/拒绝/调整这些账号，本身并不切换该环境变量。把环境变量改回 `true`（或保持默认）并重启后，剩余的注册都会走正常审批流程，可在 `/admin/users` 处理。

> 推荐：保持 `WULING_AUTH_REQUIRE_APPROVAL=true` 上线，第一个账号用 SQL 提升为 admin。永远不要让一个公开的实例在 `Require=false` 下哪怕跑 5 分钟 —— 任何陌生人都能注册并立即拥有完整账号。

### 3.3 审批工作流

管理员登录后，header 上会多出一个 “管理” 链接，指向 `/admin/users`。该页面：

- **状态过滤**：全部 / 待审核 / 已批准 / 已拒绝。默认进入 “待审核”。
- **待审核行**：右侧两个按钮，`批准` / `拒绝`。
- **已批准行**：可以 `设为管理员` / `取消管理员` / `停用` / `启用`。
- **已拒绝行**：可以 `重新批准`（再次给一次机会）。
- **当前账号**：不显示任何按钮（避免把自己 demote 锁出去）。后端也会拒绝任何让 “最后一个活跃管理员” 失活/降权/未批准的 PATCH。

被拒绝的用户登录时会看到 `403 forbidden — account registration was rejected`。等待审批的用户看到 `403 forbidden — account is pending admin approval`。两条错误都出现在密码校验**之后**，也就是说接口确实只会向账号本人泄露这一信息，匿名 enumeration 拿到的依然是统一的 `invalid credentials`。

### 3.4 数据库字段

```sql
ALTER TABLE users ADD COLUMN approval_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (approval_status IN ('pending', 'approved', 'rejected'));
ALTER TABLE users ADD COLUMN approval_note   TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN approved_at     TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN approved_by     UUID REFERENCES users(id) ON DELETE SET NULL;
```

升级现有部署时迁移会把**所有**已存在的 `pending` 行回填为 `approved`，避免现有用户在升级后被锁在外面。

### 3.5 自动化（API）

`/api/v1/admin/users` 全部需要 admin JWT，可用于脚本化批准（CI 任务、Slack bot 等）：

```bash
# 列出待审核
curl -H "Authorization: Bearer $ADMIN_JWT" \
     "https://devops.example.com/api/v1/admin/users?status=pending"

# 批准用户 X
curl -X PATCH -H "Authorization: Bearer $ADMIN_JWT" \
     -H "Content-Type: application/json" \
     -d '{"approval_status":"approved"}' \
     "https://devops.example.com/api/v1/admin/users/$USER_ID"

# 提升为 admin
curl -X PATCH -H "Authorization: Bearer $ADMIN_JWT" \
     -H "Content-Type: application/json" \
     -d '{"is_admin":true}' \
     "https://devops.example.com/api/v1/admin/users/$USER_ID"
```

---

## 4. 推荐部署模板

### 4.1 内部小团队（≤ 20 人）

```bash
WULING_AUTH_REQUIRE_APPROVAL=true
WULING_AUTH_OAUTH_AUTO_APPROVE=false
# OAuth 留空 → 仅本地密码注册
```

- 注册量很低 → 审批不会成为瓶颈。
- 没人在 GitHub 上 → 不需要 OAuth。

### 4.2 公司内 + GitHub 单点登录

```bash
WULING_AUTH_REQUIRE_APPROVAL=true
WULING_AUTH_OAUTH_AUTO_APPROVE=true       # 已经被 GitHub 验证过了
WULING_OAUTH_GITHUB_CLIENT_ID=...
WULING_OAUTH_GITHUB_CLIENT_SECRET=...
WULING_OAUTH_GITHUB_REDIRECT_URL=https://devops.example.com/api/v1/auth/oauth/github/callback
```

- 密码注册的人还是要审批（防止从公网爬取邮箱白嫖）。
- 走 GitHub 登录的人直接放行（默认已经在企业 GitHub 组织里）。
- 进一步收紧的话，可以让 reverse proxy 在 `/api/v1/auth/register` 上做 IP allowlist，把密码注册完全关掉。

### 4.3 完全开放（教学/Demo 实例）

```bash
WULING_AUTH_REQUIRE_APPROVAL=false
# OAuth 可选
```

不要在生产上这么干 —— 仅用于 demo。

---

## 5. FAQ

**Q: 我把所有管理员都意外删了/拒了，怎么办？**
A: 后端有“最后一位活跃管理员”保护（`409 conflict, refusing to demote or disable the last active admin`）。但如果数据库已经被手动改坏了，可以直接 SQL：
```sql
UPDATE users SET is_admin = TRUE, is_active = TRUE, approval_status = 'approved' WHERE username = '...';
```

**Q: OAuth 的 access_token 出现在浏览器历史里吗？**
A: 不会。它放在 URL fragment（`#access_token=...`），浏览器永远不会把 fragment 发回服务器，而且我们的 callback 页面在读完 fragment 后会立即 `history.replaceState(null, '', pathname)` 把 URL 改回 `/oauth/callback`。

**Q: 关掉某用户的 `is_active` 之后，他签发的 PAT/JWT 还能用吗？**
A: 走 `/api/v1/admin/users/{id}` 设置 `is_active=false` 之后：
- **密码登录**：立刻失败，403。
- **JWT bearer**：当前签发的 JWT 在到期前**仍然有效**（武陵 DevOps 不维护 server-side session table，这是 stateless JWT 的固有取舍）。要立即吊销，把 `WULING_JWT_SECRET` 轮换 —— 会让所有人退出登录。
- **PAT / Git basic auth**：`PasswordHashFor` 已经会过滤 `is_active=false` 和 `approval_status<>approved`，所以 git push/pull 会立刻 401。

**Q: 我想完全禁掉密码注册，只允许 GitHub OAuth 登录。**
A: 当前没有专门的开关；最简单的办法是在 Caddy / nginx / k8s ingress 上把 `/api/v1/auth/register` 直接 `respond 404`，或者用 GrowthBook/feature flag 在前端隐掉注册页。后续如果有需求可以加 `WULING_AUTH_DISABLE_PASSWORD_REGISTER=true`。
