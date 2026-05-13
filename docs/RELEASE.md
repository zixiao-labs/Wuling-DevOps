# Release 流程

武陵 DevOps 的发布完全由 `git tag` 驱动：tag 一推，CI 全自动产出二进制 / 镜像 / 前端 bundle / Nix 制品 + GitHub Release 页面。

## 切一个 release

```bash
# 0. 确保 main 是绿的（CI 全绿、本地 go test 通过）
git checkout main
git pull --ff-only

# 1. 选 version 号 — semver
#    v0.2.0    — Stage 1 GA / 显著功能
#    v0.2.1    — bug fix
#    v0.3.0-rc.1 — pre-release（CI 也会处理，但 GitHub 上会标 "Pre-release"）
TAG=v0.2.0

# 2. 打 annotated tag
git tag -a "$TAG" -m "Wuling DevOps $TAG"

# 3. 推
git push origin "$TAG"
```

推上去后：

1. **`.github/workflows/release.yml`** 触发，跑 5 个 job：
   - `build-binaries` —— 跨编译 Linux/amd64+arm64, macOS/amd64+arm64 四份 `wuling-api` + `wuling-migrate`，含 SHA256
   - `build-frontend` —— `npm run build` 出 `frontend/dist/`，打成 tar.gz + SHA256
   - `build-images` —— 推 multi-arch docker 镜像到 `ghcr.io/zixiao-labs/wuling-api:<tag>` 和 `ghcr.io/zixiao-labs/wuling-frontend:<tag>`
   - `nix-check` —— 跑 `nix flake check`，保证 flake 没烂
   - `publish-release` —— 等上面 4 个都过了，建 GitHub Release 并挂上全部产物
2. **GHCR** 自动得到 3 个 tag：`<version>`、`latest`、`sha-<7位>`。生产部署用 `<version>` 锁版本，**不要**追 `latest`。

整个流程大约 10–15 分钟，看 GHA 资源争抢情况。

## 产物清单（每个 release 都有）

| 产物 | 路径示例 |
|---|---|
| Linux amd64 二进制 | `wuling-api-linux-amd64.tar.gz` |
| Linux arm64 二进制 | `wuling-api-linux-arm64.tar.gz` |
| macOS amd64 二进制 | `wuling-api-darwin-amd64.tar.gz` |
| macOS arm64 二进制 | `wuling-api-darwin-arm64.tar.gz` |
| migrate 工具（同上 4 平台） | `wuling-migrate-<os>-<arch>.tar.gz` |
| 前端 bundle | `wuling-frontend-dist-vX.Y.Z.tar.gz` |
| 每个的 SHA256 | `<filename>.sha256` |
| API 镜像 | `ghcr.io/zixiao-labs/wuling-api:vX.Y.Z` |
| 前端镜像 | `ghcr.io/zixiao-labs/wuling-frontend:vX.Y.Z` |
| Nix flake | `github:zixiao-labs/Wuling-DevOps/vX.Y.Z#wuling-api` 等 |

## 校验产物

```bash
# 二进制
curl -LO https://github.com/zixiao-labs/Wuling-DevOps/releases/download/v0.2.0/wuling-api-linux-amd64.tar.gz
curl -LO https://github.com/zixiao-labs/Wuling-DevOps/releases/download/v0.2.0/wuling-api-linux-amd64.tar.gz.sha256
sha256sum -c wuling-api-linux-amd64.tar.gz.sha256

# 镜像（用 docker 自带的 digest 检查；或者用 cosign，将来会引）
docker pull ghcr.io/zixiao-labs/wuling-api:v0.2.0
docker image inspect --format '{{.Id}}' ghcr.io/zixiao-labs/wuling-api:v0.2.0
```

## 回滚

镜像不会被删；只要把生产环境的 image tag 调回上一个版本就行：

```bash
# Docker Compose
WULING_TAG=v0.1.9 docker compose -f deploy/docker-compose.prod.yml up -d

# k8s
kubectl -n wuling set image deploy/wuling-api api=ghcr.io/zixiao-labs/wuling-api:v0.1.9
```

如果新版本带了破坏性 migration，回滚前先用 `deploy/production/postgres/restore.sh` 把数据库还原到升级前的快照。

## Pre-release / RC

tag 写成 `v0.3.0-rc.1` 的 release 会被 GitHub 标记为 "Pre-release"（softprops/action-gh-release 自动识别 semver pre-release 后缀）。镜像不会被打 `latest` tag。

## 紧急 hotfix

```bash
git checkout v0.2.0
git checkout -b hotfix/v0.2.1
# 改代码、做提交…
git tag -a v0.2.1 -m "hotfix: …"
git push origin hotfix/v0.2.1 v0.2.1
# 之后 cherry-pick 回 main
```

## CI 用到的 secret

| Secret | 用途 |
|---|---|
| `SYNC_PAT` | 创建 GitHub Release 时绕开默认 `GITHUB_TOKEN` 的某些限制（项目内既有惯例） |
| `GITHUB_TOKEN`（自动） | 推 ghcr.io 镜像 |

如果某天我们启用代码签名（cosign / SLSA provenance），还会加：

- `COSIGN_KEY` / `COSIGN_PASSWORD`，或者用 OIDC keyless 签名
