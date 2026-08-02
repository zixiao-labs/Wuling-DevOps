---
title: 流水线概览
group: 流水线
order: 5
description: 武陵 DevOps 流水线模型与核心概念。
---

## 配置文件

流水线由仓库根目录的 **`.wuling-ci.yml`** 定义，语法类似 GitHub Actions，包含：

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

## 相关文档

- [流水线快速上手](/help/pipelines/quick-start) — 十分钟跑通第一条流水线
