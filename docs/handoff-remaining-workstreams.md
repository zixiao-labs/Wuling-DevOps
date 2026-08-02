# 剩余工作流交接

本文档记录 Stack #45 上**尚未交付 / 已交付**的工作流，供后续 agent 接续。

## Stack 纪律（`gh stack` Stack #45）

- 使用 **`gh stack`**（见 `gh stack --help`）管理 stacked PR。
- 新分支叠在栈顶：`gh stack add <branch>`；本地改完后 **不要代跑** `gh stack submit`，就绪时停下通知操作者执行。
- 每个 PR 对应一个 focused branch；合并顺序从栈底（靠近 `main`）到栈顶。
- 同步远端：`gh stack sync` / `gh stack rebase` 按需使用。
- 设计文档路径：`~/.claude/projects/-Users-logos-WebstormProjects-Wuling-DevOps/designs/`

## 已交付栈分支（bottom → top）

| 分支 | 领域 |
|------|------|
| `User-experience-optimization` | logo / SW / brand CI guard（#41） |
| `feat/aliyun-userdata-provider-aware` | Aliyun Windows user-data（#42） |
| `feat/pipeline-strategy-matrix` | strategy.matrix（#43） |
| `docs/github-app-integration` | GitHub App operator runbook（#44） |
| `feat/aliyun-resource-limits` | Aliyun RunInstances + 容器资源限制 |
| `feat/setup-actions` | setup-node / setup-rust + toolcache |
| `feat/gitops-runner-config` | GET/PUT org runner-config |
| `feat/help-center-ssr` | `/help` SSR 帮助中心 |
| `feat/autoscale-ui` | Org runner-config YAML 编辑页 |
| `feat/github-webhooks` | Webhook MVP：HMAC + ping + delivery 幂等 |
| `feat/github-webhooks-events` | repo links + fetch + PR trigger + Checks |
| `feat/runner-installers` | Windows Inno Setup + release 附件 + 安装文档 |
| `fix/stack-bugbot-findings` | Bugbot：release help prerender、push 无 App 不 500、Checks 文档对齐 |

（以 `gh stack view` 为准。）

---

## 工作流 4：Autoscale UI

**状态：** 已交付（`feat/autoscale-ui`）。

---

## 工作流 5：Runner 镜像与 Inno Setup

**状态：** 已交付（`feat/runner-installers`）。

- Inno Setup：`runners/packaging/windows/wuling-runner.iss` + `run.cmd`
- CI：`release.yml` Windows 矩阵编译 setup.exe + sha256，Release 附件含 `*.exe`
- 文档：`docs/RELEASE.md`、`runners/runner-clients/README.md`

镜像 bake 脚本（`runners/images/*/setup.*`）保持 zip/tar 路径，与 GUI 安装器并存。

---

## 工作流 7：Webhook 实现

**状态：** 已交付（`feat/github-webhooks` + `feat/github-webhooks-events`）。

- MVP：`POST /api/v1/webhooks/github`，`X-Hub-Signature-256`，`ping`，`github_webhook_deliveries`
- Events：`github_repo_links`、installation token、`git fetch`、push/PR → pipelinetrigger、check_suite/check_run
- 绑定 API：`PUT .../repos/{repo}/github-link`（maintainer+）

后续可增强：check run `external_id` ↔ pipeline run 终态自动 PATCH、annotations 分批、仓库改名自动改 link。

---

## 相关设计文件索引

```
designs/
  design-aliyun.json
  design-gitops-write.json
  design-help-center-ssr.json
  design-setup-actions.json
  design-matrix.json
  design-logo.json
```

## 验证清单（接续 agent）

1. `gh stack view` 确认当前栈顶与 base branch
2. 阅读对应 `design-*.json` 的 `currentState` + `design` + `risks`
3. 新分支：`gh stack add feat/...`
4. PR 前：`npm run typecheck` / 对应语言测试 / CI green
5. **不要代跑** `gh stack submit`；就绪后通知操作者执行

---

*最后更新：feat/runner-installers 交付时。*
