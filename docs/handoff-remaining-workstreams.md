# 剩余工作流交接

本文档记录 Stack #45 上**尚未交付**的工作流，供后续 agent 接续，避免重复阅读设计与 stack 纪律。

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
| `feat/aliyun-resource-limits` | Aliyun RunInstances + 容器资源限制（待 submit） |
| `feat/setup-actions` | setup-node / setup-rust + toolcache（待 submit） |
| `feat/gitops-runner-config` | GET/PUT org runner-config（待 submit） |
| `feat/help-center-ssr` | `/help` SSR 帮助中心（待 submit） |
| `feat/autoscale-ui` | Org runner-config YAML 编辑页（待 submit） |

（以 `gh stack view` 为准。）

---

## 工作流 4：Autoscale UI

**状态：** **已交付（本分支 `feat/autoscale-ui`）** — Org 侧栏「自动扩缩容」页：YAML textarea + GET/PUT + `base_blob_sha` 冲突提示 + parse_error/warnings + maintainer+ 写权限门控。

**页面：** `frontend/src/pages/orgs/[org_slug]/runner-config.tsx`

---

## 工作流 5：Runner 镜像与 Inno Setup

**状态：** Runner 客户端与 setup action 已有基础；**多 OS 安装包、Windows Inno Setup 安装器、发布流水线**未完整交付。

**设计参考：**

- 仓库内 `feat/multi-os-runners` 分支（若仍存在）及相关 runner 文档
- release.yml 中 runner 制品模式（`build-runner` job）

**待做：**

- 统一 Linux/macOS/Windows runner 构建矩阵
- Windows：Inno Setup `.iss` + CI 产出安装包
- 文档：安装、注册、升级路径
- GH Release 附件与 checksum

**建议分支名：** `feat/runner-installers`（叠在 `feat/autoscale-ui` 或 submit 后的栈顶之上）

---

## 工作流 7：Webhook 实现

**状态：** `docs/github-app-integration` / runbook 已有文档；**GitHub App webhook 接收、验签、事件分发与 PR check 联动**需完整实现或硬化。

**设计参考：**

- 仓库 `docs/` 下 GitHub App 集成说明
- API 侧 webhook handler 占位（搜索 `webhook` / `github app`）

**待做：**

- POST webhook endpoint + HMAC 验签
- 处理 `push`、`pull_request`、`check_run` 等事件 → 触发流水线 / 同步仓库
- Idempotency、重试、可观测性（结构化日志）
- 与 GitOps 仓库同步路径对齐

**建议分支名：** `feat/github-webhooks`

---

## 相关设计文件索引

```
designs/
  design-aliyun.json          # 阿里云 autoscale + 资源限制
  design-gitops-write.json    # runner-config GitOps
  design-help-center-ssr.json # 帮助中心 SSR（已实施）
  design-setup-actions.json   # setup-node/rust
  design-matrix.json          # strategy.matrix
  design-logo.json            # 品牌资产
```

## 验证清单（接续 agent）

1. `gh stack view` 确认当前栈顶与 base branch
2. 阅读对应 `design-*.json` 的 `currentState` + `design` + `risks`
3. 新分支：`gh stack add feat/...`
4. PR 前：`npm run typecheck` / 对应语言测试 / CI green
5. **不要代跑** `gh stack submit`；就绪后通知操作者执行

---

*最后更新：feat/autoscale-ui 交付时。*
