---
title: 轻量服务器与 Runner 内网
group: Pipelines
order: 34
description: 将阿里云轻量应用服务器上的控制面与 ECS 弹性 Runner 安全地连接到同一私网链路。
---

# 轻量服务器与 Runner 内网

本平台目前只会自动创建阿里云 **ECS** 与 AWS EC2 Runner。阿里云轻量应用服务器不是
autoscaler provider；它可以部署 Pipelines 控制面、Git 服务或跳板服务，但不会被本平台
自动扩缩容。

## 建立互联

轻量应用服务器和目标 ECS VPC 必须属于同账号、同地域，且 CIDR 不重叠。在轻量应用
服务器控制台创建 **Service Interconnection / 服务互联** 并选择 ECS VPC；控制台可用
性因地域和产品更新而异，请以阿里云当前页面为准。功能不可用时使用经审批的 VPC
peering、CEN 或私网反向代理。

ECS pool 的 `vpc_id`、`vswitch_id` 和安全组必须指向这个目标 VPC。只使用公网连通
不等于内网互联成功。

## 放行最小流量

- ECS Runner → 轻量服务器控制面：TCP 443（或实际 API/Git 端口）。
- Runner → DNS：按企业 DNS 实际开放 UDP/TCP 53。
- 只在确有回调/诊断需要时，允许轻量服务器 → Runner。
- SSH/RDP 只能从跳板机 CIDR 进入，不能开放给 `0.0.0.0/0`。

ECS 使用安全组，轻量应用服务器使用自身防火墙规则；两边都要配置。为控制面配置稳定的
私网 DNS 名称和对应 TLS 证书，不要在 Runner 配置中写临时私网 IP。

在 Linux Runner 镜像中验证：

```bash
getent hosts wuling.internal.example
curl --fail https://wuling.internal.example/healthz
nc -vz wuling.internal.example 443
```

Windows 使用 `Resolve-DnsName`、`Test-NetConnection` 和 `Invoke-WebRequest` 做同样
验证。

同地域 OSS 优先使用内网 endpoint。完整的网络清单、Windows 验证及 Workspace 的当前
边界见仓库 [`docs/pipelines-light-server-networking.md`](https://github.com/zixiao-labs/wuling-devops/blob/main/docs/pipelines-light-server-networking.md)。
