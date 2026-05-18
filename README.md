# Wuling-DevOps

武陵DevOps（玩终末地的都懂）

## 🚀快速开始

> ⚠️ **安全提示**：在执行任何脚本之前，建议先下载并审查脚本内容，确保了解其操作。

### 前置要求
- 操作系统：Linux/macOS/Windows Server
- 权限：需要管理员/root 权限
- 依赖：Git、Docker、Nix、K8s（可选）

### 一键部署

该脚本将自动完成以下操作：
- 安装必要的依赖（Docker、Git 等）
- 克隆项目仓库
- 配置开发环境
- 启动相关服务


Linux🐧/macOS🍎

```bash
curl https://raw.githubusercontent.com/zixiao-labs/Wuling-DevOps/refs/heads/main/prod-deploy.sh | bash
```
Windows Server🪟

```powershell
irm https://raw.githubusercontent.com/zixiao-labs/Wuling-DevOps/refs/heads/main/prod-deploy.ps1 | iex
```

## 📚 文档

- [`deploy/production/README.md`](deploy/production/README.md) — 生产部署 Runbook（Docker Compose / Nix / k8s）
- [`docs/auth.md`](docs/auth.md) — 身份认证：GitHub OAuth 登录 + 注册审批工作流的配置和运维

## 关于项目

因为[前一个版本](https://github.com/zixiao-labs/yuxupalace-server)的话@HwlloChen回来就没办法维护了，再加上改名改到底，直接用Go和C++（Libgit2）重写🙃

一样会提供Logos Editor集成，Github OAuth，CLI，手机APP（React Native+隔壁[赤刃明宵陈框架](https://github.com/zixiao-labs/Chen-the-Dawnstreak)），Docker注册表，npm注册表，PyPI注册表，Cargo注册表，云端开发容器，CRDT实时协作，Channels实时频道，APM遥测（放心，不是Sentry OSS换皮），MCP服务器，API，Claude Code Skill

冷知识：~~在塔卫二上打开会App unavailable~~ 因为在塔卫二没有服务器啊


还有用Github Actions做竞品平台的构建和部署真的没问题吗（~~小声bb~~）

还有Zed集成终于回来了（只不过是Fork，出门右拐[Kal'tsit·Esperanta](https://github.com/zixiao-labs/Esperanta)）

