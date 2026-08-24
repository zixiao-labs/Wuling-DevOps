---
title: 远端 CI 缓存
group: Pipelines
order: 35
description: 使用内置 actions/cache 在弹性与隔离 Runner 之间安全复用构建目录。
---

# 远端 CI 缓存

内置 `actions/cache` 会把缓存上传到 Wuling API 背后的 Artifact Service，因此 static、
ephemeral 和 `execution.mode: isolated` Runner 可以命中同一仓库的缓存。缓存只是可丢失的
加速数据，不会出现在项目的 Package、Release 或 Artifact 列表中。

## 工作流示例

```yaml
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

  - run: cargo test --locked
```

`path` 必须是 workspace 相对路径，可以写多行；不接受绝对路径、逃逸 workspace 的 `..`
或符号链接。key 支持 `${{ runner.os }}` 和单 pattern 的 `${{ hashFiles('...') }}`。
`restore-keys` 按声明顺序做前缀匹配，同一前缀选择最新缓存。

完全命中 key 时不会重复上传；未命中或只命中 restore-key 时，会在 job 成功后发布 primary
key。缓存不可变，并发写入由第一个成功发布者获胜。`pull_request` job 可以读取同仓库缓存，
但不能写入，避免不受信任的 PR 污染后续构建。

## 安全与排障

- 缓存按 repo 隔离，下载校验 SHA-256 后先解到临时目录；绝对路径、父目录穿越、链接、
  设备文件和配置路径之外的条目都会被拒绝。
- 下载、校验或解包失败会记录 warning 并继续构建；上传失败也不会改变正确的 job 结论。
- 不要缓存 `.env`、私钥、云凭据、完整 workspace 或未经审计的用户上传内容。
- key 应包含 OS、工具链/ABI 和锁文件 hash；把 commit SHA 放进每个 key 通常只会制造永不
  复用的对象。

默认单对象上限为 512 MiB，TTL 为 14 天，GC 每 15 分钟运行一次。管理员可通过
`WULING_PIPELINE_CACHE_MAX_UPLOAD_BYTES`、`WULING_PIPELINE_CACHE_TTL` 和
`WULING_PIPELINE_CACHE_GC_INTERVAL` 调整。底层 local、AWS S3、Cloudflare R2 或阿里云
OSS 由 Artifact Service 配置，Runner 不需要也不应持有 bucket 凭据。

更完整的语义、运维与成本说明见仓库
[`docs/pipelines-cache.md`](https://github.com/zixiao-labs/wuling-devops/blob/main/docs/pipelines-cache.md)。
