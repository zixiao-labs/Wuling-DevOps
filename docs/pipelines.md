# Pipelines + Secrets（Stage 1 基线 / Stage 2.0 契约）

本文是武陵 DevOps Pipelines（CI/CD）与 Secrets 子系统的设计与契约文档。它同时是
后端（Go 控制面）、Runner 客户端（Rust，`runners/runner-clients`）和前端三方共同遵守
的接口规范。

> 状态：Stage 1 基线已实装并进入 Stage 2.0 契约收口。语法风格对齐 **GitHub Actions**
> 的一个子集；Runner 与服务端走 **HTTP 长轮询 + 令牌**；Secrets 用 **AES-256-GCM**；
> 任务队列落在 **Postgres**。Kubernetes 后端**暂不实现**——按计划等自研 K8s 发行版
> （见“路线图”）。

---

## 1. 架构

沿用仓库既定的 **“core monolith + pipelines split”** 决策（见
`cmd/wuling-api/main.go`、`deploy/docker-compose.yml`）：

```text
                 ┌─────────────────────────── wuling-api (Go, 控制面) ───────────────────────────┐
   git push ───▶ │  事件 → 解析 .wuling/workflows/*.yml → 建 run/jobs/steps → 入队（Postgres）   │
                 │  Secrets（AES-GCM）  Runner 注册/心跳  Job 派发  日志落盘  Autoscaler 协调循环   │
                 └───────▲───────────────────────────────────────────────────────┬──────────────┘
                         │ HTTP 长轮询（runner token）                            │ 云 SDK（凭证取自 org Secret）
                         │  acquire / logs / complete / heartbeat                 ▼
              ┌──────────┴───────────┐                            ┌──────────────────────────────┐
              │ Runner 客户端 (Rust) │  ◀── 由 Autoscaler          │ 阿里云 ECS / AWS EC2 /        │
              │ 并发 worker + 容器    │      经 user-data 注入       │ Proxmox / VMware vCenter      │
              │ 运行时（Docker/Podman)│      token 后自举            │ （临时实例，用完按 idle 释放） │
              └──────────────────────┘                            └──────────────────────────────┘
```

- **控制面**（`wuling-api`）：解析工作流、维护队列与状态机、派发 job、收日志、跑 Autoscaler。
- **数据面**（Runner）：独立进程/独立机器，**绝不**与 `wuling-api` 同进程。可手动部署
  （static runner），也可由 Autoscaler 在云上临时拉起（ephemeral runner）。
- **不能全局**：Runner、Secrets、Autoscaler 配置一律 **org 级**。没有任何全局 runner 池。

---

## 2. 术语与数据模型

对齐 GitHub Actions 词汇：**workflow（工作流文件）→ run（一次执行）→ job → step**。

| 表 | 作用 |
|----|----|
| `secrets` | org / project 作用域的加密机密（AES-256-GCM 密文 + nonce）。 |
| `runners` | org 级注册的 runner（labels、`resource_tier`、provider、是否 ephemeral、外部实例 id、token 哈希）。 |
| `pipeline_runs` | 一个工作流文件针对某 commit/事件的一次执行（含解析快照，便于复跑）。 |
| `pipeline_jobs` | run 内的 job（`runs_on` 标签、`resource_tier`、`needs`、状态、被指派的 runner）。 |
| `pipeline_steps` | job 内的 step（编号、名称、状态、起止时间）。 |
| `pipeline_run_number_seq` | per-repo 的 run 序号分配器（UPSERT 行锁，模式同 `issue_number_seq`）。 |

Job 日志**落盘**（`<RepoRoot>/../pipeline-logs/<run_id>/<job_id>.log`，即
`WULING_PIPELINE_LOG_DIR`，默认 `./var/pipeline-logs`），与仓库/头像的磁盘存储一致；
DB 仅存 `log_size` 以支持范围拉取与 SSE 增量 tail。

状态机：`queued → running → success | failed | canceled`（job/step 同构）。run 的状态由其
job 汇总（任一失败则失败；全成功则成功）。

---

## 3. 工作流语法（`.wuling/workflows/*.yml`）

GitHub Actions 风格的**子集**，外加 `resource:` 档位扩展。示例：

```yaml
name: CI
on:
  push:
    branches: [main, "release/*"]
  pull_request:
  workflow_dispatch:            # 允许手动触发

jobs:
  build:
    runs-on: [linux, docker]    # 标签；runner 必须满足全部标签
    resource: medium            # 武陵扩展：low | medium | high（默认 medium）
    container: node:20          # step 在此镜像内执行；缺省用 runner 默认镜像
    env:
      CI: "true"
    steps:
      - name: Checkout
        uses: actions/checkout@v4     # Stage 1 仅特判 checkout（克隆到工作区）
      - name: Install & build
        run: |
          npm ci
          npm run build
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}   # 见 §4
  test:
    needs: [build]              # DAG 依赖；build 成功后才跑
    runs-on: [linux]
    resource: low
    steps:
      - run: npm test
```

**Stage 1 支持的字段**

- 顶层：`name`、`on`（`push.branches`、`pull_request`、`workflow_dispatch`）。
- `jobs.<id>`：`runs-on`（字符串或字符串数组）、`resource`、`container`、`needs`、`env`、`steps`。
- `steps[]`：`name`、`run`（shell 脚本）、`uses`（仅 `actions/checkout[@x]`）、`with`、`env`、
  `if`（仅 `always()` / `success()` / `failure()` 三种谓词，默认 `success()`）、`timeout-minutes`。

**校验**：`needs` 必须无环且指向同文件内已存在的 job；`resource` 必须是合法档位；标签非空。

`resource` 与标签的关系：`resource` 决定 Autoscaler 该用哪个 tier 的机器；`runs-on` 标签用于
把 job 派给“声明了这些标签”的 runner。Autoscaler 拉起的 runner 会自动带上
`tier:<low|medium|high>` 之外的池标签（见 §6）。

**OS 路由与执行**：runner 注册时按自身 OS 自动追加 `linux` / `windows` / `macos` 裸标签（沿用
`linux` 既有约定），所以 `runs-on: [windows]`、`[macos]` 直接命中对应机器，调度的标签匹配逻辑不变。
执行方式随 OS：Linux 始终在容器内（`sh -ec`）；Windows 缺省在宿主 `pwsh` 跑，job 声明 `container:`
时才用 Windows 容器；macOS 只在宿主 `bash` 跑（`container:` 被忽略，macOS 无容器）。

`${{ secrets.X }}` 与 `${{ env.X }}` 的插值**在 Runner 端**完成，机密通过已鉴权的
acquire 响应下发——明文绝不进 git，也不写进 run 的解析快照。

---

## 4. Secrets

- **加密**：AES-256-GCM。主密钥来自 `WULING_SECRETS_KEY`（32 字节，base64 或 hex），
  dev 下缺省自动生成临时密钥并告警（重启失效，与 OAuth HMAC 的处理一致）；**生产必须显式设置**。
- **作用域**：`org`（org 内所有 project 可见）与 `project`（仅该 project）。job 解析机密时，
  project 级覆盖同名 org 级。
- **可见性**：API 永不回吐明文。列表/读取只返回名字与元数据。明文只在两处解密：
  (a) job acquire 响应（下发给被授权的 runner）；(b) Autoscaler 取云凭证时（进程内，不出网）。
- **权限**：维护者及以上（maintainer+）可增删改；任何成员可列名字。
- **云凭证即 Secret**：`runner-config.yaml` 里 provider 的 `credentials_secret` 只写**名字**，
  真正的 AK/SK / API token 存在 org Secret 里。这样 GitOps 配置可以放心走 MR 评审。

**配置云访问密钥（入口）**：在 **org 设置 → Secrets** 页（或 `PUT /api/v1/orgs/{org}/secrets/{name}`，
需 maintainer+）新建一个 **org 级** Secret，名字与池里的 `credentials_secret` 一致，值为对应 provider
的凭证 JSON：

| Provider | Secret 值（JSON） |
|----|----|
| 阿里云 | `{"access_key_id":"…","access_key_secret":"…"}` |
| AWS | `{"access_key_id":"…","secret_access_key":"…","session_token":"…"（临时凭证可选）}` |

密钥用 AES-256-GCM 加密落库，API 永不回显（写入即只读）。**`credentials_secret` 留空会被 config
校验直接拒绝**；若引用的 Secret 缺失或 AK/SK 失效，Autoscaler 调云 API 会 401/403，该池拉不起实例。

---

## 5. Runner 协议（HTTP）

所有 runner 端点挂在 `/api/v1/runner`，用 **runner token**（前缀 `wlrt_`，argon2id 存哈希）鉴权，
走 `Authorization: Bearer wlrt_…`。

| 方法 & 路径 | 作用 |
|----|----|
| `POST /api/v1/runner/register` | 用**注册令牌**（org maintainer 在 UI 生成，短期）换取持久 runner token + runner id；上报 labels/tier/os/容量。 |
| `POST /api/v1/runner/heartbeat` | 续活（更新 `last_seen_at`、状态）。 |
| `POST /api/v1/runner/jobs/acquire` | 长轮询领取一个匹配 labels/tier 且 `needs` 已满足的 queued job；原子置为 running 并绑定 runner。返回 job + steps + 解密后的 secrets + 仓库检出信息（clone URL + 临时 token + sha）。 |
| `POST /api/v1/runner/jobs/{id}/logs` | 追加日志块（`?step=<n>` 可选），落盘。 |
| `PATCH /api/v1/runner/jobs/{id}` | 上报 step 级状态变更（开始/结束/结论）。 |
| `POST /api/v1/runner/jobs/{id}/complete` | 终结 job（success/failed/canceled），触发 run 汇总与下游 job 解锁。 |
| `POST /api/v1/runner/jobs/{id}/artifacts` | 上传 artifact（tar 流；Stage 1 落盘）。 |

派发用 `SELECT … FOR UPDATE SKIP LOCKED` 原子摘取，避免多 runner 抢同一 job。
控制面有 **reaper**：`running` 的 job 若其 runner 超过 `WULING_RUNNER_REAP_AFTER`（默认 90s）
未心跳，则该 job 重新入队（达到重试上限则判失败）。

---

## 6. 组织级 GitOps 配置（`config` 仓库 + `runner-config.yaml`）

**约定**：每个 org 维护一个 **config 仓库**——默认 `项目 slug = config`、`仓库 slug = config`
（即 `{org}/config/config`，二者均可用 `WULING_RUNNER_CONFIG_PROJECT` /
`WULING_RUNNER_CONFIG_REPO` 改名）。仓库默认分支根目录下的 `runner-config.yaml` 即该 org 的
Runner/Autoscaler 配置。控制面用 libgit2 直接读该 blob（带 TTL 缓存）。

把配置放进 git（而非全局 server 配置）满足三点：**组织级**、**可走 MR 评审**、**可审计**。
完整字段见 `runners/config/runner-config.example.yaml`。要点：

- `default_tier`：job 未声明 `resource:` 且 `runs-on` 无 `tier:*` 标签时的默认档位。
- `idle_timeout`（默认 `5m`）：**临时 runner 最近一次任务结束后空闲超过此时长**才释放。
  这是刻意为之——按量计费的 ECS/EC2 频繁起停既贵又慢（开机→注入 runner→注销很耗时），
  留一个空闲宽限能显著降本提速。
- `tiers.{low,medium,high}`：把抽象档位映射到具体 **CPU / 内存 / 存储容量**。
- `pools[]`：每个池绑定一个 `provider` + `tier`，带 `os`（`linux` 默认 / `windows`；macOS 仅手动、
  不可 autoscale）、`labels`、`min`、`max`，以及 provider 专属字段；云凭证以 `credentials_secret`
  （org Secret 名）引用。

---

## 7. Autoscaler

控制面内的一个协调 goroutine（`WULING_AUTOSCALER_ENABLED`，默认开），周期
`WULING_AUTOSCALER_INTERVAL`（默认 `20s`）对每个 org 执行 reconcile：

1. 读该 org `config` 仓库的 `runner-config.yaml`（缓存）。
2. 统计每个 (tier, labels) 维度上**当前没有合适在线 runner 承接**的 queued job 数。
3. **扩容**：匹配的 pool 若未达 `max`，调用 `Provider.Launch(spec)` 拉起实例，并通过 user-data
   注入「服务端地址 + 持久 runner token + labels/tier」，新机自举后上线。引导脚本随 `pool.os` 而变：
   Linux 走 cloud-init（写 `runner.env` + `systemctl enable --now`），Windows 走 `<powershell>`
   （写 `runner.env` + 计划任务 `schtasks /Run`，见 `BuildWindowsUserData`）；macOS 不参与扩容。
4. **缩容**：`ephemeral` 且 `provider==该池` 的 runner，若**空闲（无运行中 job）时长 > `idle_timeout`**
   且池内存活数 > `min`，调用 `Provider.Terminate(externalID)` 释放并删除 runner 行。
5. `min` 维持热备：池内存活不足 `min` 时补足（即便当前无排队）。

Provider 接口（`internal/autoscale`）：

```go
type Provider interface {
    Name() string
    Launch(ctx context.Context, spec LaunchSpec) (InstanceRef, error)
    Terminate(ctx context.Context, externalID string) error
}
```

四个 provider（`internal/autoscale`）：

- **阿里云 ECS** 与 **AWS EC2**：**已完整实现**。用各自的签名 REST API 直连（AWS SigV4、
  Aliyun HMAC-SHA1 RPC 签名），不引入庞大的 vendor SDK，保持 `wuling-api` 二进制依赖精简。
  这两个正是你最在意的按量计费场景。user-data 直接走实例的 user-data 字段注入 runner token。
- **Proxmox VE** 与 **VMware vCenter**：接口已就位、配置校验完整，但 VM 置备**暂为占位**
  （`Launch`/`Terminate` 返回明确错误）。原因：Proxmox 注入原始 cloud-init user-data 需要
  snippets 存储（无一等 API），vCenter 需要 govmomi/SOAP + guest 定制——都依赖部署环境且无法
  无凭证联测。建议与自研 K8s provider 一并补齐（见“路线图”）。

> ⚠️ 若 `runner-config.yaml` 配置了 **Proxmox / vCenter** 池：`NewProvider` 在为该池构建 provider
> 时即返回 “not supported” 错误，Autoscaler 记录告警并**跳过该池**（不影响 aws/aliyun 池），在置备
> 补齐前不会拉起任何实例。

每个从 `LaunchSpec`（tier→CPU/内存/存储、镜像/模板、网络、注入脚本）创建一台临时机。
凭证从 org Secret（`credentials_secret`）解密注入，不落盘、不出网。

> 注意：云 provider 的真实调用需各自的账号/镜像模板/网络资源，**无法在本地无凭证集成测试**。
> 仓库内附带的是契约 + 单元测试（config 解析/校验、调度数学、签名拼装）；真机验证需在目标云上做。

### 7.1 自定义镜像 / 模板（预装 runner）

池里的 `image_id`（阿里云）/ `ami`（AWS）必须指向一个**预装好 runner 运行时**的镜像。Autoscaler
拉起实例后只通过 user-data 注入 token 等少量变量并启动 runner，**不会**在启动时联网安装任何东西
（既慢又脆）。

**用预构建二进制，别在镜像里装工具链。** 每次发版（`v*` tag）的 GitHub Release 都附带各平台的
`wuling-runner-<os>-<arch>`：Linux 是**静态 musl**（零 libc/openssl 依赖，镜像最省）、Windows 是
`.zip`、macOS 是 `.tar.gz`。镜像准备脚本只**下载**它、不编译——见 `runners/images/<os>/setup.*`，
设好 `WULING_RUNNER_VERSION=<tag>` 即可。三种 OS 都**不要**把任何 token 烤进镜像，token 由
user-data 逐机注入。

**Linux（容器执行）** — `runners/images/linux/setup.sh`：
1. **容器运行时 + git**：Docker 或 Podman，外加 `git`（checkout 走宿主机 git）。
2. **`wuling-runner`**（静态 musl）装进 `/usr/local/bin`。
3. **systemd 单元 `wuling-runner.service`**，`EnvironmentFile=/etc/wuling-runner/runner.env`、
   `ExecStart=/usr/local/bin/wuling-runner`、`Restart=on-failure`。单元由 user-data 在每台实例首启时
   `systemctl enable --now`，不要在镜像里 enable。

**Windows（Server 2022+）** — `runners/images/windows/setup.ps1`：
1. **`wuling-runner.exe`** 装进 `C:\wuling-runner\`。
2. **可选 Docker**：仅当要跑声明了 `container:` 的 job（Windows 容器）才需要；缺省走宿主 `pwsh`
   执行，无需 Docker。⚠️ Windows Docker daemon 的 **Windows-容器模式与 Linux-容器模式互斥**，同一台机
   同时只能跑一种。
3. **计划任务 `wuling-runner`**（以 SYSTEM 运行）——runner 是普通控制台程序、不是 SCM 服务，用计划任务
   可零第三方依赖（无需 NSSM/WinSW）。任务跑一个 `run.cmd` 包裹脚本，先加载
   `C:\ProgramData\wuling-runner\runner.env` 再启动 runner。user-data 写完 runner.env 后用
   `schtasks /Run /TN wuling-runner` 触发（见 `internal/autoscale/cloudinit.go`）。

**macOS（仅手动注册物理机）** — `runners/images/macos/setup.sh`：macOS **不进 autoscaler**（Apple
授权要求跑在 Apple 硬件上），也**没有容器**——step 一律在宿主 `bash` 直接执行。脚本装好
`/usr/local/bin/wuling-runner` 并给出一个 launchd `LaunchDaemon` 模板；填入 server URL + 注册令牌后
`launchctl load` 即可。

Autoscaler 注入的 `runner.env`（Linux 在 `/etc/wuling-runner/`、`chmod 600` root-only；Windows 在
`C:\ProgramData\wuling-runner\`、`icacls` 限 Administrators/SYSTEM；见 `internal/autoscale/cloudinit.go`）：

| 变量 | 值 |
|----|----|
| `WULING_RUNNER_SERVER_URL` | 控制面对外地址（取自 `WULING_OAUTH_PUBLIC_BASE_URL`） |
| `WULING_RUNNER_TOKEN` | 该实例专属的持久 runner token（`wlrt_…`，Autoscaler 预先建好） |
| `WULING_RUNNER_NAME` | Autoscaler 生成的 runner 名 |
| `WULING_RUNNER_LABELS` | 池的 `labels` |
| `WULING_RUNNER_CONCURRENCY` | 固定 `1`（一机一并发，便于按需伸缩） |

runner 二进制还认 `WULING_RUNNER_OS`（默认取构建目标：win/mac 构建自识别）、`WULING_RUNNER_DEFAULT_IMAGE`
（容器执行且 job 未声明 `container:` 时的默认镜像）、`WULING_RUNNER_WORK_DIR`、`WULING_RUNNER_POLL_INTERVAL`
等开关（`wuling-runner --help` 有全量列表）。**手动注册的 static runner** 同理备机，只是改用
`--registration-token`（UI 生成）换取 token，而非由 Autoscaler 注入。

---

## 8. 安全考量

- Runner token / 注册令牌：随机 32B，argon2id 存哈希（同 PAT）；注册令牌短期且一次性。
- Secrets 主密钥仅在内存；密文 + nonce 落库；GCM 提供机密性 + 完整性。
- 工作流插值在 runner 端做，明文机密不进 git、不进解析快照、不进普通 API 响应。
- 派发给 runner 的仓库检出 token 为**最小权限、短期**（仅该 repo 读）。
- 云凭证以 Secret 名引用，GitOps 配置可公开评审。

---

## 9. 配置项（env）

| 变量 | 默认 | 说明 |
|----|----|----|
| `WULING_SECRETS_KEY` | （dev 自动生成） | Secrets/凭证主密钥；32B，base64 或 hex。生产必填。 |
| `WULING_PIPELINE_LOG_DIR` | `./var/pipeline-logs` | job 日志落盘目录。 |
| `WULING_RUNNER_CONFIG_PROJECT` | `config` | 组织 config 仓库所在 project slug。 |
| `WULING_RUNNER_CONFIG_REPO` | `config` | 组织 config 仓库 slug。 |
| `WULING_RUNNER_REGISTRATION_TTL` | `1h` | 注册令牌有效期。 |
| `WULING_RUNNER_REAP_AFTER` | `90s` | runner 失联多久后回收其 running job。 |
| `WULING_AUTOSCALER_ENABLED` | `true` | 是否启用 Autoscaler 协调循环。 |
| `WULING_AUTOSCALER_INTERVAL` | `20s` | reconcile 周期。 |
| `WULING_AUTOSCALER_DEFAULT_IDLE_TIMEOUT` | `5m` | `runner-config.yaml` 未声明 `idle_timeout` 时的回退值。 |

---

## 10. Stage 2+ 路线图 / 暂未实现

- **Kubernetes provider**：按计划等自研 K8s 发行版后再加（同 `Provider` 接口即可接入）。
- **Proxmox / vCenter 置备**：接口与配置已就位，VM 置备待补（Proxmox 需 snippets 存储注入
  cloud-init；vCenter 需 govmomi + guest 定制）。与 K8s provider 一并推进。
- **多 OS runner**：**已实现**——Windows（宿主 `pwsh` 与 Windows 容器两种执行模型，AWS EC2 / 阿里云
  ECS 可 autoscale，最低 Server 2022）、macOS（手动注册物理机，宿主执行）。详见 §3 / §7 / §7.1。
- **matrix**（`strategy.matrix` + `include`/`exclude` + `fail-fast`/`max-parallel`）：下一切片。
- step 级 `uses` 第三方 action 生态、可复用 workflow、环境/审批门禁 → Stage 2+。
- 日志/artifact 转对象存储（当前落盘）。
- 基于 WebSocket 的实时日志推送（当前 SSE + 轮询）。
