# Pipelines 跨 Runner 缓存

Wuling 内置的 `actions/cache` 通过控制面 API 与私有 Artifact Service 保存缓存，static、
ephemeral 以及 `execution.mode: isolated` Runner 都能复用同一仓库的结果。用户无需在
workflow 中配置 OSS/S3 凭据，Runner 也不会直接接触对象存储。

Artifact 与 Cache 的产品语义不同：Artifact 是用户可下载和保留的构建输出；Cache 是
可随时丢失的性能优化。两者复用 Blob Service，但缓存使用独立的 `pipeline-cache/`
namespace、PostgreSQL 元数据和 TTL/GC，不会创建 Package 或 Release。

## Workflow 语法

```yaml
jobs:
  test:
    runs-on: [linux]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/cache@v4
        with:
          path: |
            target
            .ci-cache
          key: rust-${{ runner.os }}-${{ hashFiles('Cargo.lock') }}-${{ hashFiles('rust-toolchain.toml') }}
          restore-keys: |
            rust-${{ runner.os }}-
            rust-
      - run: cargo test --locked
```

- `key`、`path` 必填。key 支持 `${{ runner.os }}` 与单 pattern 的
  `${{ hashFiles('pattern') }}`；可在一个 key 中写多个表达式。
- `path` 是一行或多行 workspace 相对路径。`.`、绝对路径、逃逸 workspace 的 `..`、
  符号链接和不存在的全部路径不会被上传。
- `restore-keys` 最多 10 行，按声明顺序做字面前缀匹配；每个前缀选择最新的 ready、
  未过期缓存。`%` 与 `_` 没有 SQL wildcard 语义。
- archive version 包含格式版本、Runner OS 和规范化路径集合，因此不同 OS/路径不会误用
  同一份字节。

完全命中 primary key 后不会再上传。缓存 miss 或 restore-key 命中时，Runner 只会在 job
成功后发布 primary key；上传失败不改变 job 结论。相同 repo/key/version 的值不可变，
并发发布由第一个完成的 writer 获胜。

## 权限和信任边界

缓存 namespace 由服务端根据 job 的 org/project/repo 决定，Runner 不能在请求中选择其他
仓库。job 必须仍处于 running 且属于当前 runner。`pull_request` job 可以读取同仓库由可信
push/manual job 发布的缓存，但不能上传；这避免 PR 修改 key 后污染主分支构建。

Runner 下载后校验服务端记录的 SHA-256，并先解到 job 临时目录。以下内容会被拒绝：

- 绝对路径、父目录穿越和配置 path 之外的 archive entry；
- symlink、hardlink、device、FIFO 等非普通文件/目录；
- 指向 workspace 外的缓存源，或 workspace 中已有的 symlink 目标。

缓存恢复不可用、checksum 错误或 archive 不安全时，Runner 记录 warning 并继续执行构建。
不要缓存 `.env`、私钥、云凭据、完整 workspace、未经审计的用户上传内容或会跨信任边界
执行的二进制。key 至少应包含 OS、工具链/ABI、构建 profile 与锁文件 hash。

## 服务端存储与回收

PostgreSQL 的 `pipeline_cache_entries` 保存 repo scope、key、archive version、Blob key、
size、SHA-256、状态与过期时间。上传先创建 `uploading` reservation，Artifact Service 完整
接收且 size/checksum 一致后才原子切换为 `ready`。中断上传不会被 restore 看见。

默认配置：

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `WULING_PIPELINE_CACHE_MAX_UPLOAD_BYTES` | `536870912` | 单 archive 上限（512 MiB） |
| `WULING_PIPELINE_CACHE_TTL` | `336h` | ready 缓存保留 14 天 |
| `WULING_PIPELINE_CACHE_GC_INTERVAL` | `15m` | GC 周期 |

GC 启动时立即执行，之后每个周期处理一个有界批次。它先删除 Blob，再删除元数据；Blob
删除失败时保留 expired 行供下次重试。Artifact Service 的上传上限也必须不小于 Pipeline
Cache 上限。

## Local、OSS、S3 与 R2

缓存沿用 `WULING_ARTIFACTS_STORAGE_PROVIDER`：开发环境可使用 local，生产可使用 AWS S3、
Cloudflare R2 或阿里云 OSS。凭据只配置在 `wuling-artifacts`，Runner 只需连通 Wuling API。

- OSS/S3 尽量与服务同地域并使用私网/VPC endpoint，避免 NAT、公网和跨区流量费用。
- 短 TTL cache 通常不适合有最低保存期或提前删除费用的 IA/归档存储类。
- R2 需要同时预算 Class A 写入/删除、Class B 读取和存储；不要把大量小文件拆成对象，
  内置 action 已将配置路径合并为一个 tar。
- bucket versioning 或 provider lifecycle 不能替代数据库 GC；若启用版本控制，还要清理
  DELETE marker 与非当前版本。

建议监控命中率、上传/下载字节、对象数、失败率和过期删除积压。缓存永远是可重建的；
若业务不能承受删除，请使用 Artifact/Package，而不是 Cache。
