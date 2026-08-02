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
./wuling-runner register \
  --url https://your-host \
  --token <registration-token> \
  --name my-runner
```

## 运行 Runner

注册成功后启动 Runner 进程，它会轮询待执行作业：

```bash
./wuling-runner run
```

## 标签与资源

为 Runner 配置 **标签**（如 `linux`、`docker`）以便流水线通过 `runs-on` 选择执行环境。组织管理员可在 GitOps 中配置 Runner 的容器资源上限。

## 故障排查

- 确认 Runner 在线且心跳正常
- 检查注册令牌是否过期
- 查看 Runner 日志中的作业输出
