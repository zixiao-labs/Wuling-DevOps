---
title: Runner
group: 运维
order: 30
description: 注册与运行自托管 Runner，执行流水线作业。
---

## 什么是 Runner

**Runner** 是执行流水线作业的工作进程。武陵 DevOps 支持：

- **共享 Runner** — 平台提供的默认执行环境
- **自托管 Runner** — 在你自己的机器或 VM 上运行

## 注册自托管 Runner

1. 进入 **组织设置 → Runners**（或项目级 Runners）。
2. 点击 **注册 Runner**，复制注册令牌。
3. 在目标机器下载 Runner 客户端并执行注册命令。

```bash
wuling-runner \
  --server-url https://your-host \
  --registration-token <registration-token> \
  --name my-runner \
  --labels linux,docker
```

## 运行 Runner

上述命令会在首次启动时兑换一次性注册令牌，并立即开始轮询待执行作业；Runner 客户端没有 `register` 或 `run` 子命令。已有持久 Runner token 时，可改用环境变量启动：

```bash
WULING_RUNNER_SERVER_URL=https://your-host \
WULING_RUNNER_TOKEN=<runner-token> \
wuling-runner
```

## 标签与资源

为 Runner 配置 **标签**（如 `linux`、`docker`）以便流水线通过 `runs-on` 选择执行环境。组织管理员可在 GitOps 中配置 Runner 的容器资源上限。

## runner-config 的 YAML Language Server

组织级 `runner-config.yaml` 位于 config 仓库默认分支的根目录。给该文件首行加入 modeline，可在任意 config 仓库中获得 schema 补全和结构检查：

```yaml
# yaml-language-server: $schema=https://<wuling-origin>/.well-known/wuling/schemas/v1/runner-config.json
```

将 `<wuling-origin>` 替换为实际武陵 DevOps 站点 origin。schema 覆盖 v1 兼容配置以及 v2
`vpc_id`、`data_disks`、`runner_data_disk` 字段。

Language Server 只检查静态结构；服务端仍会权威校验 Secret 引用、VPC/子网与磁盘挂载的关联、云供应商限制及 Runner 容量。未实现的运行语义不能仅凭编辑器无报错视为已启用。

## 故障排查

- 确认 Runner 在线且心跳正常
- 检查注册令牌是否过期
- 查看 Runner 日志中的作业输出
