---
title: 流水线概览
group: 流水线
order: 5
description: 武陵 DevOps 流水线模型与核心概念。
---

## 配置文件

流水线由仓库中的 **`.wuling/workflows/*.yml`**（也支持 `.yaml`）定义，语法类似 GitHub Actions，包含：

- **workflow** — 名称与触发条件（push、pull_request、manual 等）
- **jobs** — 并行或串行执行的任务单元
- **steps** — 每个 job 内的具体步骤（shell 命令或内置 action）

## 触发方式

| 事件 | 说明 |
|------|------|
| `push` | 推送到指定分支 |
| `pull_request` | 打开或更新 MR |
| `workflow_dispatch` | 手动触发 |

## 执行环境

每个 job 通过 `runs-on` 指定 Runner 标签。步骤在 Runner 的工作目录中执行，可访问仓库 checkout 与缓存目录。

## 跨仓库 YAML 补全

本项目的 `.vscode/settings.json` 已将工作流路径关联到 schema，并推荐安装 Red Hat YAML 扩展。对于任意业务仓库，可把下列 modeline 放到工作流首行，让 YAML Language Server 直接从当前武陵 DevOps 站点加载 schema：

```yaml
# yaml-language-server: $schema=https://<wuling-origin>/.well-known/wuling/schemas/v1/workflow.json
```

将 `<wuling-origin>` 替换为实际站点 origin。该 schema 提供 v1 工作流字段、内置 action 输入，以及 `execution.mode`（`shared`、`exclusive`、`isolated`）和隔离作业的 `execution.pool` 的补全与结构提示。

不想在每个文件写 modeline 时，也可在业务仓库创建 `.vscode/settings.json`：

```json
{
  "yaml.schemas": {
    "https://<wuling-origin>/.well-known/wuling/schemas/v1/workflow.json": [
      ".wuling/workflows/*.yml",
      ".wuling/workflows/*.yaml"
    ],
    "https://<wuling-origin>/.well-known/wuling/schemas/v1/runner-config.json": "runner-config.yaml"
  }
}
```

此方式同样要求把 `<wuling-origin>` 替换为实际站点；安装 Red Hat YAML 扩展后重新打开 YAML
文件即可生效。modeline 更适合单个文件，workspace 设置适合整个仓库。

Language Server 只检查静态结构；服务端解析仍是权威来源，并继续校验矩阵展开、`needs` 依赖、Runner 容量与标签、action 运行时支持及其它运行语义。

## 相关文档

- [流水线快速上手](/help/pipelines/quick-start) — 十分钟跑通第一条流水线
