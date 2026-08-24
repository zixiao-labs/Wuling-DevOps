---
title: GitHub Actions 与检查状态监听
group: 集成
order: 10
description: 配置 GitHub App 监听 Actions workflow 与第三方 Check，并了解默认采集范围和通知行为。
---

## 默认行为

不需要在 GitHub Actions workflow、`.wuling/workflows/*.yml` 或武陵部署环境中另外声明检查名称。
完成 GitHub App 和仓库绑定后，武陵默认采集符合以下条件的全部终态事件：

| 事件 | 粒度 | 采集条件 |
|------|------|----------|
| `workflow_run` | 一次 GitHub Actions workflow run | `action=completed` 且 `status=completed` |
| `check_run` | 一个 Actions job、武陵检查或第三方 App 检查 | `action=completed` 且 `status=completed` |

当前没有按 workflow 名称、分支、provider 或检查名称配置 allowlist / denylist。一次 GitHub Actions
执行可能同时产生一条 workflow 级状态和多条 job 级 check 状态；武陵会分别保存它们。

每条监听到的终态还会回显为由武陵 GitHub App 创建的独立 Check Run。名称保持稳定，方便在
GitHub 分支保护或 Rulesets 的 **Require status checks to pass** 中选择：

| 来源 | 回显名称 |
|------|----------|
| GitHub Actions workflow | `武陵监听 / GitHub Actions / <workflow 名称>` |
| GitHub Actions job | `武陵监听 / GitHub Actions / <job 名称>` |
| 第三方 App | `武陵监听 / <provider> / <check 名称>` |

回显沿用原检查的 commit SHA、终态和详情链接。Webhook 重投或同一 workflow run 重跑时会更新已
记录的回显 Check Run；武陵会识别并忽略自己创建的监听回显，避免递归回显和重复通知。原 provider
创建的 Check Run 始终只读，不会被武陵修改。

“监听全部”并不是监听 GitHub 上的所有仓库。事件还必须同时满足三个范围限制：

1. 仓库包含在 GitHub App 当前安装所允许访问的仓库范围内。
2. GitHub App 已订阅对应 Webhook 事件。
3. GitHub 仓库已经绑定到一个武陵仓库；未绑定仓库的事件会被安全忽略。

## 1. 配置 GitHub App 权限

进入 GitHub App 的 **Settings → Permissions & events → Repository permissions**：

| 权限 | 级别 | 用途 |
|------|------|------|
| **Metadata** | Read-only | GitHub App 必需的仓库元数据权限 |
| **Actions** | Read-only | 接收 `workflow_run` 并读取 Actions workflow 状态 |
| **Checks** | Read and write | 接收 `check_run`，并为监听到的 Actions/第三方终态创建或更新回显 Check Run |

当前监听功能包含 Check Run 回显，因此实际部署必须使用 **Read and write**。只有禁用所有 Checks
回显、完全只读采集的定制部署，才可以降为 Read-only。

修改已安装 App 的权限后，组织管理员必须在
**Organization Settings → GitHub Apps → Wuling DevOps → Review request** 接受新权限。
在接受前，旧 installation token 不会获得 Actions 权限。

## 2. 订阅 Webhook 事件

在同一 GitHub App 设置页的 **Subscribe to events** 中至少勾选：

- **Workflow run** — 接收 GitHub Actions workflow 的 `completed` 事件。
- **Check run** — 接收 GitHub Actions job 和其他 Checks provider 的完成事件。

Webhook 区域还需要启用 **Active**，并填写：

| 字段 | 示例 |
|------|------|
| Webhook URL | `https://wuling.example.com/api/v1/webhooks/github` |
| Webhook secret | 使用 `openssl rand -hex 32` 生成的高熵随机值 |
| SSL verification | Enable |

## 3. 配置武陵控制面

控制面至少需要配置 Webhook secret；完整的仓库同步、PR 触发和武陵 Checks 回显还需要 App ID
与私钥：

```env
WULING_GITHUB_APP_ID=3713023
WULING_GITHUB_WEBHOOK_SECRET=<与 GitHub App 设置一致的 secret>
WULING_GITHUB_APP_PRIVATE_KEY_PATH=/run/secrets/wuling-github-app.pem
```

也可以使用 `WULING_GITHUB_APP_PRIVATE_KEY` 直接传入 PEM 全文。不要同时把私钥写入仓库、镜像
或普通日志。`WULING_GITHUB_WEBHOOK_SECRET` 为空时，控制面不会挂载 GitHub Webhook 路由，
也就不会监听任何检查。

## 4. 绑定仓库

以目标组织的 maintainer 身份调用仓库绑定 API：

```http
PUT /api/v1/orgs/{org}/projects/{project}/repos/{repo}/github-link
Content-Type: application/json
Authorization: Bearer <token>

{
  "owner": "acme",
  "name": "app",
  "installation_id": 12345678
}
```

`installation_id` 可以在 GitHub App 安装设置页 URL 或 installation Webhook payload 中找到。
绑定完成后不需要重启控制面，也不需要修改业务仓库的 workflow 文件。

## 状态与通知语义

- GitHub `conclusion=success` 映射为武陵 `color=green`。
- `failure`、`cancelled`、`timed_out`、`neutral`、`skipped` 及未知终态映射为 `color=red`，
  避免非成功结果被误报为通过。
- 同一检查重跑时，以较高 `attempt` 为新；同一 attempt 内以较晚 `completed_at` 为新。
  迟到的旧 Webhook 不会覆盖最新结果。
- 状态与 `github.check.completed` 通知事件在同一数据库事务中写入，Webhook 重投不会产生重复事件。
- GitHub 不认识的 provider 终态在回显时按 `failure` 处理，避免分支保护被未知结果意外放行。

通知投递服务尚未上线。当前事件会保存在 `notification_outbox`，等待后续 worker 投递站内信、
移动推送或邮件；这不会影响状态变为 Green/Red。

## 验证配置

1. 在 GitHub App **Advanced → Recent Deliveries** 中确认 `workflow_run` / `check_run` 返回 200。
2. 完成一次 GitHub Actions workflow，控制面日志应出现 `completed check recorded`。
3. 数据库 `github_check_states` 应出现 workflow/job 状态；成功结果的 `color` 应为 `green`。
4. `notification_outbox` 应出现 `event_type=github.check.completed` 且 `delivered_at` 为空的记录。
5. 对应 commit 或 PR 的 Checks 页应出现 `武陵监听 / ...`；`github_check_states.feedback_check_run_id`
   应保存它的 ID。先让目标检查至少运行一次，再到仓库 **Settings → Branches** 或 **Rules →
   Rulesets**，把这个稳定名称选为 required status check。

如果 Recent Deliveries 没有对应事件，先检查事件订阅和新权限是否已接受；如果 Delivery 是 200
但没有状态记录，检查 GitHub 仓库是否已经绑定到正确的武陵仓库。已有状态但没有回显时，优先检查
Checks 权限是否为 Read and write，以及 installation 是否已经接受新增权限。
