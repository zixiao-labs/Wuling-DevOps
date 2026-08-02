# GitHub 集成：Webhook 触发流水线 + 仓库自动同步

本文只讲**运维要改什么**。代码契约见 `docs/pipelines.md`。

本实例复用**已经存在**的组织级 GitHub App（不要新建）：

| 项 | 值 |
|----|----|
| App 名称 | **Wuling DevOps** |
| App ID | `3713023` |
| Client ID | `Iv23lijBzt0DlGTEwC9v` |

这个 App 目前只用于 **OAuth 登录**（见 `docs/auth.md`）。要让它同时能触发流水线和同步仓库，
需要在**同一个 App** 上补三样东西：**私钥**、**Webhook**、**权限与事件订阅**。

---

## 0. 先说一个最容易踩的坑

> ⚠️ **改完权限不会立即生效。** GitHub 要求每一个**已安装**该 App 的账户/组织**显式接受**新权限，
> 之后新权限才对该安装生效。改完权限后，管理员会收到一封邮件；也可以直接去
> `https://github.com/organizations/<org>/settings/installations` → 该 App → **Review request**
> → **Accept new permissions**。
>
> 在没人点「接受」之前，App 拿到的 installation token **仍然是旧权限**，表现为：Webhook 能收到、
> 签名能过，但 `git clone` 或 API 调用返回 **404**（GitHub 对无权限资源返回 404 而非 403，
> 所以这个故障看起来像「仓库不存在」，很容易误诊）。

---

## 1. 进入 App 设置页

组织右上角 → **Your organizations** → 目标组织 → **Settings** → 左栏 **Developer settings** →
**GitHub Apps** → **Wuling DevOps** 右侧 **Edit**。

---

## 2. 生成私钥（App 认证用）

页面下方 **Private keys** → **Generate a private key**，浏览器会下载一个 `.pem`。

这把私钥用来签 JWT（RS256，`iss` = App ID `3713023`，`exp` 最长 10 分钟），再用 JWT 换
**installation access token**；同步仓库时以
`https://x-access-token:<installation_token>@github.com/<owner>/<repo>.git` 的形式 clone/fetch。

> 私钥只能下载一次，且等价于 App 的完整身份。**不要**进 git。按 `docs/pipelines.md` §4 的做法，
> 存成 org Secret 或部署环境变量。旧私钥可以在同一页面吊销，支持多把并存以便轮换。

---

## 3. Webhook

同一页 **Webhook** 区块：

| 字段 | 填什么 |
|----|----|
| **Active** | ✅ 勾上（默认可能是关的） |
| **Webhook URL** | `https://<你的域名>/api/v1/webhooks/github` |
| **Webhook secret** | 一段高熵随机串，例如 `openssl rand -hex 32` |
| **SSL verification** | 保持 **Enable**（不要关） |

Secret 同时要配到控制面（见下方 §5）。服务端按 **`X-Hub-Signature-256`** 头做
HMAC-SHA256 校验并**常量时间**比较；校验不过一律 401。

> 注意：GitHub 同时还会发一个 legacy 的 `X-Hub-Signature`（SHA-1）。**只认 SHA-256 的那个**，
> 不要因为旧教程里写的是 `X-Hub-Signature` 就去校验 SHA-1。
>
> 本地调试可以先把 URL 填 smee.io 的代理地址，再 `npx smee -u <URL> -t http://localhost:8080/api/v1/webhooks/github` 转发；**生产不要用 smee**。

---

## 4. 权限与事件订阅

### Repository permissions

| 权限 | 级别 | 为什么 |
|----|----|----|
| **Metadata** | Read-only | 强制项，GitHub 自动带上 |
| **Contents** | **Read-only** | 同步仓库要 clone/fetch，读 `.wuling/workflows/*.yml` 也要它 |
| **Pull requests** | **Read-only** | `pull_request` 事件触发流水线要读 PR 的 head/base |
| **Checks** | **Read and write** | 把流水线结果回显到 PR 的 **Checks** 页（结论、摘要、行内注解、重跑按钮） |

> 只想「GitHub 推送 → 武陵跑流水线」、不需要回显的话，去掉 Checks 即可，其余只读权限就够。
> 需要回显就必须给 Checks **读写**——这是唯一需要写权限的地方，`Contents` 仍然保持只读。

### Subscribe to events

勾选：

- ✅ **Push** —— 推送触发流水线 + 同步仓库
- ✅ **Pull request** —— PR 打开/同步时触发
- ✅ **Check suite** —— 有人推代码时 GitHub 自动建 check suite，`requested` / `rerequested` 是我们建 check run 的信号
- ✅ **Check run** —— check run 建好后的 `created` 是「可以开跑」的信号；`rerequested` 是单条重跑；`requested_action` 是自定义按钮被点
- ✅ **Repository** —— 仓库改名、转移时更新映射

另外两个**不用勾**、也勾不了，但一定会收到，服务端必须能处理：

- `ping` —— 保存 Webhook 时 GitHub 立刻发一次，用来验证连通性与签名。回 200 即可。
- `installation` / `installation_repositories` —— 安装、卸载、增删可访问仓库时发送。
  用它来维护「GitHub 仓库 ↔ 武陵仓库」的映射，以及在卸载时**停用**对应映射。

改完点页面底部 **Save changes**，然后回到 §0 去**接受新权限**。

---

## 4.1 Checks 回显的三个坑

实现 check run 回显时，下面三条都是「不知道就会中招」的：

1. **`check_run` 事件是广播的。** GitHub 会把 check run 事件发给该仓库上**所有**有 Checks 权限的
   App，不只是发给创建它的那个。所以处理前必须先比对
   `payload.check_run.app.id == 3713023`，否则会去响应别家 App（甚至 GitHub Actions）的 check run。
   `check_suite` 的 `requested` / `rerequested` 则只发给被请求的 App，不需要这层过滤。

2. **注解一次最多 50 条。** `output.annotations` 每请求上限 50；要报更多就得多次 `PATCH`
   同一个 check run，每次追加一批（注解是累加的，不是覆盖）。武陵这边一个 matrix 展开后的
   run 很容易超过 50 条，必须分批，不能截断了事。
   另外 `start_column` / `end_column` **只有在 `start_line == end_line` 时才允许带**，跨行注解带列会被拒。

3. **`conclusion` 的取值是固定枚举**：`success` / `failure` / `neutral` / `cancelled` /
   `timed_out` / `skipped` / `action_required`。武陵的 job 状态机是
   `success | failed | canceled`，映射时注意 **`failed`→`failure`、`canceled`→`cancelled`**
   （GitHub 用的是英式双 l 拼写），拼错会被 API 拒绝。

可选：在 `output.actions` 里加按钮（如「重跑」），点击后收到 `check_run.requested_action`，
按 `requested_action.identifier` 分发。`actions` 最多 3 个。

---

## 5. 控制面侧配置

对应的环境变量（完整表见 `docs/pipelines.md` §9）：

| 变量 | 值 |
|----|----|
| `WULING_GITHUB_APP_ID` | `3713023` |
| `WULING_GITHUB_APP_PRIVATE_KEY` | §2 下载的 PEM 全文（或 `WULING_GITHUB_APP_PRIVATE_KEY_PATH` 指向文件） |
| `WULING_GITHUB_WEBHOOK_SECRET` | §3 里填的那串 secret |

控制面在 `WULING_GITHUB_WEBHOOK_SECRET` 非空时挂载
`POST /api/v1/webhooks/github`（HMAC 验签 + `X-GitHub-Delivery` 幂等）。
要对某个武陵仓库处理 `push` / `pull_request` / Checks，还需用维护者身份调用：

`PUT /api/v1/orgs/{org}/projects/{project}/repos/{repo}/github-link`

```json
{ "owner": "acme", "name": "app", "installation_id": 12345678 }
```

未绑定的 GitHub 仓库事件会被忽略（不 5xx）。

`WULING_OAUTH_GITHUB_CLIENT_ID` / `..._SECRET` 保持不变——**登录走的仍然是同一个 App 的 OAuth 凭据**，
这里新增的是 App 自身的身份（JWT 私钥），两者并存、互不影响。

---

## 6. 验证

1. **Webhook 通没通**：App 设置页 → **Advanced** → **Recent Deliveries**。保存时那次 `ping`
   应该是绿色 200。红色就点进去看 Response——401 基本是 secret 对不上，404 是 URL 错了。
2. **权限接受了没**：组织 → Settings → **GitHub Apps**（installations）→ 该 App 下若还挂着
   *Request pending* 就是没接受，回 §0。
3. **端到端**：往一个已安装该 App、且含 `.wuling/workflows/*.yml` 的仓库推一个 commit，
   在武陵的流水线页面应能看到新 run。Delivery 里能看到 `push` 事件是绿的、但武陵没建 run，
   说明事件收到了而 workflow 发现或映射有问题——查控制面日志，不要再去动 App 设置。
4. **回显通没通**：在该仓库开一个 PR，**Checks** 页应出现名为「武陵 CI」的 check run（queued）。
   当前 MVP 只在 `check_suite` 上 `CreateCheckRun`；随流水线推进到 in progress /
   conclusion 的 `UpdateCheckRun` 尚未接到 pipeline 终态（见 handoff 后续增强）。
   PR 上没出现但 Delivery 里 `check_suite` 是绿的，多半是 Checks 权限没生效（回 §0）
   或 `check_run.app.id` 过滤把自己也滤掉了。

---

## 7. 安全须知

- Webhook secret 与私钥都等价于凭据：不进 git，不进日志，不回显。
- **签名校验先于一切**：在验签通过之前不要解析 body、不要按 payload 里的仓库名去碰文件系统
  （payload 完全由请求方控制，仓库名要当作不可信输入做白名单校验）。
- 用 `X-GitHub-Delivery` 做幂等去重：GitHub 在超时后会**重投**，没有去重就会重复建 run。
- installation token 有效期 1 小时，按 installation 缓存并提前刷新；不要落盘。
