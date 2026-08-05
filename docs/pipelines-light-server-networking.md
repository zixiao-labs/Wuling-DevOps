# 阿里云轻量应用服务器与 Pipelines 内网互联

本仓库的 autoscaler 当前只置备阿里云 **ECS** 和 AWS EC2 Runner。轻量应用服务器
（Simple Application Server，SAS）不是可选的 autoscaler provider；本指南只说明把
部署在轻量服务器上的 Pipelines 控制面、Git 服务或未来 Workspace 服务，与 ECS Runner
置于可验证私网链路所需的前置条件。

不要把“能从公网访问”当成内网互通完成。公网路径会绕过预期的安全边界，且可能产生
流量费用与不稳定的 DNS 解析。

## 前置条件

1. 轻量应用服务器和目标 ECS VPC 位于**同一阿里云账号、同一地域**。
2. 两端 CIDR 不重叠。记录轻量服务器的网段和 ECS VPC/vSwitch 网段；重叠时路由会不
   确定，无法通过安全组规则修复。
3. 在轻量应用服务器控制台创建并等待 **Service Interconnection / 服务互联** 成功，
   并选择目标 ECS VPC。控制台名称和支持范围可能随地域/产品更新，以阿里云当前文档
   为准；若此功能不可用，使用经过审批的 VPC peering、CEN 或私网反向代理方案。
4. `runner-config.yaml` 的 ECS pool 使用互联目标 VPC 内的 `vpc_id`、`vswitch_id`
   和安全组。vSwitch 的可用区必须与所选实例规格、镜像相容。

## 最小网络规则

以控制面部署在轻量服务器、Runner 部署在 ECS 为例：

| 流量 | 来源 | 目标 | 规则 |
|---|---|---|---|
| Runner → API/Git | ECS Runner vSwitch CIDR | 轻量服务器私网地址 | TCP 443（或实际 API/Git 端口） |
| Runner → DNS | ECS vSwitch | 企业/阿里云 DNS | UDP/TCP 53（按实际 DNS） |
| 控制面 → Runner（可选） | 轻量服务器网段 | ECS Runner | 仅在需要回调、诊断或指标端口时开放 |
| 运维 SSH/RDP | 跳板机 CIDR | ECS/轻量服务器 | 仅管理员来源，不能是 `0.0.0.0/0` |

ECS 安全组控制 ECS 网卡，轻量应用服务器使用自身防火墙规则；两边都要允许所需方向。
安全组/防火墙应使用 CIDR 或专用安全组引用，不能只依赖实例的公网 IP。保持 outbound
默认放通前，请确认 Runner 所需的软件源、对象存储和镜像仓库都有明确的出站策略。

## 私网 DNS 与连通性验证

为控制面使用一个稳定的内部域名，例如 `wuling.internal.example`，在私网 DNS 中解析到
轻量服务器的私网地址。Runner 的 `server_url` 必须使用该 HTTPS 域名，证书的 SAN 也
必须包含它；不要在 YAML 中硬编码临时私网 IP。

在同一 pool 镜像或一台测试 ECS 上逐项验证：

```bash
getent hosts wuling.internal.example
curl --fail --silent --show-error https://wuling.internal.example/healthz
nc -vz wuling.internal.example 443
```

Windows 可使用：

```powershell
Resolve-DnsName wuling.internal.example
Test-NetConnection wuling.internal.example -Port 443
Invoke-WebRequest https://wuling.internal.example/healthz -UseBasicParsing
```

检查结果应包含解析到预期私网地址、TLS 验证成功以及 API `healthz` 返回成功。不要为了
排障关闭证书验证或永久开放宽泛防火墙规则。

## 与 OSS、Workspace 和自动伸缩的关系

- ECS Runner 与同地域 OSS bucket 通信时优先选 OSS 内网 endpoint，并给 Runner 最小
  bucket/prefix 权限；详细缓存策略见 [pipelines-cache.md](pipelines-cache.md)。
- 轻量应用服务器可承载控制面或跳板服务，但它不由本项目的 Runner autoscaler 创建、
  伸缩或销毁。
- Workspace 仍是后续阶段能力；本指南中的互联、DNS、CIDR 和防火墙约束可复用，但不
  表示已存在 Workspace autoscaler。

## 上线前清单

- [ ] 账号、地域、CIDR 与 Service Interconnection 均已复核。
- [ ] ECS vSwitch、安全组、镜像和 Runner `server_url` 属于同一预期网络。
- [ ] 两侧防火墙按最小端口和来源 CIDR 配置。
- [ ] Linux 和 Windows 测试 VM 都已完成 DNS、TLS 与 API 连通性检查。
- [ ] OSS/镜像仓库的 endpoint 路径和流量费用已纳入预算。
- [ ] 管理员已理解真实 Runner 自检会临时创建并计费 ECS 实例。
