# Wuling DevOps - production deployment helper for Windows (PowerShell).
#
# Mirrors prod-deploy.sh and implements the three paths from
# deploy/production/README.md:
#   A. Docker Compose  - single host, self-managed VPS, home server
#   B. Nix flake       - NixOS module or bare-metal binaries
#   C. Kubernetes      - existing cluster (kubectl apply -k)
#
# The dev compose at deploy/docker-compose.yml is NOT for production.

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $repoRoot
$prodDir = Join-Path $repoRoot "deploy/production"

# $IsWindows is a built-in in PowerShell 7+; on Windows PowerShell 5.1 it
# doesn't exist, so fall back to $env:OS.
$onWindows = if ($null -ne $IsWindows) { $IsWindows } else { $env:OS -eq "Windows_NT" }

# --- ui helpers --------------------------------------------------------

function Show-Banner {
    Write-Host "██╗    ██╗██╗   ██╗██╗     ██╗███╗   ██╗ ██████╗     ██████╗ ███████╗██╗   ██╗ ██████╗ ██████╗ ███████╗"
    Write-Host "██║    ██║██║   ██║██║     ██║████╗  ██║██╔════╝     ██╔══██╗██╔════╝██║   ██║██╔═══██╗██╔══██╗██╔════╝"
    Write-Host "██║ █╗ ██║██║   ██║██║     ██║██╔██╗ ██║██║  ███╗    ██║  ██║█████╗  ██║   ██║██║   ██║██████╔╝███████╗"
    Write-Host "██║███╗██║██║   ██║██║     ██║██║╚██╗██║██║   ██║    ██║  ██║██╔══╝  ╚██╗ ██╔╝██║   ██║██╔═══╝ ╚════██║"
    Write-Host "╚███╔███╔╝╚██████╔╝███████╗██║██║ ╚████║╚██████╔╝    ██████╔╝███████╗ ╚████╔╝ ╚██████╔╝██║     ███████║"
    Write-Host " ╚══╝╚══╝  ╚═════╝ ╚══════╝╚═╝╚═╝  ╚═══╝ ╚═════╝     ╚═════╝ ╚══════╝  ╚═══╝   ╚═════╝ ╚═╝     ╚══════╝"
    Write-Host "                                                              production deploy runbook"
}

function Require-Command($name) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) {
        Write-Error "missing dependency: $name - install it and retry."
        exit 1
    }
}

# Empty input matches the default; -Default 'y' shows [Y/n], 'n' shows [y/N].
function Confirm-Yes {
    param(
        [Parameter(Mandatory=$true)][string]$Prompt,
        [string]$Default = "y"
    )
    $hint = if ($Default -eq "y") { "[Y/n]" } else { "[y/N]" }
    $ans = Read-Host "$Prompt $hint"
    if ([string]::IsNullOrWhiteSpace($ans)) { $ans = $Default }
    return ($ans -match "^[yY]")
}

# Returns the 1-based index of the chosen option.
function Read-Choice {
    param(
        [Parameter(Mandatory=$true)][string]$Prompt,
        [Parameter(Mandatory=$true)][string[]]$Options
    )
    for ($i = 0; $i -lt $Options.Length; $i++) {
        Write-Host ("  [{0}] {1}" -f ($i + 1), $Options[$i])
    }
    while ($true) {
        $raw = Read-Host $Prompt
        $n = 0
        if ([int]::TryParse($raw, [ref]$n) -and $n -ge 1 -and $n -le $Options.Length) {
            return $n
        }
        Write-Host ("invalid choice; pick 1-{0}." -f $Options.Length)
    }
}

# Generate a 48-byte hex secret using the OS crypto RNG.
function New-HexSecret {
    param([int]$Bytes = 48)
    $buf = New-Object byte[] $Bytes
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($buf)
    return -join ($buf | ForEach-Object { $_.ToString("x2") })
}

function Replace-InFile {
    param(
        [Parameter(Mandatory=$true)][string]$Path,
        [Parameter(Mandatory=$true)][string]$Pattern,
        [Parameter(Mandatory=$true)][string]$Replacement
    )
    $content = Get-Content -Raw -LiteralPath $Path
    $updated = $content -replace [regex]::Escape($Pattern), [regex]::Escape($Replacement).Replace('\','\\')
    # The second .Replace handles the regex-replace's special `$` and `\` escaping.
    Set-Content -LiteralPath $Path -Value $updated -NoNewline
}

# --- path A: docker compose -------------------------------------------

function Deploy-Compose {
    Require-Command "docker"

    $composeVersion = & docker compose version 2>&1
    if ($LASTEXITCODE -ne 0 -or -not ($composeVersion -match "v5|version 5")) {
        Write-Error "Docker Compose v5 required (got: $composeVersion). Install Docker Desktop with Compose v5 and retry."
        exit 1
    }

    Write-Host ""
    Write-Host "Docker Compose actions:"
    $action = Read-Choice -Prompt "Action: " -Options @(
        "First-time deploy (bootstrap .env, up, migrate)",
        "Upgrade (pull, migrate, restart)",
        "Status (compose ps)",
        "Tail logs",
        "Backup database now",
        "Down (stop the stack)"
    )

    switch ($action) {
        1 { Compose-FirstDeploy }
        2 { Compose-Upgrade }
        3 { Push-Location $prodDir; docker compose -f docker-compose.prod.yml ps; Pop-Location }
        4 { Push-Location $prodDir; docker compose -f docker-compose.prod.yml logs -f --tail=200; Pop-Location }
        5 { Compose-Backup }
        6 { Compose-Down }
    }
}

function Compose-FirstDeploy {
    Push-Location $prodDir
    try {
        if (-not (Test-Path ".env")) {
            Copy-Item "env.example" ".env"
            Write-Host "-> created .env from env.example."

            if (Confirm-Yes "Generate a random WULING_JWT_SECRET into .env now?") {
                $secret = New-HexSecret 48
                (Get-Content -Raw ".env") `
                    -replace "__REPLACE_ME_OR_DIE__", $secret `
                    | Set-Content -NoNewline ".env"
                Write-Host "  inserted a 48-byte hex JWT secret."
            }

            Write-Host ""
            Write-Host "Now edit $prodDir\.env. At minimum:"
            Write-Host "  WULING_DOMAIN              your public hostname"
            Write-Host "  POSTGRES_PASSWORD          strong random password"
            Write-Host "  WULING_DB_DSN              must contain the same POSTGRES_PASSWORD"
            Write-Host "  WULING_HTTP_CORS_ORIGINS   https://<your domain>"
            Write-Host "  ACME_EMAIL                 Let's Encrypt expiry notifications"
            Write-Host "  WULING_JWT_SECRET          (only if you skipped the auto-fill above)"
            Write-Host ""
            Write-Host "Optional: pin WULING_TAG to a release (e.g. v0.2.0) -- the compose file"
            Write-Host "defaults to :latest, which makes rollbacks ambiguous."
            Write-Host ""

            if (-not (Confirm-Yes "Continue once .env is filled in?")) {
                Write-Host "Aborting. Re-run when ready."
                exit 1
            }
        }

        $envContent = Get-Content -Raw ".env"
        if ($envContent -match "__REPLACE_ME_OR_DIE__|CHANGE_ME_POSTGRES_PW") {
            Write-Error ".env still has placeholder values. Edit it and re-run."
            exit 1
        }

        Write-Host "-> pulling images..."
        docker compose -f docker-compose.prod.yml pull
        if ($LASTEXITCODE -ne 0) { Write-Error "docker compose pull failed."; exit $LASTEXITCODE }

        Write-Host "-> bringing the stack up..."
        docker compose -f docker-compose.prod.yml up -d
        if ($LASTEXITCODE -ne 0) { Write-Error "docker compose up failed."; exit $LASTEXITCODE }

        Write-Host "-> waiting for the api container to become healthy..."
        $ready = $false
        for ($i = 1; $i -le 60; $i++) {
            $status = & docker compose -f docker-compose.prod.yml ps api 2>$null
            if ($status -match "healthy") { $ready = $true; break }
            Start-Sleep -Seconds 2
        }
        if (-not $ready) {
            Write-Warning "api didn't report healthy within 120s -- check logs:"
            Write-Warning "  docker compose -f $prodDir\docker-compose.prod.yml logs api"
        }

        Write-Host "-> running migrations..."
        docker compose -f docker-compose.prod.yml exec -T api wuling-migrate
        if ($LASTEXITCODE -ne 0) { Write-Error "migrations failed."; exit $LASTEXITCODE }

        $domain = (Select-String -Path ".env" -Pattern "^WULING_DOMAIN=(.*)$" -List).Matches[0].Groups[1].Value
        if ($domain -and $domain -ne "devops.example.com") {
            Write-Host "-> probing https://$domain/healthz ..."
            try {
                $resp = Invoke-WebRequest -UseBasicParsing -Uri "https://$domain/healthz" -TimeoutSec 10
                if ($resp.StatusCode -eq 200) { Write-Host "  ok." }
                else { Write-Host "  unexpected status: $($resp.StatusCode)" }
            } catch {
                Write-Host "  no luck yet -- DNS propagation or Let's Encrypt cert issuance can take a few minutes."
                Write-Host "  re-try later with: curl -v https://$domain/healthz"
            }
        }

        Write-Host "-> done. Follow logs with: docker compose -f $prodDir\docker-compose.prod.yml logs -f"
    } finally {
        Pop-Location
    }
}

function Compose-Upgrade {
    Push-Location $prodDir
    try {
        if (-not (Test-Path ".env")) {
            Write-Error "$prodDir\.env missing -- run first-time deploy."
            exit 1
        }
        docker compose -f docker-compose.prod.yml pull
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        docker compose -f docker-compose.prod.yml run --rm api wuling-migrate
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        docker compose -f docker-compose.prod.yml up -d
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        docker compose -f docker-compose.prod.yml ps
    } finally {
        Pop-Location
    }
}

function Compose-Backup {
    Push-Location $prodDir
    try {
        $stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd-HHmmss")
        $outDir = Join-Path $repoRoot "var/backups"
        New-Item -ItemType Directory -Force -Path $outDir | Out-Null
        $outFile = Join-Path $outDir "wuling-$stamp.sql.gz"

        Write-Host "-> dumping into $outFile"
        # pg_dump | gzip inside the container, capture stdout to local file.
        # PowerShell's redirect preserves the binary stream.
        $tmp = "$outFile.tmp"
        docker compose -f docker-compose.prod.yml exec -T postgres `
            sh -c "pg_dump --no-owner --no-privileges --clean --if-exists -U wuling wuling | gzip -9" `
            > $tmp
        if ($LASTEXITCODE -ne 0) {
            Remove-Item -Force -ErrorAction SilentlyContinue $tmp
            Write-Error "backup failed."
            exit $LASTEXITCODE
        }
        Move-Item -Force $tmp $outFile
        $bytes = (Get-Item $outFile).Length
        Write-Host "  done ($bytes bytes)."
        Write-Host "  restore with: deploy/production/postgres/restore.sh $outFile"
    } finally {
        Pop-Location
    }
}

function Compose-Down {
    if (Confirm-Yes "Stop all production services? (volumes are preserved)" "n") {
        Push-Location $prodDir
        try {
            docker compose -f docker-compose.prod.yml down
        } finally {
            Pop-Location
        }
    }
}

# --- path B: nix ------------------------------------------------------

function Deploy-Nix {
    Require-Command "nix"

    & nix flake metadata --no-write-lock-file *> $null
    if ($LASTEXITCODE -ne 0) {
        Write-Error @"
nix flakes are not enabled.
  Run with: nix --extra-experimental-features 'nix-command flakes' ...
  Or add to /etc/nix/nix.conf: experimental-features = nix-command flakes
"@
        exit 1
    }

    Write-Host ""
    Write-Host "Nix flake actions:"
    $action = Read-Choice -Prompt "Action: " -Options @(
        "Build everything (api + migrate + frontend)",
        "Build wuling-api",
        "Build wuling-migrate",
        "Build wuling-frontend",
        "nix flake check",
        "Show NixOS module wiring instructions"
    )

    switch ($action) {
        1 {
            nix build .#wuling-api      --out-link result-api
            if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
            nix build .#wuling-migrate  --out-link result-migrate
            if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
            nix build .#wuling-frontend --out-link result-frontend
            if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
            Write-Host ""
            Write-Host "-> outputs:"
            Write-Host "    result-api/bin/wuling-api"
            Write-Host "    result-migrate/bin/wuling-migrate"
            Write-Host "    result-frontend/   (static dist/ for nginx/caddy)"
        }
        2 { nix build .#wuling-api      --out-link result-api      ; if ($LASTEXITCODE -eq 0) { Write-Host "-> result-api/bin/wuling-api" } }
        3 { nix build .#wuling-migrate  --out-link result-migrate  ; if ($LASTEXITCODE -eq 0) { Write-Host "-> result-migrate/bin/wuling-migrate" } }
        4 { nix build .#wuling-frontend --out-link result-frontend ; if ($LASTEXITCODE -eq 0) { Write-Host "-> result-frontend/" } }
        5 { nix flake check }
        6 { Show-NixInstructions }
    }
}

function Show-NixInstructions {
    Write-Host @"

----------------------------------------------------------------------
 NixOS module wiring (see deploy/production/README.md §4.2)
----------------------------------------------------------------------
1. Add this flake to your system flake's inputs:

     inputs.wuling.url = "github:zixiao-labs/Wuling-DevOps";

2. Import the module in your nixosSystem config:

     modules = [ inputs.wuling.nixosModules.default ./host.nix ];

3. Enable the service in host.nix:

     services.wuling = {
       enable        = true;
       domain        = "devops.example.com";
       jwtSecretFile = "/run/secrets/wuling-jwt";    # via sops/agenix
       repoRoot      = "/var/lib/wuling/repos";
       frontend.enable = true;
       reverseProxy = "caddy";                       # 'caddy'|'nginx'|'none'
     };
     networking.firewall.allowedTCPPorts = [ 80 443 2222 ];

4. Apply: sudo nixos-rebuild switch --flake .#<hostname>

For non-NixOS Linux/macOS hosts, copy the built binaries and unit file:

   scp result-api/bin/wuling-* user@host:/usr/local/bin/
   sudo cp deploy/production/systemd/wuling-api.service /etc/systemd/system/
   sudo install -m 600 deploy/production/env.example /etc/wuling/wuling.env
   # edit /etc/wuling/wuling.env, then:
   sudo systemctl daemon-reload
   sudo systemctl enable --now wuling-api
----------------------------------------------------------------------

NOTE: nix runs natively on Linux/macOS. On Windows it works inside
      WSL2 -- consider using prod-deploy.sh from a WSL shell instead.
"@
}

# --- path C: kubernetes ----------------------------------------------

function Deploy-K8s {
    Require-Command "kubectl"

    & kubectl cluster-info *> $null
    if ($LASTEXITCODE -ne 0) {
        Write-Error "kubectl can't reach a cluster. Check your KUBECONFIG / context."
        exit 1
    }

    Write-Host ""
    Write-Host "Kubernetes actions:"
    $action = Read-Choice -Prompt "Action: " -Options @(
        "First-time deploy (apply -k + migrate)",
        "Apply updates (apply -k again)",
        "Status (pods / svc / ingress)",
        "Run migrations",
        "Tail api logs",
        "Delete the namespace (DESTRUCTIVE)"
    )

    switch ($action) {
        1 { K8s-FirstDeploy }
        2 { kubectl apply -k (Join-Path $prodDir "k8s/") }
        3 { K8s-Status }
        4 { kubectl -n wuling exec deploy/wuling-api -- wuling-migrate }
        5 { kubectl -n wuling logs deploy/wuling-api -f --tail=200 }
        6 { K8s-Destroy }
    }
}

function K8s-FirstDeploy {
    $secretPath  = Join-Path $prodDir "k8s/secret.yaml"
    $examplePath = Join-Path $prodDir "k8s/secret.example.yaml"
    $ingressPath = Join-Path $prodDir "k8s/ingress.yaml"

    if (-not (Test-Path $secretPath)) {
        Copy-Item $examplePath $secretPath
        Write-Host "-> created k8s/secret.yaml from secret.example.yaml."

        if (Confirm-Yes "Generate a random WULING_JWT_SECRET into secret.yaml?") {
            $jwt = New-HexSecret 48
            # Only replace the JWT_SECRET line -- leave DB password as REPLACE_ME so
            # the user still has to make a choice for it.
            $lines = Get-Content $secretPath
            $patched = $lines | ForEach-Object {
                if ($_ -match 'WULING_JWT_SECRET:') {
                    $_ -replace '"REPLACE_ME"', "`"$jwt`""
                } else {
                    $_
                }
            }
            $patched | Set-Content $secretPath
            Write-Host "  inserted JWT secret."
        }

        Write-Host ""
        Write-Host "Edit these files now:"
        Write-Host "  $secretPath  - set DB password (both REPLACE_ME spots)"
        Write-Host "                                 + JWT secret if you skipped the auto-fill"
        Write-Host "  $ingressPath - set your real hostname + TLS issuer"
        Write-Host ""

        if (-not (Confirm-Yes "Continue once both files are filled in?")) {
            Write-Host "Aborting."
            exit 1
        }
    }

    if ((Get-Content -Raw $secretPath) -match "REPLACE_ME") {
        Write-Error "secret.yaml still contains REPLACE_ME -- fix and re-run."
        exit 1
    }

    Write-Host "-> applying manifests..."
    kubectl apply -k (Join-Path $prodDir "k8s/")
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    Write-Host "-> waiting for wuling-api rollout (5 min max)..."
    kubectl -n wuling rollout status deployment/wuling-api --timeout=300s
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "rollout didn't complete. Check pod status with: kubectl -n wuling get pods"
        exit 1
    }

    Write-Host "-> running migrations..."
    kubectl -n wuling exec deploy/wuling-api -- wuling-migrate
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    Write-Host ""
    K8s-Status
}

function K8s-Status {
    kubectl -n wuling get pods,svc,ingress
}

function K8s-Destroy {
    Write-Host "WARNING: this deletes the 'wuling' namespace, including the postgres PVC"
    Write-Host "         and the repo storage PVC. All repositories and DB data will be lost."
    if (Confirm-Yes "Are you absolutely sure?" "n") {
        kubectl delete -k (Join-Path $prodDir "k8s/")
    }
}

# --- main ------------------------------------------------------------

Show-Banner

Write-Host @"

Production deployment helper. Three paths are supported, matching
deploy/production/README.md:

  A. Docker Compose  - single host, self-managed VPS, home server
  B. Nix flake       - NixOS module or bare-metal binaries via Nix
  C. Kubernetes      - existing cluster (kubectl apply -k)

The dev compose at deploy/docker-compose.yml is NOT for production.

"@

if (-not (Confirm-Yes "Continue?")) {
    Write-Host "Aborting."
    exit 1
}

$choice = Read-Choice -Prompt "Pick a deployment path: " -Options @(
    "Docker Compose",
    "Nix flake / NixOS",
    "Kubernetes"
)

switch ($choice) {
    1 { Deploy-Compose }
    2 { Deploy-Nix }
    3 { Deploy-K8s }
}
