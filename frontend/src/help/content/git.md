---
title: Git 操作
group: 开发
order: 20
description: 克隆、推送、SSH 密钥与 Smart HTTP。
---

## 远程地址格式

武陵 DevOps 支持 **SSH** 与 **HTTPS（Smart HTTP）** 两种方式：

| 协议 | 示例 |
|------|------|
| SSH | `git@your-host:org-slug/project-slug/repo-slug.git` |
| HTTPS | `https://your-host/org-slug/project-slug/repo-slug.git` |

在仓库 **克隆** 面板可复制完整地址。

## SSH 密钥

1. 进入 **用户设置 → SSH 密钥**。
2. 粘贴 `id_ed25519.pub` 或 `id_rsa.pub` 公钥内容。
3. 保存后即可使用 SSH 协议克隆与推送。

```bash
ssh-keygen -t ed25519 -C "you@example.com"
cat ~/.ssh/id_ed25519.pub
```

## 常用命令

```bash
git clone git@your-host:my-org/my-project/my-repo.git
cd my-repo
git checkout -b feature/my-change
git push -u origin feature/my-change
```

## 合并请求

推送分支后，在仓库页面创建 **合并请求（MR）**，指定目标分支与审查者。流水线状态会显示在 MR 页面上。
