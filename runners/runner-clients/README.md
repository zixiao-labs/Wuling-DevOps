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
