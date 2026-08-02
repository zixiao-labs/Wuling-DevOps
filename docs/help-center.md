# 帮助中心文档编写指南

武陵 DevOps 帮助中心位于 `/help/*`，由 Node SSR 渲染（零 hydration），源码在 `frontend/src/help/content/`。

## 目录与 URL

Markdown 文件路径映射为 URL slug：

| 文件 | URL |
|------|-----|
| `index.md` | `/help` |
| `getting-started.md` | `/help/getting-started` |
| `pipelines/quick-start.md` | `/help/pipelines/quick-start` |

**不要**镜像仓库根目录的 `docs/`——帮助内容独立维护，面向终端用户（目前仅 zh-CN）。

## Front matter

每个文件以 YAML front matter 开头：

```markdown
---
title: 页面标题
group: 分组名
order: 10
description: 用于 SEO 与搜索摘要的简短描述。
---

正文…
```

- `group` — 侧边栏分组
- `order` — 组内排序（数字越小越靠前）

## 支持的 Markdown

帮助文档使用 `markdown-core.js` 的 **docs 模式**（含链接、标题锚点、表格、分隔线）：

- 标题 `#`–`######`（h2+ 自动生成 TOC 锚点）
- 粗体、斜体、行内代码、围栏代码块
- 引用、无序列表
- `[文本](/help/...)` 链接（仅 `http(s):` 或以 `/` 开头的相对路径）
- GFM 表格、 `---` 水平线

Issue/MR 正文仍使用精简模式，输出与迁移前保持一致。

## 本地预览

```bash
cd frontend
npm run dev
# 打开 http://localhost:3000/help
```

开发服务器通过 `help-docs-plugin` 中间件 SSR `/help/*`，修改 content 后会热重载。

## 构建与预渲染

```bash
npm run build        # dist/ + dist-help/
npm run help:smoke   # 预渲染到 dist/help/ 并校验 sw.js 绕过 /help
```

生产部署：`Dockerfile.frontend` 在 builder 阶段运行 `node dist-help/prerender.js dist`，Caddy 在 help 容器不可用时从静态卷提供相同 HTML。

## 架构要点

- **无 HeroUI** — 帮助页使用 plain HTML + 应用 Tailwind token（`.wuling-prose`）
- **SPA 侧栏「帮助」** — 普通 `<a href="/help">`，非 client-side router Link
- **Service Worker** — `build/service-worker-plugin.js` 对 `/help/*` 不做缓存，始终走网络

详见设计文档：`~/.claude/projects/-Users-logos-WebstormProjects-Wuling-DevOps/designs/design-help-center-ssr.json`
