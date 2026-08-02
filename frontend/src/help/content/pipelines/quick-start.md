---
title: 流水线快速上手
group: 流水线
order: 10
description: 十分钟跑通第一条流水线。
---

## 创建 .wuling-ci.yml

在仓库根目录新建 `.wuling-ci.yml`：

```yaml
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
        run: git status

      - name: Hello
        run: echo "Hello from Wuling CI"
```

提交并推送到远程仓库。

## 查看运行结果

1. 打开仓库 **流水线** 页面。
2. 点击最新运行查看各 job / step 日志。
3. 失败步骤会标红并显示退出码与输出。

## 使用内置 Action

武陵 DevOps 提供 `setup-node`、`setup-rust` 等内置 action，可缓存依赖：

```yaml
steps:
  - uses: setup-node
    with:
      node-version: "22"
  - run: npm ci && npm test
```

## 矩阵构建

使用 `strategy.matrix` 在多版本 / 多平台上并行测试：

```yaml
strategy:
  matrix:
    node: ["20", "22"]
steps:
  - uses: setup-node
    with:
      node-version: ${{ matrix.node }}
```

## 下一步

- 阅读 [流水线概览](/help/pipelines/overview) 了解触发与 job 模型
- 配置 [自托管 Runner](/help/runners) 在私有网络执行作业
