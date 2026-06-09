# 武陵 DevOps · 品牌资源 (Brand Kit)

方案 A —「武」字切角印。终末地 / 工业终端风的青蓝切角方块，主符号是几何化的「武」字，
右上角切角 + 一道青色"切口高光"是记忆点。

## 文件

| 文件 | 用途 |
|---|---|
| `logo-mark.svg` | 主标（切角方块，固定青蓝渐变）——用于 README、文档、深浅背景 |
| `icon-fullbleed.svg` | App 图标母版（满版，无圆角无透明，由系统遮罩）——iOS / PWA 渲染源 |
| `icon-maskable.svg` | 参考：更紧的安全区版本 |
| `icon-1024.png` | iOS App 图标母版 1024×1024（不透明，可直接进 Xcode Asset Catalog / App Store） |

实际被前端引用的图标在 `frontend/public/`：`favicon.svg`（明暗自适应）、
`apple-touch-icon.png`(180)、`icon-192.png`、`icon-512.png`(PWA manifest)。

## 配色（hue ≈ 201，青蓝）

| Token | Hex | 说明 |
|---|---|---|
| 品牌主色 | `#5a8fb0` | 等于 `clean` 主题 `--accent` 与 manifest `theme_color` |
| 渐变浅 | `#6ea7c9` | 图标背景渐变顶 |
| 渐变深 | `#3d6e8a` | 图标背景渐变底 |
| 切口高光 | `#a9dcef` | 切角处的青色斜线 |
| 字面 | `#ffffff` | 「武」字 |

## 字体来源 / License

「武」字形提取自 **Noto Sans SC** (Bold / 700)，授权 **SIL Open Font License 1.1**
——按 OFL 条款，从字体派生 logo 字形并嵌入产品完全合规。字形已转为 SVG `<path>`
轮廓，不再依赖任何系统字体（旧 favicon 用 `<text>` + system-ui，跨 OS 渲染不一致）。

## 重新生成

需要 `python3 + fonttools + brotli`、`rsvg-convert`、ImageMagick(`magick`)。
脚本会拉取 Noto Sans SC 单字「武」子集（仅 ~1KB）、导出轮廓、生成全部 SVG/PNG。
生成器见提交历史（`gen_logo.py`），输出已固化在本目录与 `frontend/public/`。
