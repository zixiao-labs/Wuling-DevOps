# Wuling DevOps - one-shot local dev launcher (PowerShell).
# Mirrors dev-server.sh: Postgres in Docker + wuling-api + nasti frontend,
# with Ctrl+C cleanup. Postgres is intentionally left running on exit so
# the next launch is fast.

$ErrorActionPreference = "Stop"

Write-Host "Wellcome Wuling"

Write-Host "██╗    ██╗██╗   ██╗██╗     ██╗███╗   ██╗ ██████╗     ██████╗ ███████╗██╗   ██╗ ██████╗ ██████╗ ███████╗"
Write-Host "██║    ██║██║   ██║██║     ██║████╗  ██║██╔════╝     ██╔══██╗██╔════╝██║   ██║██╔═══██╗██╔══██╗██╔════╝"
Write-Host "██║ █╗ ██║██║   ██║██║     ██║██╔██╗ ██║██║  ███╗    ██║  ██║█████╗  ██║   ██║██║   ██║██████╔╝███████╗"
Write-Host "██║███╗██║██║   ██║██║     ██║██║╚██╗██║██║   ██║    ██║  ██║██╔══╝  ╚██╗ ██╔╝██║   ██║██╔═══╝ ╚════██║"
Write-Host "╚███╔███╔╝╚██████╔╝███████╗██║██║ ╚████║╚██████╔╝    ██████╔╝███████╗ ╚████╔╝ ╚██████╔╝██║     ███████║"
Write-Host " ╚══╝╚══╝  ╚═════╝ ╚══════╝╚═╝╚═╝  ╚═══╝ ╚═════╝     ╚═════╝ ╚══════╝  ╚═══╝   ╚═════╝ ╚═╝     ╚══════╝"

Write-Host "Development Environment Requirements"
Write-Host "Node.js: 24"
Write-Host "Golang: 1.25"
Write-Host "Docker Desktop (with compose v2)"
Write-Host "nix (optional)"
Write-Host "Please ensure that it is installed and that your office is not on Terra or Talos-II, otherwise some bad things may happen (such as undefined behavior, startup failure, unexplained bugs)."

$answer = Read-Host "Are you sure you want to continue? [Y/n]"
# Empty input defaults to "yes" so the prompt matches the [Y/n] convention.
if ($answer -eq "n" -or $answer -eq "N") {
    Write-Host "Aborting."
    exit 1
}

# Anchor everything to the script directory so relative paths work no matter
# where the user launched PowerShell from.
$repoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $repoRoot

# $IsWindows is a built-in in PowerShell 7+; on Windows PowerShell 5.1 it
# doesn't exist, so fall back to $env:OS.
$onWindows = if ($null -ne $IsWindows) { $IsWindows } else { $env:OS -eq "Windows_NT" }

# npm ships as a .cmd shim on Windows; Start-Process needs the explicit name
# because PATHEXT resolution isn't applied to -FilePath in PS 5.1.
$npmExe = if ($onWindows) { "npm.cmd" } else { "npm" }

foreach ($cmd in @("go", "node", $npmExe, "docker")) {
    if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
        Write-Error "missing dependency: $cmd - install it and retry."
        exit 1
    }
}

# Docker on its own isn't enough — we use `docker compose` (v2) below, and
# v1's `docker-compose` standalone binary has a different invocation. Fail
# fast with a clear message if v2 isn't available.
$composeOutput = & docker compose version 2>&1
if ($LASTEXITCODE -ne 0 -or -not ($composeOutput -match "v2|version 2")) {
    Write-Error "Docker Compose v2 not available (got: $composeOutput). Install Docker Desktop with Compose v2 and retry."
    exit 1
}

if (-not $env:WULING_ENV)        { $env:WULING_ENV        = "dev" }
if (-not $env:WULING_HTTP_ADDR)  { $env:WULING_HTTP_ADDR  = ":8080" }
if (-not $env:WULING_DB_DSN)     { $env:WULING_DB_DSN     = "postgres://wuling:wuling@localhost:5432/wuling?sslmode=disable" }
if (-not $env:WULING_REPO_ROOT)  { $env:WULING_REPO_ROOT  = Join-Path $repoRoot "var/repos" }
if (-not $env:WULING_JWT_SECRET) { $env:WULING_JWT_SECRET = "dev-only-not-a-real-secret" }
if (-not $env:WULING_LOG_FORMAT) { $env:WULING_LOG_FORMAT = "text" }

if ($env:WULING_JWT_SECRET -eq "dev-only-not-a-real-secret") {
    Write-Warning "using dev-only-not-a-real-secret default JWT secret - for local development only; override WULING_JWT_SECRET in staging/CI/prod"
}

New-Item -ItemType Directory -Force -Path $env:WULING_REPO_ROOT | Out-Null

Write-Host "-> starting postgres (docker compose)..."
docker compose -f deploy/docker-compose.yml up -d postgres
if ($LASTEXITCODE -ne 0) {
    Write-Error "docker compose failed to start postgres."
    exit $LASTEXITCODE
}

Write-Host "-> waiting for postgres to accept connections..."
$pgReady = $false
for ($i = 1; $i -le 30; $i++) {
    docker compose -f deploy/docker-compose.yml exec -T postgres pg_isready -U wuling -d wuling *> $null
    if ($LASTEXITCODE -eq 0) { $pgReady = $true; break }
    Start-Sleep -Seconds 1
}
if (-not $pgReady) {
    Write-Error "postgres did not become ready within 30s; aborting."
    exit 1
}

Write-Host "-> applying database migrations..."
go run ./cmd/wuling-migrate up
if ($LASTEXITCODE -ne 0) {
    Write-Error "wuling-migrate up failed."
    exit $LASTEXITCODE
}

if (-not (Test-Path "frontend/node_modules")) {
    Write-Host "-> installing frontend dependencies (first run)..."
    Push-Location frontend
    & $npmExe install
    $code = $LASTEXITCODE
    Pop-Location
    if ($code -ne 0) {
        Write-Error "npm install failed."
        exit $code
    }
}

# Kill a process and all its descendants. On Windows npm.cmd spawns node;
# Stop-Process alone would orphan the Vite dev server, so taskkill /T is the
# only reliable way to take down the tree.
function Stop-Tree($proc) {
    if (-not $proc) { return }
    try { if ($proc.HasExited) { return } } catch { return }
    if ($onWindows) {
        & taskkill.exe /T /F /PID $proc.Id *> $null
    } else {
        try {
            Stop-Process -Id $proc.Id -Force
        } catch {
            Write-Error "failed to stop process $($proc.Id): $($_.Exception.Message)"
        }
    }
}

# -NoNewWindow keeps both children attached to this console so their stdout
# interleaves here and Ctrl+C propagates to them via the console group.
Write-Host "-> starting wuling-api on $($env:WULING_HTTP_ADDR)..."
$api = Start-Process -FilePath "go" `
    -ArgumentList "run", "./cmd/wuling-api" `
    -WorkingDirectory $repoRoot `
    -NoNewWindow -PassThru

Write-Host "-> starting frontend on http://localhost:3000..."
$front = Start-Process -FilePath $npmExe `
    -ArgumentList "run", "dev" `
    -WorkingDirectory (Join-Path $repoRoot "frontend") `
    -NoNewWindow -PassThru

Write-Host ""
Write-Host "-- Wuling DevOps dev environment ----------------------"
# Derive the displayed URL from WULING_HTTP_ADDR so it tracks the actual
# bind address (":8080", "0.0.0.0:8080", "127.0.0.1:9000", ...).
$displayAddr = $env:WULING_HTTP_ADDR
if (-not $displayAddr) { $displayAddr = ":8080" }
if ($displayAddr -match "^:\d+$") {
    $apiUrl = "http://localhost$displayAddr"
} elseif ($displayAddr -match "^https?://") {
    $apiUrl = $displayAddr
} else {
    $apiUrl = "http://$displayAddr"
}
Write-Host "  API:       $apiUrl"
Write-Host "  Frontend:  http://localhost:3000"
Write-Host "  Postgres:  localhost:5432 (wuling/wuling)"
Write-Host "-------------------------------------------------------"
Write-Host ""
Write-Host "Press Ctrl+C to stop."
Write-Host ""

try {
    while (-not $api.HasExited -and -not $front.HasExited) {
        Start-Sleep -Seconds 1
    }
    Write-Host "-> a dev service exited; tearing down the rest."
} finally {
    Stop-Tree $api
    Stop-Tree $front
    Write-Host "  (postgres is still running -- 'docker compose -f deploy/docker-compose.yml down' to stop it.)"
}
