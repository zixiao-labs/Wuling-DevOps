---
title: Rust 与 Cargo Xtask
group: Pipelines
order: 33
description: 固定 Rust 工具链，并在 Linux 或 Windows Runner 上运行可重复的 Cargo Xtask CI。
---

# Rust 与 Cargo Xtask

将 `rust-toolchain.toml`、`.cargo/config.toml` 和 `xtask` crate 一起提交到仓库。
Runner 的 `actions/setup-rust` 会读取该工具链文件并为后续步骤设置 `RUSTUP_HOME`、
`CARGO_HOME` 和 Cargo 路径。

```toml
# rust-toolchain.toml
[toolchain]
channel = "1.85.0"
profile = "minimal"
components = ["clippy", "rustfmt"]
```

```toml
# .cargo/config.toml
[alias]
xtask = "run --package xtask --"
```

## Linux

```yaml
jobs:
  check:
    runs-on: [linux, docker]
    execution:
      mode: exclusive
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-rust@v1
        with:
          toolchain: file
      - run: cargo xtask ci
```

`exclusive` 会让任务独占一个已有 Runner，适合不能与其它任务共用 Docker daemon 或工具
状态的构建；它不会创建新 VM。

## Windows 与强隔离

Windows 未设置 `container:` 时使用宿主 PowerShell（优先 `pwsh`）；使用 Windows 容器时
必须保证宿主和容器的 Windows Server 版本兼容。对不可信或强隔离的构建，指定临时 pool：

```yaml
jobs:
  test:
    runs-on: [windows]
    execution:
      mode: isolated
      pool: aws-windows
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-rust@v1
        with:
          toolchain: file
      - run: cargo xtask ci
```

`isolated` 直接新建该 pool 的 VM，完成/取消/超时后销毁，不是嵌套虚拟化。它不复用本地
工作目录、工具目录或缓存，启动较慢且会产生实例费用；需要加速时使用隔离的远端缓存。

更多 Linux/Windows 的完整示例、交叉构建边界和 `--locked` 约束见仓库
[`docs/pipelines-rust-xtask.md`](https://github.com/zixiao-labs/wuling-devops/blob/main/docs/pipelines-rust-xtask.md)。
