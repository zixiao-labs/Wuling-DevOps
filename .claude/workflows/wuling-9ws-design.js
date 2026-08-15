export const meta = {
  name: 'wuling-9ws-design',
  description: 'Deep-read Wuling-DevOps subsystems across 9 workstreams and produce verified implementation designs',
  phases: [
    { title: 'Investigate', detail: 'parallel deep-readers, one per subsystem' },
    { title: 'Critique', detail: 'adversarial check of each design against real code' },
  ],
}

const ROOT = '/Users/logos/WebstormProjects/Wuling-DevOps'

const DESIGN_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['area', 'currentState', 'filesToTouch', 'design', 'risks', 'openQuestions'],
  properties: {
    area: { type: 'string' },
    currentState: { type: 'string', description: 'What exists today, with file:line citations' },
    filesToTouch: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['path', 'change'],
        properties: {
          path: { type: 'string' },
          change: { type: 'string', description: 'new | edit — and what specifically' },
        },
      },
    },
    design: { type: 'string', description: 'Concrete implementation design: signatures, schema, wire format, YAML shape. Include code sketches.' },
    risks: { type: 'array', items: { type: 'string' } },
    openQuestions: { type: 'array', items: { type: 'string' } },
  },
}

const CRITIQUE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['verdict', 'problems', 'corrections'],
  properties: {
    verdict: { enum: ['SOUND', 'NEEDS_FIX'] },
    problems: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['claim', 'evidence'],
        properties: {
          claim: { type: 'string' },
          evidence: { type: 'string', description: 'file:line proving the design is wrong or would not compile/work' },
        },
      },
    },
    corrections: { type: 'string', description: 'Corrected design details, or empty if SOUND' },
  },
}

const COMMON = `You are reading the Go+Rust+React monorepo at ${ROOT} (Wuling-DevOps: a self-hosted GitLab/GitHub-alike).
Read the ACTUAL files — never guess at APIs, signatures, or table columns. Cite file:line for every claim about current state.
Repo conventions: Go control plane in internal/*, Rust runner in runners/runner-clients/src/*, React 19 + HeroUI v3 + chen-the-dawnstreak router + Nasti bundler in frontend/.
Comments and user-facing strings in this repo are Chinese for UI, English for code comments. Match that.
Your output is a DESIGN for another agent to implement — be concrete enough that they need not re-derive anything.`

const AREAS = [
  {
    key: 'matrix',
    prompt: `${COMMON}

AREA: strategy/matrix CI syntax.
Read internal/pipeline/workflow.go, spec.go, discover.go, workflow_test.go; internal/pipelinestore/*.go (esp. run/job creation and the 'definition' JSONB); internal/pipelinetrigger/trigger.go; docs/pipelines.md §3.
Design GitHub-Actions-compatible \`strategy:\` on a job: \`matrix\` (named axes -> value lists), \`matrix.include\`, \`matrix.exclude\`, \`fail-fast\` (default true), \`max-parallel\`.
Nail down: (a) the parsed Go structs + YAML unmarshalling (matrix values can be strings/numbers/bools/objects — decide the value type), (b) the expansion algorithm producing one job per combination with a deterministic name suffix like \`build (ubuntu, 18)\`, (c) where expansion happens (parse time vs run-creation time) given jobs are persisted with a JobSpec snapshot, (d) how \`\${{ matrix.X }}\` interpolates and WHERE (server-side into JobSpec, or runner-side alongside secrets/env — check internal/runnerhttp + runners/runner-clients/src/executor.rs for how interpolation works today), (e) how \`needs:\` resolves when the needed job is a matrix (all combinations must finish), (f) fail-fast cancellation and max-parallel throttling given the SELECT..FOR UPDATE SKIP LOCKED dispatcher, (g) DB migration needs (check internal/db for the migration pattern and whether pipeline_jobs needs new columns).
Report the exact new/changed Go types and function signatures.`,
  },
  {
    key: 'setup-actions',
    prompt: `${COMMON}

AREA: built-in actions in the Rust runner (setup-node, setup-rust, plus what Zed and Vite need).
Read ALL of runners/runner-clients/src/*.rs (executor.rs, backend.rs, api.rs, config.rs, main.rs) — especially how \`uses:\` is dispatched (executor.rs ~line 210), how steps run on linux/windows/macos backends, workspace layout, and env propagation between steps.
Also read /Users/logos/WebstormProjects/zed/rust-toolchain.toml, /Users/logos/WebstormProjects/zed/Cargo.toml (workspace), /Users/logos/WebstormProjects/zed/script/ or ci/ if present, and /Users/logos/WebstormProjects/vite/package.json + pnpm-workspace.yaml — to determine EXACTLY what a build of each needs (toolchain versions, targets, system packages, package manager, caching keys).
Design: \`actions/setup-node@v4\` (with: node-version, cache: npm|pnpm|yarn, registry-url), \`actions/setup-rust\` or \`dtolnay/rust-toolchain\` (with: toolchain, targets, components, cache), and any additional built-ins needed for Zed (e.g. system deps on linux) and Vite (corepack/pnpm).
CRITICAL: these must work on all three backends. Decide download sources (nodejs.org dist, rustup), checksum verification, install location inside the workspace vs a shared tool cache dir, and how PATH mutation persists across steps (check whether the executor keeps env between steps — cite the code).
Report exact Rust function signatures, new modules, and the \`with:\` input contract.`,
  },
  {
    key: 'aliyun-autoscale',
    prompt: `${COMMON}

AREA: Aliyun ECS autoscaling for Linux AND Windows + resource limits.
Read internal/autoscale/*.go in full (config.go, aliyun.go, aws.go, cloudinit.go, reconcile.go, provider.go, autoscale_test.go) and runners/config/runner-config.example.yaml and docs/pipelines.md §6/§7.
KNOWN BUG to confirm and fix: cloudinit.go BuildWindowsUserData wraps the script in \`<powershell>\` — that is AWS EC2Launch v2 / cloudbase-init syntax. Alibaba Cloud ECS Windows user-data requires a \`[powershell]\` (or \`[bat]\`) first-line prefix instead. Verify against the current Aliyun ECS user-data documentation and design the fix so AWS keeps \`<powershell>\` and Aliyun gets \`[powershell]\` (i.e. user-data rendering must become provider-aware, not just OS-aware).
Also design: (a) Aliyun Windows instance specifics the RunInstances call is missing (Password / KeyPairName, SystemDisk size+category, DataDisk, InstanceChargeType, spot params, tags), (b) RESOURCE LIMITS — map the runner-config \`tiers.{cpu,memory,storage}\` onto actual Aliyun params (SystemDisk.Size for storage; and whether to select InstanceType by cpu/memory via DescribeInstanceTypes rather than a hardcoded instance_type), plus per-job container resource limits (check whether the Rust runner passes --cpus/--memory to docker — cite backend.rs), (c) error handling / retry for RunInstances throttling.
Report exact struct field additions and function signature changes.`,
  },
  {
    key: 'gitops-write',
    prompt: `${COMMON}

AREA: server-side GitOps write-back — an API that commits runner-config.yaml into the org's config repo.
Read internal/repohttp/handler.go (all of it), internal/git/git.go (esp. CommitFile, DeleteFile, Resolve, ReadBlob signatures), internal/git/git_stub.go, internal/repostore/layout.go, internal/secrethttp/handler.go (for the auth/permission pattern), internal/auth/middleware.go + roles*.go (permission helpers), internal/httpapi (response helpers), internal/apperr, and internal/autoscale/config.go (the schema being written). Also check how the autoscaler LOADS runner-config today (grep reconcile.go / server.go for the config repo lookup + TTL cache) so a write can invalidate it.
Design: (a) a generic repo file write endpoint OR a purpose-built \`GET/PUT /api/v1/orgs/{org}/runner-config\` endpoint — recommend one and justify; (b) request/response JSON shape; (c) permission (maintainer+); (d) optimistic concurrency (base commit sha / If-Match) so two editors don't clobber; (e) validation — must reject a config that autoscale.Parse rejects, BEFORE committing; (f) auto-create the config project+repo if missing (check orghttp/repohttp create paths for how projects/repos are created programmatically); (g) cache invalidation after commit; (h) the commit author signature (which user).
Report exact handler signatures, route registration (chi), and the OpenAPI additions needed in api/openapi.yaml.`,
  },
  {
    key: 'autoscale-ui',
    prompt: `${COMMON}

AREA: frontend UI for graphical elastic-scaling + resource-limit configuration (writes to GitOps).
Read frontend/src/pages/orgs/[org_slug]/runners.tsx and secrets.tsx in FULL; frontend/src/components/page/primitives.tsx + badges.tsx + data-list.tsx; frontend/src/components/shell/*.tsx; frontend/src/api/client.ts, endpoints.ts, types.ts (patterns for adding an endpoint + types); frontend/src/components/error-banner.tsx, loading.tsx, empty-state.tsx.
Note which @heroui/react v3 components the codebase ALREADY uses and how (compound components like Modal/TextField/Select). Do NOT invent component APIs — list exactly which HeroUI v3 components the design needs so the implementer can look them up via the heroui-react MCP server before writing code.
Design a Runners page redesign or a new sibling tab: a form-driven editor for runner-config.yaml covering default_tier, idle_timeout, tiers.{low,medium,high}.{cpu,memory,storage}, and a pools[] list editor (name, provider aliyun|aws|proxmox|vcenter, tier, os linux|windows, labels, min, max, credentials_secret + provider-specific fields). Requirements: live YAML preview, validation before save, save = PUT to the GitOps endpoint (designed by a sibling agent — assume \`GET/PUT /api/v1/orgs/{org}/runner-config\` returning {yaml, sha} and accepting {yaml, base_sha}), conflict handling, and an empty state when the config repo does not exist yet.
Report the component tree, new files, endpoint/type additions, and the exact list of HeroUI components required.`,
  },
  {
    key: 'github-webhook',
    prompt: `${COMMON}

AREA: GitHub webhook ingress -> trigger pipelines + auto-sync (mirror) repos. The instance authenticates users via a GitHub App (see docs/auth.md and internal/config/config.go WULING_OAUTH_GITHUB_*).
Read docs/auth.md in FULL, internal/config/config.go, internal/oauthhttp/*.go (esp. how the GitHub OAuth start/callback works — find the file that handles github), internal/authhttp/, internal/githttp/handler.go (the PushTrigger interface + RefUpdate), internal/pipelinetrigger/trigger.go, internal/pipeline/discover.go, internal/server/*.go (route wiring), internal/repostore/layout.go, internal/git/git.go (what clone/fetch primitives exist — does the C layer expose fetch/remote at all? check wuling_git.h).
Design: (a) \`POST /api/v1/webhooks/github\` — HMAC-SHA256 \`X-Hub-Signature-256\` verification against a per-repo or per-org webhook secret, \`X-GitHub-Event\` dispatch (ping, push, pull_request, installation, installation_repositories), replay/delivery-id dedup; (b) mapping a GitHub repo to a Wuling repo (new table? new columns on repos? check internal/db migrations pattern and internal/model); (c) on push: fetch/mirror the new commits into the Wuling repo then run the SAME workflow-discovery + CreateRun path as pipelinetrigger — note whether git.go can fetch from a remote at all, and if not, design the minimum C/libgit2 addition or a shelling-out fallback; (d) GitHub App auth for the mirror pull (installation token via JWT signed with the App private key — design the token minting + caching, and the new config env vars); (e) EXACT GitHub App settings the operator must change: which Permissions (Contents: Read, Metadata: Read, Pull requests, Webhooks), which Subscribe-to-events checkboxes, the Webhook URL + secret field, and Callback URL — write this as doc-ready Chinese prose.
Report table/migration DDL, Go types, handler signatures, and config env additions.`,
  },
  {
    key: 'help-center-ssr',
    prompt: `${COMMON}

AREA: a help-center documentation site — SSR'd, alongside the existing CSR app.
CONFIRMED: chen-the-dawnstreak 4.2.2 exports "./ssr" (createHTMLShell / renderToStream / renderToHTML, StaticRouter re-exported as ChenSSRRouter) and Nasti 2.2 has client+ssr environments with ssrLoadModule. Verify by reading frontend/node_modules/chen-the-dawnstreak/dist/ssr/index.js and .d.ts, frontend/node_modules/chen-the-dawnstreak/dist/vite-plugin/index.js (how the routes:true file-router works and whether it can emit a server route manifest), and the Nasti SOURCE at /Users/logos/WebstormProjects/Nasti/src/build/index.ts + src/server/index.ts + src/config/index.ts + src/types.ts to learn exactly how to configure an SSR build (environments, build.ssr, rollupOptions input, manifest output).
Also read frontend/nasti.config.ts, frontend/index.html, frontend/src/main.tsx, frontend/src/pages/_layout.tsx, deploy/production/Dockerfile.frontend, deploy/production/Caddyfile, deploy/production/docker-compose.prod.yml, deploy/production/nginx-frontend.conf.
Design: a \`/help/*\` documentation site — markdown-sourced pages (check frontend/src/components/markdown.tsx for the existing renderer), sidebar nav + search, SSR-rendered for SEO/first paint, while the rest of the app stays CSR/static. Cover: (a) build wiring (a second Nasti build in the ssr environment producing a Node entry), (b) a tiny Node SSR server (which framework — plain node:http? check what deps exist), (c) how markdown docs are authored & bundled, (d) the Docker + Caddy topology change: Caddy routes /help/* to the SSR container and everything else to static, (e) graceful degradation if the SSR container is down.
If after reading you conclude in-repo SSR is NOT actually workable, say so explicitly with evidence and design a standalone Next.js container instead.
Report exact file list, config diffs, Dockerfile, and Caddyfile changes.`,
  },
  {
    key: 'runner-images',
    prompt: `${COMMON}

AREA: runner image provisioning — Windows dual init (Inno Setup installer + PowerShell script) with auto-start-on-boot, and Linux auto-start-on-boot.
Read runners/images/linux/setup.sh, runners/images/windows/setup.ps1, runners/images/macos/setup.sh in FULL; internal/autoscale/cloudinit.go; docs/pipelines.md §7.1; runners/runner-clients/Cargo.toml + src/main.rs + src/config.rs (what env vars / CLI flags the binary accepts, and whether it can run as a Windows service); and .github/workflows/* (how release artifacts wuling-runner-<os>-<arch> are built and published — check the release workflow so the Inno Setup script can be built in CI too).
Design: (a) an Inno Setup .iss script that installs wuling-runner.exe to Program Files, registers the Scheduled Task (or a service) for auto-start at boot, optionally prompts for server URL + registration token and writes C:\\ProgramData\\wuling-runner\\runner.env, includes an uninstaller, and is silent-installable (/VERYSILENT /SERVERURL=... /TOKEN=...) for image baking; (b) the PowerShell path kept as the scriptable alternative, with the two sharing one source of truth for paths/task definition; (c) CI job to build the installer with Inno Setup 6 on a windows runner and attach it to the release; (d) Linux: make setup.sh able to enable auto-start (a --enable/--static flag or a separate enable script) covering BOTH systemd and non-systemd (OpenRC) hosts, without baking tokens into the image; (e) how autoscaled instances differ from static ones in each path.
Verify the current Inno Setup version and the correct directives against real docs before writing the .iss.
Report full file list and the key script contents.`,
  },
  {
    key: 'logo-branding',
    prompt: `${COMMON}

AREA: the missing-logo bug after Docker deploy, using the new logo at ${ROOT}/assets/wuling-logo-3.svg (and .png, 1024x1024).
Read frontend/index.html, frontend/nasti.config.ts (the PWA icons block), frontend/public/ (list it), frontend/src/components/shell/app-shell.tsx (the BrandMark component ~line 163), frontend/src/pages/login.tsx and register.tsx (any brand marks there), deploy/production/Dockerfile.frontend, deploy/production/nginx-frontend.conf, deploy/production/Caddyfile, deploy/Dockerfile.
CONFIRMED BUG: nasti.config.ts PWA config references /icon-192.png and /icon-512.png but frontend/public/ contains ONLY favicon.svg — so the manifest points at 404s. Confirm this and find EVERY other place a brand asset is referenced or should be.
Design the fix: which asset files to generate from wuling-logo-3.svg (favicon.svg, favicon.ico?, icon-192.png, icon-512.png, maskable icon, apple-touch-icon-180.png, og-image), exactly where they live so Nasti copies them into dist/, the index.html <link> tags (icon, apple-touch-icon, og:image, theme-color), the manifest entries (including a maskable purpose entry), and how BrandMark should render the real logo (inline SVG component vs <img src>) while still respecting light/dark themes.
ALSO: verify whether the Docker build actually copies frontend/public into the image — trace COPY frontend/ ./ in Dockerfile.frontend and any .dockerignore/.gitignore that might exclude it (read ${ROOT}/.gitignore and frontend/.gitignore — a public/*.png ignore rule would be the real root cause). Report findings with citations.
Report the exact file list, the ImageMagick/rsvg commands to generate rasters from the SVG, and every code diff.`,
  },
]

phase('Investigate')

const results = await pipeline(
  AREAS,
  (a) => agent(a.prompt, { label: `design:${a.key}`, phase: 'Investigate', schema: DESIGN_SCHEMA }),
  (design, area) => {
    if (!design) return null
    return agent(
      `${COMMON}

You are an adversarial reviewer. Another agent produced this implementation design for area "${area.key}". Your job is to REFUTE it by checking every factual claim against the real code at ${ROOT}.

DESIGN UNDER REVIEW:
${JSON.stringify(design, null, 2)}

Check specifically:
- Does every cited file:line actually exist and say what is claimed? Open them.
- Would the proposed Go/Rust code actually compile against the real signatures in the repo (correct package names, existing helper functions, correct struct fields)?
- Does it contradict an existing convention, test, or the CGO/stub split in internal/git?
- Are proposed DB columns/tables consistent with the real migration pattern in internal/db?
- For frontend designs: does it use HeroUI v3 components that actually exist, and follow the repo's existing import/style patterns?
- Does it miss a place that must also change (route wiring in internal/server, OpenAPI spec, docs/, tests)?
Default to finding problems. If after real verification the design is genuinely sound and complete, say SOUND.
When you find a problem, supply the CORRECTED detail so the implementer can use it.`,
      { label: `critique:${area.key}`, phase: 'Critique', schema: CRITIQUE_SCHEMA },
    ).then((c) => ({ area: area.key, design, critique: c }))
  },
)

const out = results.filter(Boolean)
log(`designs complete: ${out.map((r) => `${r.area}=${r.critique?.verdict ?? '?'}`).join(', ')}`)
return out
