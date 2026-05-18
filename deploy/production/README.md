# Wuling DevOps · 生产部署 Runbook

本目录覆盖三条**生产**部署路径，按你的偏好挑一条。dev 用的 `deploy/docker-compose.yml`
和 `deploy/Dockerfile` 不要直接当生产环境使。

| 路径 | 适合 | 在本目录看哪些文件 |
|---|---|---|
| **Docker Compose**（推荐起步） | 单机、自管 VPS、家里的小服务器 | `docker-compose.prod.yml` · `Caddyfile` · `env.example` |
| **Nix Flake / NixOS 模块** | NixOS、不想用容器、追求声明式与原子升级 | `flake.nix` · `nix/module.nix` |
| **Kubernetes** | 已有 k8s 集群，想做横向伸缩 / 滚动升级 | `k8s/*.yaml` |

不论选哪一条，都共用同一套环境变量（`env.example`）和同一组 Postgres 备份脚本
（`postgres/backup.sh` · `postgres/restore.sh`）。

---

## 1. 架构与端口

```text
                    ┌─────────────────────────────┐
                    │  浏览器 / git client / SSH   │
                    └──────────────┬──────────────┘
                                   │
                  ┌────────────────┴───────────────┐
                  │  TLS 反向代理 (Caddy / Nginx)   │  :80  → 重定向到 :443
                  │  ─ HTTPS / HTTP-01 自动证书     │  :443 → 内部 :8080
                  │  ─ 透传 *.git/ 给 API           │
                  └────────────────┬───────────────┘
                                   │ 8080 (HTTP)
                  ┌────────────────┴───────────────┐
                  │  wuling-api（cgo + libgit2）   │
                  │  ─ /api/v1/*                    │
                  │  ─ /{org}/{project}/{repo}.git/ │
                  │  ─ embedded Git SSH 2222 (TCP)  │  ←─── 外网直接暴露 2222
                  └────────┬─────────────┬──────────┘
                           │             │
                  ┌────────┴────┐  ┌─────┴────────────┐
                  │ Postgres 18 │  │ /var/lib/wuling/  │
                  │ (持久卷)     │  │   repos (持久卷)   │
                  └─────────────┘  └──────────────────┘

  static frontend (Nasti build → dist/) — Caddy 直接吐 / nginx 静态托管
```

**对外端口：**

| 端口 | 用途 | 必须？ |
|---|---|---|
| 80 | HTTP → HTTPS 重定向；HTTP-01 challenge | 用 Caddy 自动 TLS 时是 |
| 443 | HTTPS（前端 + REST + Git smart HTTP） | **必须** |
| 2222 | Git over SSH | 想让用户用 SSH 推/拉就要开。家庭网络注意路由器/防火墙端口转发 |

---

## 2. 环境变量

从 `env.example` 拷一份成 `.env`，挨个填。**至少**改这几样：

| 变量 | 含义 | 生成建议 |
|---|---|---|
| `WULING_JWT_SECRET` | JWT 签名密钥 | `openssl rand -hex 48`，**绝不要**用 dev 里那个字符串 |
| `WULING_DB_DSN` | Postgres 连接串 | 用一个 **只读+读写两套** 用户，至少限制到本 db |
| `WULING_REPO_ROOT` | 仓库根目录 | 挂到持久卷，**不要**放 `/tmp` |
| `WULING_SSH_HOST_KEY` | SSH 主机密钥 | 首次启动会自动生成；要稳定的话用 `ssh-keygen -t ed25519 -f host_ed25519 -N ""` 预先生成，固化在持久卷里 |
| `WULING_HTTP_CORS_ORIGINS` | 前端域名（多个用逗号） | `https://devops.example.com` |

不要在 git 里提交真实的 `.env`。

---

## 3. 路径 A：Docker Compose（推荐起步）

### 3.1 准备工作

```bash
cd deploy/production
cp env.example .env
# 用编辑器打开 .env，至少改 WULING_JWT_SECRET / WULING_DOMAIN
```

> **强烈建议**：上线前先读一遍 [`docs/auth.md`](../../docs/auth.md)。它详细解释了
> 注册审批工作流（`WULING_AUTH_REQUIRE_APPROVAL`，默认开启）和 GitHub OAuth
> 登录的配置步骤，以及如何为新装的实例引导第一个管理员。

### 3.2 首次启动

```bash
# 1. 建两个持久卷（compose 会自动创建，但显式创建可以指定挂载点）
docker volume create wuling-pg
docker volume create wuling-repos
docker volume create wuling-caddy-data

# 2. 启动
docker compose -f docker-compose.prod.yml up -d

# 3. 初始化数据库
docker compose -f docker-compose.prod.yml exec api wuling-migrate

# 4. 测一下
curl -sf https://${WULING_DOMAIN:-localhost}/healthz
```

第一次启动 Caddy 会用 HTTP-01 从 Let's Encrypt 拿证书；保证 80/443 端口在外网可达。

### 3.3 升级

```bash
# 拉新镜像
docker compose -f docker-compose.prod.yml pull
# 跑 migrate（每次升级前都跑一次）
docker compose -f docker-compose.prod.yml run --rm api wuling-migrate
# 重启
docker compose -f docker-compose.prod.yml up -d
```

### 3.4 回滚

镜像 tag 固定到具体版本（**不要**用 `:latest`）。回滚就是改回旧 tag + `docker compose up -d`。
如果新版本带破坏性 migration，回滚前用 `postgres/restore.sh` 还原 DB 快照。

---

## 4. 路径 B：Nix Flake / NixOS 模块（直接部署，不用容器）

适合 NixOS 上不愿意装 docker 的人，或者用 `nix profile install` 在任意 Linux/macOS 上跑二进制。

### 4.1 构建

```bash
# 在仓库根目录
nix build .#wuling-api          # → result/bin/wuling-api
nix build .#wuling-migrate      # → result/bin/wuling-migrate
nix build .#wuling-frontend     # → result/   (静态文件夹)
```

flake 暴露的 outputs：

| Output | 用途 |
|---|---|
| `packages.${system}.wuling-api` | 后端二进制（含 libgit2 静态/动态链接） |
| `packages.${system}.wuling-migrate` | 迁移工具 |
| `packages.${system}.wuling-frontend` | Nasti 构建出的 `dist/` |
| `packages.${system}.default` | 等价于 `wuling-api` |
| `nixosModules.default` | NixOS 系统模块（见下） |
| `devShells.${system}.default` | 含 Go、libgit2、Node 的开发环境 |

### 4.2 NixOS 部署

把 flake 引到你的 NixOS 配置：

```nix
# flake.nix（系统配置）
{
  inputs.wuling.url = "github:zixiao-labs/Wuling-DevOps";
  outputs = { self, nixpkgs, wuling, ... }: {
    nixosConfigurations.devops-host = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        wuling.nixosModules.default
        ./host.nix
      ];
    };
  };
}
```

然后在 `host.nix` 启用服务：

```nix
{ ... }: {
  services.wuling = {
    enable = true;
    domain = "devops.example.com";
    httpAddr = ":8080";
    sshAddr = ":2222";

    # Postgres 推荐用同机的 services.postgresql 起，连串走 Unix socket
    databaseUrl = "postgres:///wuling?host=/run/postgresql";

    # 强烈建议把 JWT secret 用 sops/agenix 注入；这里只是示意
    jwtSecretFile = "/run/secrets/wuling-jwt";

    # 仓库与 SSH host key 的持久路径
    repoRoot = "/var/lib/wuling/repos";
    sshHostKeyFile = "/var/lib/wuling/ssh/host_ed25519";

    frontend.enable = true;     # 用 nginx 静态托管 dist/
    reverseProxy = "caddy";     # 'caddy' | 'nginx' | 'none'
  };

  services.postgresql = {
    enable = true;
    ensureDatabases = [ "wuling" ];
    ensureUsers = [ { name = "wuling"; ensureDBOwnership = true; } ];
  };

  # 防火墙
  networking.firewall.allowedTCPPorts = [ 80 443 2222 ];
}
```

升级就是 `nixos-rebuild switch --flake .#devops-host`，原子切换 + 失败回滚 = 两条命令。

### 4.3 非 NixOS Linux / macOS 上的二进制部署

```bash
# 用 Nix 构出二进制（任意有 nix 的机器）
nix build .#wuling-api
nix build .#wuling-migrate

# 拷过去
scp result-*/bin/wuling-* user@host:/usr/local/bin/

# 用我们的 systemd unit
sudo cp deploy/production/systemd/wuling-api.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now wuling-api
```

systemd unit 已经设了 `EnvironmentFile=/etc/wuling/wuling.env`，把 `env.example` 拷过去填进。

---

## 5. 路径 C：Kubernetes

`k8s/` 目录给的是裸 manifests，能直接 `kubectl apply -k` 起来（kustomization 在 `k8s/kustomization.yaml`）。

```bash
# 1. 改 secret
cp k8s/secret.example.yaml k8s/secret.yaml
# 把 jwt-secret / pg-password 改成强随机值

# 2. 改 ingress 里的域名

# 3. 起
kubectl apply -k deploy/production/k8s/

# 4. 跑 migrate
kubectl -n wuling exec deploy/wuling-api -- wuling-migrate
```

涉及到的资源：

- `namespace.yaml` `configmap.yaml` `secret.example.yaml`
- `postgres-statefulset.yaml`（带 PVC）
- `api-deployment.yaml` + `api-service.yaml`
- `frontend-deployment.yaml` + `frontend-service.yaml`（nginx 静态文件）
- `ingress.yaml`（cert-manager 注解）

Pipelines 仍未实装，这里没相关 manifest。SSH 2222 暂时通过 `NodePort` 或 `LoadBalancer` Service 单独暴露（见 `api-service.yaml` 内的注释）。

---

## 6. 备份与恢复

**Postgres：**

```bash
# 每天凌晨 02:30 跑
deploy/production/postgres/backup.sh        # 输出 /var/backups/wuling/wuling-YYYYMMDD-HHMM.sql.gz

# 灾难恢复
deploy/production/postgres/restore.sh /var/backups/wuling/wuling-20260513-0230.sql.gz
```

把脚本放进 cron / `systemd.timer` / k8s `CronJob`。建议每天本地 `pg_dump` + WAL 归档异地（`pgbackrest` 是个好选择，但超出本 runbook 范围）。

**仓库存储：**

`WULING_REPO_ROOT` 下是裸 git 仓库。LVM/zfs 快照足够；如果没有快照，
`rsync --delete --link-dest=` 也行。**不要**热备份正在被 push 的仓库；
要么先 `systemctl stop wuling-api`，要么用文件系统快照。

---

## 7. 监控

- `/healthz` 返回 200 = liveness 通过（含 DB ping）
- `/version` 返回版本信息（JSON）
- 日志结构化 JSON（`WULING_LOG_FORMAT=json`），用 Loki / Vector / Fluent Bit 收一下。

**最小报警建议：**

| 现象 | 阈值 | 触发条件 |
|---|---|---|
| `/healthz` 非 200 | 连续 3 次 | 立刻 page |
| Postgres CPU > 90% | 持续 5 分钟 | 告警 |
| 仓库磁盘 > 85% | 跨阈值 | 告警 |
| SSH 2222 连接失败率 > 5% | 持续 10 分钟 | 告警 |

---

## 8. 安全清单

- [x] `WULING_JWT_SECRET` 是 ≥ 32 字节随机值，**不**入 git
- [x] Postgres 用独立 db user，没给 superuser
- [x] `WULING_REPO_ROOT` 上跑的进程是 non-root（compose 里用了 UID 10001；NixOS 模块走 DynamicUser；k8s pod 设 runAsNonRoot）
- [x] SSH 2222 主机密钥固化，**别**让它每次启动都重生
- [x] Caddy / nginx 强制 HTTPS（重定向 80 → 443）
- [ ] 关掉公开注册（待后端实装 env flag 后再勾。当下若要禁用，用反代里的 `path /api/v1/auth/register` 规则 403 掉）
- [ ] PAT 轮换计划（建议 90 天）

---

## 9. 发布流程（Release）

见 `../../docs/RELEASE.md`（本目录的 `release.yml` 工作流由 `git tag v*` 触发）。简而言之：

```bash
# 在 main 上
git tag -a v0.2.0 -m "Stage 1 GA"
git push origin v0.2.0
```

CI 会构建：

- Linux amd64 / arm64、macOS amd64 / arm64 的 `wuling-api` 与 `wuling-migrate`
- 前端 `wuling-frontend-dist.tar.gz`
- Docker 镜像 push 到 GHCR：`ghcr.io/zixiao-labs/wuling-api:v0.2.0`
- `nix flake check` 结果

产物全部附带 SHA256，发到 GitHub Release 页面。

---

## 10. 常见坑

- **首次启动 SSH host key 改了 → 所有用户 `Host key verification failed`。** 解法：**保存** host key 路径到持久卷，第一次启动后把它 chmod 600 备份起来。
- **Caddy 第一次拿不到证书。** 检查：80 端口外网可达、`WULING_DOMAIN` DNS 已生效。Let's Encrypt 同一证书每周限频 5 次，反复重试会被关进沙箱 1 小时。
- **`git clone` 用 PAT 报 401。** PAT 要带前缀 `wlpat_…`；用户名随便填，密码填 PAT 全文（含前缀）。
- **push 到 SSH 卡死。** 容器里 SSH 监听需要 `0.0.0.0:2222`（不是 `127.0.0.1`）。`WULING_SSH_ADDR=:2222` 已经是这样了。
