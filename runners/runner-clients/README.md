# wuling-runner

The Wuling DevOps CI runner client. It registers with the control plane,
long-polls for jobs, and executes each job's steps inside a container or on
the host shell (macOS / Windows without `container:`).

See `docs/pipelines.md` for the protocol and the overall architecture.

## Build

```sh
cargo build --release
# binary: target/release/wuling-runner
```

## Requirements on the host

- A container runtime exposing the Docker API (Docker or Podman with the
  Docker-compatible socket). `bollard` connects via the local default socket,
  or `DOCKER_HOST` if set. Required on Linux (always containerized) and on
  Windows when jobs declare `container:`.
- `git` on PATH (used to check out repositories for `actions/checkout`).
- Outbound HTTPS to `nodejs.org`, `static.rust-lang.org`, and
  `registry.npmjs.org` (built-in `setup-node` / `setup-rust` download
  toolchains on the host).
- Two persistent directories (defaults under `--work-dir`):
  `_tools` (immutable tool dists, read-only in containers) and
  `_toolstate` (mutable caches: `CARGO_HOME`, pnpm/npm stores).

## Run

Register-and-run with a one-time registration token (minted by an org
maintainer in the UI, or injected by the autoscaler via cloud-init):

```sh
wuling-runner \
  --server-url https://wuling.example.com \
  --registration-token wlreg_xxx \
  --labels linux,docker \
  --concurrency 2
```

Or run with an existing persistent runner token (`wlrt_…`):

```sh
WULING_RUNNER_SERVER_URL=https://wuling.example.com \
WULING_RUNNER_TOKEN=wlrt_xxx \
wuling-runner
```

All flags have `WULING_RUNNER_*` env equivalents (run `wuling-runner --help`).

## Install / upgrade (multi-OS)

Release artifacts (see `docs/RELEASE.md`):

| Platform | Artifact |
|----------|----------|
| Linux amd64/arm64 | `wuling-runner-linux-<arch>.tar.gz` (static musl) |
| macOS amd64/arm64 | `wuling-runner-darwin-<arch>.tar.gz` |
| Windows amd64 | `wuling-runner-windows-amd64.zip` **or** `wuling-runner-windows-amd64-setup.exe` |

Verify with the adjacent `.sha256` file before installing.

**Linux / macOS image bake:** set `WULING_RUNNER_VERSION=<tag>` and run
`runners/images/<os>/setup.*` — downloads the release binary, no Rust toolchain.

**Windows (static machine):**

1. Download `wuling-runner-windows-amd64-setup.exe` from the GitHub Release.
2. Run the installer (admin). Default install dir: `C:\wuling-runner\`.
   Optionally register the `wuling-runner` Scheduled Task (AtStartup / SYSTEM).
3. Mint a registration token in the org UI → Runners.
4. Write `C:\ProgramData\wuling-runner\runner.env`:

   ```
   WULING_RUNNER_SERVER_URL=https://wuling.example.com
   WULING_RUNNER_REGISTRATION_TOKEN=wlreg_...
   ```

5. Start: `schtasks /Run /TN wuling-runner`

**Upgrade:** re-run the newer setup.exe over the same directory (Inno Setup
overwrites `wuling-runner.exe` + `run.cmd`). Re-registering the scheduled task
is idempotent. Autoscaled Windows images keep using
`runners/images/windows/setup.ps1` (zip download), not the GUI installer.

The Inno Setup source lives at `runners/packaging/windows/wuling-runner.iss`.

## Behaviour notes

- **Concurrency**: `--concurrency N` runs N jobs in parallel, each in its own
  container and workspace.
- **Tiers/labels**: the control plane only dispatches a job to a runner whose
  `resource_tier` matches and whose labels are a superset of the job's
  `runs-on`. Tier/labels are fixed at registration.
- **Checkout**: `uses: actions/checkout` clones the repo at the dispatched
  commit using this runner's own token (read-only, scoped to its org). The
  token is redacted from logs.
- **Setup actions**: `actions/setup-node`, `pnpm/action-setup`, and
  `actions/setup-rust` (plus `dtolnay/rust-toolchain` /
  `actions-rust-lang/setup-rust-toolchain` aliases) provision toolchains into
  the shared tool cache and export PATH/env for later steps.
- **Secrets**: org/project secrets are injected as environment variables into
  every `run` step's container.
- **Graceful shutdown**: on SIGTERM/SIGINT the runner stops acquiring new jobs
  and lets in-flight jobs finish before exiting — this is what lets the
  autoscaler reclaim an idle ephemeral runner without killing live work.
