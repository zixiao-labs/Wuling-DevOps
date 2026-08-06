---
title: 远端 CI 缓存
group: Pipelines
order: 35
description: 在弹性 Runner 上安全使用 OSS、S3 或 Cloudflare R2 缓存依赖，并避免常见账单陷阱。
---

# 远端 CI 缓存

内置 `actions/cache` 是 Runner 的本地 tar 缓存，不是 OSS、S3 或 Cloudflare R2 的
远端后端。它只适合同一台 Runner 重复执行；弹性 Runner、独占任务和隔离任务会启动
新 VM，不能依赖本地缓存命中。

Artifact 与缓存也不同：Artifact 是可下载、可保留的构建输出；缓存是可随时删除的加速
数据。请使用独立 bucket 与 prefix，不要把缓存写进 Artifact Blob 存储。

## 安全的缓存 key

key 至少包含项目、OS、CPU 架构、工具链版本和锁文件 hash。例如：

```text
ci-cache/<project>/rust/<os>/<arch>/<toolchain>/<Cargo.lock.sha256>
```

不要把分支名或每次构建的提交 SHA 当作唯一 key；这样会无限积累对象。不要将 `.env`、
私钥、凭据、未审计上传内容或整个工作目录打进缓存。

## 连接对象存储

- **阿里云 OSS**：Runner 与 bucket 同地域时使用 OSS 内网 endpoint；配置 lifecycle
  删除过期对象，并关注 PUT/Copy/LIST 与小对象请求的累计费用。
- **AWS S3**：使用同地域 bucket，必要时配 VPC endpoint。短期 CI 缓存不要误放到有
  最短保存期或提前删除费的 IA/归档存储类；同时计算请求、KMS、跨区复制和 NAT 流量。
- **Cloudflare R2**：使用 S3 兼容 endpoint 与 `auto` region。写入和列举属于 Class A，
  读取属于 Class B；将大量小文件打包后上传。选择 Infrequent Access 前确认生命周期
  超过当前条款的最短保存期（通常为 30 天）。

CI 凭据只能取得项目 prefix 的 `GetObject`、`PutObject`、`HeadObject`（和必要时的
受限删除）权限。优先使用短效凭据或工作负载身份，绝不把长效 Access Key 写入 YAML。

## 恢复与写入策略

下载必须是可失败的优化：缓存不可用时继续构建。上传应写入临时对象，完成 SHA-256
校验后再提升为稳定 key；读取方忽略 `.partial` 对象。为 prefix 设置 7–30 天 TTL、
容量上限、每日写入预算和对象数/命中率告警。

完整的对象布局、示例脚本、provider 账单检查项见仓库
[`docs/pipelines-cache.md`](https://github.com/zixiao-labs/wuling-devops/blob/main/docs/pipelines-cache.md)。
