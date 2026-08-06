# Pipelines 远端 CI 缓存：OSS、S3 与 Cloudflare R2

本平台当前内置的 `actions/cache` 是 Runner 本地的 tar 缓存：它适合复用同一台
Runner 的工作目录，但**不是** OSS、S3 或 R2 后端。弹性 Runner、独占任务和
`execution.mode: isolated` 都会使用新的 VM；它们不会可靠地命中本地缓存。

制品（Artifact）与缓存也不是同一个概念。Artifact Service 的不可变 Blob 存储、
权限模型和保留策略见 [artifacts.md](artifacts.md)；本页的缓存 bucket 应独立创建，
可被覆盖，并应设短生命周期。

## 推荐的 key 和对象布局

缓存 key 至少包含：

1. 项目或仓库前缀，避免不同项目读取彼此的依赖；
2. OS、CPU 架构、语言/工具链版本；
3. 依赖锁文件的 SHA-256；
4. 可选的 ABI、编译 profile 或 feature 集。

例如 Rust 依赖缓存的逻辑 key：

```text
ci-cache/<org>/<project>/rust/<os>/<arch>/<toolchain>/<Cargo.lock.sha256>
```

不要使用分支名、提交 SHA 或随机 UUID 作为每次写入的唯一对象名；这样会将缓存变成
没有回收机制的制品仓库。读取时只接受同一项目 namespace 下的完整 key，下载到临时
目录后再原子替换目标目录。上传前清理 `target/` 中的二进制、测试输出和包含 token 的
配置文件。

## 最小权限和并发

为 CI 单独创建访问凭据或工作负载身份，只允许指定 bucket/prefix 的：

- `GetObject`、`PutObject`、`HeadObject`；
- 必要时的受限 `DeleteObject`（仅 lifecycle 无法满足时）。

拒绝 `ListBucket`、跨项目 prefix、bucket 策略编辑和账号级权限。缓存上传应使用临时
对象（如 `<key>.partial.<run-id>`）并在校验 SHA-256 后再提升为稳定 key；读取方发现
partial 对象必须忽略。不要把长效 Access Key 写入工作流 YAML，应使用组织 Secret、
短效 STS/AssumeRole 或云工作负载身份。

以下是适用于 shell 步骤的抽象流程；将 `cache_fetch` / `cache_store` 替换为团队选定
的 `aws s3 cp`、`ossutil`、`rclone` 或 SDK 封装：

```yaml
steps:
  - uses: actions/checkout@v4
  - name: Restore dependency cache
    run: |
      os="$(uname -s | tr '[:upper:]' '[:lower:]')"
      arch="$(uname -m)"
      # Prefer a pinned channel from rust-toolchain.toml; fall back to rustc -Vv.
      toolchain="$(sed -n 's/^channel[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' rust-toolchain.toml 2>/dev/null | head -n1)"
      if [ -z "$toolchain" ]; then
        toolchain="$(rustc -Vv | sed -n 's/^release: //p')"
      fi
      key="ci-cache/${os}/${arch}/rust/${toolchain}/$(sha256sum Cargo.lock | cut -d ' ' -f1)"
      mkdir -p .ci-cache
      tmp="$(mktemp -d .ci-cache/download.XXXXXX)"
      if cache_fetch "$key.tar.zst" "$tmp/archive.tar.zst"; then
        # Reject absolute paths, parent traversal, and unsafe symlinks before extract.
        if tar --zstd -tvf "$tmp/archive.tar.zst" | awk '
          {
            # GNU tar -tv lines: permissions ... name [-> link-target]
            name=$0
            sub(/^[^ ]+ +[^ ]+ +[^ ]+ +[^ ]+ +[^ ]+ +[^ ]+ +[^ ]+ +/, "", name)
            target=""
            if (name ~ / -> /) {
              split(name, parts, / -> /)
              name=parts[1]
              target=parts[2]
            }
            if (name ~ /^\// || name ~ /(^|\/)\.\.(\/|$)/) exit 1
            if (name !~ /^(registry\/|git\/|target\/)/) exit 1
            if (target != "" && (target ~ /^\// || target ~ /(^|\/)\.\.(\/|$)/)) exit 1
          }
        '; then
          mkdir -p "$tmp/extract"
          tar --zstd -xf "$tmp/archive.tar.zst" -C "$tmp/extract"
          # Atomically replace only the Cargo and target cache trees we restore.
          if [ -d "$tmp/extract/registry" ]; then
            rm -rf "$CARGO_HOME/registry.new"
            mv "$tmp/extract/registry" "$CARGO_HOME/registry.new"
            rm -rf "$CARGO_HOME/registry"
            mv "$CARGO_HOME/registry.new" "$CARGO_HOME/registry"
          fi
          if [ -d "$tmp/extract/git" ]; then
            rm -rf "$CARGO_HOME/git.new"
            mv "$tmp/extract/git" "$CARGO_HOME/git.new"
            rm -rf "$CARGO_HOME/git"
            mv "$CARGO_HOME/git.new" "$CARGO_HOME/git"
          fi
          if [ -d "$tmp/extract/target" ]; then
            rm -rf target.new
            mv "$tmp/extract/target" target.new
            rm -rf target
            mv target.new target
          fi
        fi
      fi
      rm -rf "$tmp"
  - name: Build
    run: cargo build --locked
  - name: Save dependency cache
    run: |
      os="$(uname -s | tr '[:upper:]' '[:lower:]')"
      arch="$(uname -m)"
      toolchain="$(sed -n 's/^channel[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' rust-toolchain.toml 2>/dev/null | head -n1)"
      if [ -z "$toolchain" ]; then
        toolchain="$(rustc -Vv | sed -n 's/^release: //p')"
      fi
      key="ci-cache/${os}/${arch}/rust/${toolchain}/$(sha256sum Cargo.lock | cut -d ' ' -f1)"
      mkdir -p .ci-cache
      paths=()
      # Pack only trees that exist so a missing $CARGO_HOME/git does not fail tar.
      [ -d "$CARGO_HOME/registry" ] && paths+=(-C "$CARGO_HOME" registry)
      [ -d "$CARGO_HOME/git" ] && paths+=(-C "$CARGO_HOME" git)
      # target/ restore/pack stays independent of the Cargo home caches.
      [ -d target/debug/.fingerprint ] && paths+=(target/debug/.fingerprint)
      if [ "${#paths[@]}" -gt 0 ]; then
        tar --zstd -cf .ci-cache/upload.tar.zst "${paths[@]}" || true
        cache_store_atomic "$key.tar.zst" .ci-cache/upload.tar.zst || true
      fi
```

每个 `run:` 都是独立 shell，所以保存步骤会重新计算 key；不要依赖前一个步骤的 shell
变量。命令中的路径仅作示例。Windows Runner 请使用 PowerShell 的 `Get-FileHash`、`tar.exe`
和 Windows 兼容的对象存储客户端；不要将 Linux 路径的缓存直接恢复到 Windows。

## Provider 选择与网络

### 阿里云 OSS

- ECS 与 bucket 同地域时优先使用对应的 OSS 内网 endpoint；不要从同地域 VM 强制走
  公网 endpoint，否则延迟和流量账单都会更差。
- bucket 开启版本控制时，覆盖缓存 key 会生成非当前版本；为旧版本配置单独的
  lifecycle，否则“缓存已过期”不代表数据已删除。
- 计算 lifecycle 时将 PUT/Copy、LIST、生命周期转储和小对象请求计入预算。频繁上传
  小 tar 包的请求成本常高于存储成本；可合并目录、限制一天写入次数并使用短 TTL。

### AWS S3

- 选择 Runner 所在区域的 bucket 和 VPC endpoint（Gateway/Interface 的取舍取决于
  网络拓扑），并在 bucket policy 中限制 `aws:SourceVpce` 或工作负载角色。
- Standard-IA、One Zone-IA 和归档类有最短保存期与提前删除费用；短 TTL 的 CI 缓存
  通常应先留在 Standard，除非访问和保存时间确实符合目标存储类。
- PUT、GET、LIST、KMS、跨区域复制、NAT/公网 egress 都是独立成本维度。不要在每个
  job 中 LIST 整个 bucket，也不要跨区域恢复缓存。

### Cloudflare R2

- R2 使用 S3 兼容 endpoint，但区域通常填 `auto`；请以账户 dashboard 和当前文档为
  准，不要把 AWS region 照搬过去。
- 预算要同时看存储量、Class A（写入/列举）与 Class B（读取）操作。将每个文件单独
  上传会快速放大请求数量，应打包并压缩后上传。
- 若选择 Infrequent Access，确认生命周期大于其最短保存期（当前产品条款通常为
  30 天，价格/条款可能更新）；对几小时或几天的 CI 缓存不要迁移到它。

## 生命周期、配额与可观测性

建议为每个缓存 prefix 设置：

- 7–30 天到期删除（依赖缓存通常 7–14 天足够）；
- 单对象大小上限、每项目容量上限和每日写入预算；
- 分段指标：命中率、下载字节、上传字节、对象数、失败率、过期删除数；
- 预算告警和异常写入告警（例如对象数一天增长超过基线）。

缓存永远是可丢失的性能优化：恢复失败必须继续构建；上传失败不得让正确的构建结果
变成失败。不要缓存 `.env`、私钥、云凭据、完整工作目录或未审计的用户上传文件。
