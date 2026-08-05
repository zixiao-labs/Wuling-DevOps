---
title: 流水线快速上手
group: 流水线
order: 10
description: 十分钟跑通第一条流水线。
---

## 创建 `.wuling/workflows/ci.yml`

在仓库中新建 `.wuling/workflows/ci.yml`：

```yaml
# yaml-language-server: $schema=https://<wuling-origin>/.well-known/wuling/schemas/v1/workflow.json
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  build:
    runs-on: [linux]
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up Node.js
        uses: actions/setup-node@v4
        with:
          node-version: "22"
          cache: npm

      - name: Test
        run: npm ci && npm test
```

将 `<wuling-origin>` 替换为当前武陵 DevOps 站点的实际 origin。提交并推送到远程仓库。

## 查看运行结果

1. 打开仓库 **流水线** 页面。
2. 点击最新运行查看各 job / step 日志。
3. 失败步骤会标红并显示退出码与输出。

## 使用内置 Action

武陵 DevOps 支持 GitHub Actions 风格的内置 action。使用完整 action 名称和版本引用：

```yaml
steps:
  - uses: actions/checkout@v4

  - uses: actions/setup-node@v4
    with:
      node-version: "22"
      cache: npm

  - run: npm ci && npm test
```

`actions/setup-node@v4` 的 `cache` 可选值为 `npm`、`pnpm` 或 `yarn`。`actions/setup-rust` 用于配置 Rust 工具链；它的 `cache` input 当前不会启用 Rust 依赖缓存，请不要将其作为缓存保证。

## 矩阵构建

使用 `strategy.matrix` 在多版本 / 多平台上并行测试：

```yaml
strategy:
  matrix:
    node: ["20", "22"]
steps:
  - uses: actions/checkout@v4
  - uses: actions/setup-node@v4
    with:
      node-version: ${{ matrix.node }}
```

## 下一步

- 阅读 [流水线概览](/help/pipelines/overview) 了解触发与 job 模型
- 配置 [自托管 Runner](/help/runners) 在私有网络执行作业
