# Pipelines 中的 Rust 工具链与 Cargo Xtask

Runner 内置 `actions/setup-rust`，可读取仓库根目录的 `rust-toolchain.toml`，或通过
`with.toolchain` 固定版本。将工具链和 CI 编排一并提交到仓库，避免依赖某台长期运行
Runner 的预装环境。

## 固定工具链

在仓库根目录创建 `rust-toolchain.toml`：

```toml
[toolchain]
channel = "1.85.0"
profile = "minimal"
components = ["clippy", "rustfmt"]
targets = ["x86_64-unknown-linux-gnu"]
```

版本可以是固定 release、`stable` 或团队批准的 nightly 日期。发布/安全敏感 CI 建议
固定 release；只固定 `stable` 会让未来工具链变更影响历史构建。

推荐将自动化命令放到 workspace 中的 `xtask` crate，并提供短 alias：

```toml
# .cargo/config.toml
[alias]
xtask = "run --package xtask --"
```

`cargo xtask ci` 应在本机和 CI 做同一组可重复步骤，例如 fmt、clippy、test、代码生成
校验。不要让 xtask 读取未声明的本机路径、交互式提示或长效凭据。

## Linux workflow

工作流位于 `.wuling/workflows/*.yml`。下面使用 `exclusive`：它会让该任务独占一台
已注册 Runner，适合会修改共享工具链状态、Docker daemon 或设备的构建；它并不新建 VM。

```yaml
name: Rust CI
on: [push, pull_request]

jobs:
  check:
    runs-on: [linux, docker]
    resource: medium
    execution:
      mode: exclusive
    container: rust:1.85-bookworm
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-rust@v1
        with:
          toolchain: file
      - run: cargo xtask ci
```

`actions/setup-rust` 会将 `RUSTUP_HOME`、`CARGO_HOME` 和 Cargo bin 目录写入后续步骤
环境。若不使用 `rust-toolchain.toml`，可将 `toolchain` 设为固定 channel，例如
`1.85.0`。`actions/setup-rust` 本身不提供远端依赖缓存；缓存策略见
[pipelines-cache.md](pipelines-cache.md)。

## Windows workflow

Windows Runner 未设置 `container:` 时在宿主机的 PowerShell（优先 `pwsh`，否则
Windows PowerShell）执行；路径、引号、环境变量和 shell 语法应保持 PowerShell 兼容。
如果使用 Windows 容器，宿主与容器的 Windows Server 版本必须兼容，且镜像必须包含任务
需要的 shell/工具。

`isolated` 会为每个任务新建指定 pool 的临时 Runner VM，任务结束、失败、取消或超时后
立即回收。它必须显式指定 `pool`，且不会复用工作目录、工具目录或本地缓存：

```yaml
name: Rust Windows CI
on:
  workflow_dispatch:

jobs:
  test:
    runs-on: [windows]
    resource: medium
    execution:
      mode: isolated
      pool: aws-windows
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-rust@v1
        with:
          toolchain: file
      - name: Test
        run: |
          cargo xtask ci
```

隔离不是嵌套虚拟化：控制面直接创建新的 ECS/EC2 VM，再在任务结束后销毁。该模式会增加
启动时间和实例费用；只有确实需要强边界或不可信构建时才使用。需要加速时，使用带 OS /
架构 / 锁文件 hash 的远端缓存，不能依赖上一台 VM 的磁盘。

## Linux 与 Windows 的交叉构建边界

- Linux 容器执行 Linux target；Windows 宿主执行 MSVC target。不要假设一种环境可直接
  产出另一种平台的原生二进制。
- 若 xtask 调用 PowerShell，使用 `$env:NAME` 和 `Join-Path`；若调用 sh，使用
  `$NAME` 和 POSIX 路径。把 OS 分支放在不同 job 或 xtask 内明确处理。
- Windows 上禁止在 `C:\Users` 中依赖初始化时才存在的用户 profile；autoscaled 镜像的
  Runner 配置、工具和状态应由数据盘初始化逻辑提供。
- `cargo fmt --check`、`cargo clippy -- -D warnings`、`cargo test --locked` 应使用
  `--locked`，以便锁文件 hash 真正代表依赖图。
