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
| **Checks** | Read and write | 接收 `check_run`；武陵还需要创建和更新自己的 Check Run |

如果 App 只做状态监听，Checks Read-only 已足够读取检查；当前武陵 App 还会把“武陵 CI”回显到
GitHub，因此实际部署应使用 **Read and write**。

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

通知投递服务尚未上线。当前事件会保存在 `notification_outbox`，等待后续 worker 投递站内信、
移动推送或邮件；这不会影响状态变为 Green/Red。

## 验证配置

1. 在 GitHub App **Advanced → Recent Deliveries** 中确认 `workflow_run` / `check_run` 返回 200。
2. 完成一次 GitHub Actions workflow，控制面日志应出现 `completed check recorded`。
3. 数据库 `github_check_states` 应出现 workflow/job 状态；成功结果的 `color` 应为 `green`。
4. `notification_outbox` 应出现 `event_type=github.check.completed` 且 `delivered_at` 为空的记录。

如果 Recent Deliveries 没有对应事件，先检查事件订阅和新权限是否已接受；如果 Delivery 是 200
但没有状态记录，检查 GitHub 仓库是否已经绑定到正确的武陵仓库。
