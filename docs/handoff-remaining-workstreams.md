# 剩余工作流交接

本文档记录 Stack #45 上**尚未交付**的工作流，供后续 agent 接续，避免重复阅读设计与 stack 纪律。

## Stack 纪律（Graphite Stack #45）

- 使用 **Graphite** 管理 stacked PR：`gt stack add` 将新分支叠在当前栈顶；提交前 **`gt stack stop`** 停止自动 restack（若正在运行）。
- 每个 PR 对应一个 focused branch；合并顺序从栈底到栈顶。
- 设计文档路径：`~/.claude/projects/-Users-logos-WebstormProjects-Wuling-DevOps/designs/`

## 已交付栈分支（参考）

| 分支 | 领域 |
|------|------|
| `feat/pipeline-strategy-matrix` | 流水线 matrix 策略 |
| `feat/setup-actions` | setup-node / setup-rust 内置 action |
| `feat/gitops-runner-config` | GET/PUT org runner-config GitOps 写回 |
| `feat/aliyun-resource-limits` | 阿里云 RunInstances + 作业资源限制 |
| `feat/help-center-ssr` | `/help` SSR 帮助中心（本 PR） |
| `User-experience-optimization` / `opt-ux` | 品牌资产、Service Worker 修复等 UX |

（具体栈顺序以 `gt log` / `gh pr list` 为准。）

---

## 工作流 4：Autoscale UI

**状态：** 后端/API 面已在 `feat/aliyun-resource-limits` 等分支落地；**前端 autoscale 配置 UI 未做**。

**设计参考：**

- `~/.claude/projects/-Users-logos-WebstormProjects-Wuling-DevOps/designs/design-aliyun.json`
- `critique-aliyun.json`（实现时注意 critique 中的约束）

**待做：**

- 组织/项目设置页：展示与编辑 autoscale provider、实例规格、容量上下限
- 与现有 GitOps runner-config API 联动（`feat/gitops-runner-config`）
- 表单校验、权限（org admin）、空状态与错误提示

**建议分支名：** `feat/autoscale-ui`

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

**建议分支名：** `feat/runner-installers`

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

1. `gt log` 确认当前栈顶与 base branch
2. 阅读对应 `design-*.json` 的 `currentState` + `design` + `risks`
3. 新分支：`gt create -a feat/...` 或 `git checkout -b feat/...`
4. PR 前：`npm run typecheck` / 对应语言测试 / CI green
5. **`gt stack stop`** 后再 `gt submit`（或 `gh pr create`），避免 restack 冲突

---

*最后更新：feat/help-center-ssr 交付时创建。*
