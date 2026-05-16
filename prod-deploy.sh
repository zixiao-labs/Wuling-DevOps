#!/usr/bin/env bash
# Wuling DevOps — production deployment helper for Linux & macOS.
#
# Implements the three paths from deploy/production/README.md:
#   A. Docker Compose  — single host, self-managed VPS, home server
#   B. Nix flake       — NixOS module or bare-metal binaries
#   C. Kubernetes      — existing cluster with `kubectl apply -k`
#
# The dev compose at deploy/docker-compose.yml is NOT for production.

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$(pwd)"
PROD_DIR="$ROOT/deploy/production"

# ─── ui helpers ────────────────────────────────────────────────────────────

banner() {
    cat <<'BANNER'
██╗    ██╗██╗   ██╗██╗     ██╗███╗   ██╗ ██████╗     ██████╗ ███████╗██╗   ██╗ ██████╗ ██████╗ ███████╗
██║    ██║██║   ██║██║     ██║████╗  ██║██╔════╝     ██╔══██╗██╔════╝██║   ██║██╔═══██╗██╔══██╗██╔════╝
██║ █╗ ██║██║   ██║██║     ██║██╔██╗ ██║██║  ███╗    ██║  ██║█████╗  ██║   ██║██║   ██║██████╔╝███████╗
██║███╗██║██║   ██║██║     ██║██║╚██╗██║██║   ██║    ██║  ██║██╔══╝  ╚██╗ ██╔╝██║   ██║██╔═══╝ ╚════██║
╚███╔███╔╝╚██████╔╝███████╗██║██║ ╚████║╚██████╔╝    ██████╔╝███████╗ ╚████╔╝ ╚██████╔╝██║     ███████║
 ╚══╝╚══╝  ╚═════╝ ╚══════╝╚═╝╚═╝  ╚═══╝ ╚═════╝     ╚═════╝ ╚══════╝  ╚═══╝   ╚═════╝ ╚═╝     ╚══════╝
                                                              production deploy runbook
BANNER
}

require() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "missing dependency: $1 — install it and retry." >&2
        exit 1
    fi
}

# confirm <prompt> [default y|n]   — empty input matches the default.
confirm() {
    local prompt="$1" default="${2:-y}" hint ans
    if [ "$default" = "y" ]; then hint="[Y/n]"; else hint="[y/N]"; fi
    read -r -p "$prompt $hint " ans
    [ -z "$ans" ] && ans="$default"
    case "$ans" in
        y|Y) return 0 ;;
        *)   return 1 ;;
    esac
}

# pick "prompt" "label-1" "label-2" ...  — echoes the 1-based selected index.
pick() {
    local prompt="$1"; shift
    local i=1
    for label in "$@"; do
        printf "  [%d] %s\n" "$i" "$label" >&2
        i=$((i+1))
    done
    local n=$#
    local choice
    while true; do
        read -r -p "$prompt " choice
        if [[ "$choice" =~ ^[0-9]+$ ]] && [ "$choice" -ge 1 ] && [ "$choice" -le "$n" ]; then
            echo "$choice"
            return
        fi
        echo "invalid choice; pick 1-$n." >&2
    done
}

# in-place substitute that works on BSD sed (macOS) and GNU sed alike.
substitute() {
    local pattern="$1" replacement="$2" file="$3"
    sed "s|$pattern|$replacement|" "$file" > "$file.new" && mv "$file.new" "$file"
}

# ─── path A: docker compose ────────────────────────────────────────────────

deploy_compose() {
    require docker

    local version_out
    version_out=$(docker compose version 2>&1 || true)
    if ! echo "$version_out" | grep -qE "v5|version 5"; then
        echo "error: Docker Compose v5 required (got: $version_out)." >&2
        echo "       Install Docker Desktop / engine with Compose v5 and retry." >&2
        exit 1
    fi

    echo
    echo "Docker Compose actions:"
    local action
    action=$(pick "Action: " \
        "First-time deploy (bootstrap .env, up, migrate)" \
        "Upgrade (pull, migrate, restart)" \
        "Status (compose ps)" \
        "Tail logs" \
        "Backup database now" \
        "Down (stop the stack)")

    case "$action" in
        1) compose_first_deploy ;;
        2) compose_upgrade ;;
        3) (cd "$PROD_DIR" && docker compose -f docker-compose.prod.yml ps) ;;
        4) (cd "$PROD_DIR" && docker compose -f docker-compose.prod.yml logs -f --tail=200) ;;
        5) compose_backup ;;
        6) compose_down ;;
    esac
}

compose_first_deploy() {
    cd "$PROD_DIR"

    if [ ! -f .env ]; then
        cp env.example .env
        echo "→ created .env from env.example."

        if command -v openssl >/dev/null 2>&1 && confirm "Generate a random WULING_JWT_SECRET into .env now?"; then
            local secret
            secret=$(openssl rand -hex 48)
            substitute "__REPLACE_ME_OR_DIE__" "$secret" .env
            echo "  inserted a 48-byte hex JWT secret."
        fi

        cat <<EOF

Now edit $PROD_DIR/.env. At minimum:
  WULING_DOMAIN              your public hostname
  POSTGRES_PASSWORD          strong random password
  WULING_DB_DSN              must contain the same POSTGRES_PASSWORD
  WULING_HTTP_CORS_ORIGINS   https://<your domain>
  ACME_EMAIL                 Let's Encrypt expiry notifications
  WULING_JWT_SECRET          (only if you skipped the auto-fill above)

Optional: pin WULING_TAG to a release (e.g. v0.2.0) — the compose file
defaults to :latest, which makes rollbacks ambiguous.

EOF
        if ! confirm "Continue once .env is filled in?"; then
            echo "Aborting. Re-run when ready."
            exit 1
        fi
    fi

    if grep -q "__REPLACE_ME_OR_DIE__\|CHANGE_ME_POSTGRES_PW" .env; then
        echo "error: .env still has placeholder values. Edit it and re-run." >&2
        exit 1
    fi

    echo "→ pulling images…"
    docker compose -f docker-compose.prod.yml pull

    echo "→ bringing the stack up…"
    docker compose -f docker-compose.prod.yml up -d

    echo "→ waiting for the api container to become healthy…"
    local ready=0
    for _ in $(seq 1 60); do
        if docker compose -f docker-compose.prod.yml ps api 2>/dev/null | grep -q "healthy"; then
            ready=1
            break
        fi
        sleep 2
    done
    if [ "$ready" -ne 1 ]; then
        echo "warning: api didn't report healthy within 120s — check:" >&2
        echo "         docker compose -f $PROD_DIR/docker-compose.prod.yml logs api" >&2
    fi

    echo "→ running migrations…"
    docker compose -f docker-compose.prod.yml exec -T api wuling-migrate

    local domain
    domain=$(grep -E "^WULING_DOMAIN=" .env | head -n1 | cut -d= -f2-)
    if [ -n "$domain" ] && [ "$domain" != "devops.example.com" ]; then
        echo "→ probing https://$domain/healthz …"
        if curl -sfm 10 "https://$domain/healthz" >/dev/null 2>&1; then
            echo "  ok."
        else
            echo "  no luck yet — DNS propagation or Let's Encrypt cert issuance can take a few minutes."
            echo "  re-try later with: curl -v https://$domain/healthz"
        fi
    fi

    echo "→ done. Follow logs with: docker compose -f $PROD_DIR/docker-compose.prod.yml logs -f"
}

compose_upgrade() {
    cd "$PROD_DIR"
    [ -f .env ] || { echo "error: $PROD_DIR/.env missing — run first-time deploy."; exit 1; }
    docker compose -f docker-compose.prod.yml pull
    docker compose -f docker-compose.prod.yml run --rm api wuling-migrate
    docker compose -f docker-compose.prod.yml up -d
    docker compose -f docker-compose.prod.yml ps
}

compose_backup() {
    cd "$PROD_DIR"
    local out
    out="$ROOT/var/backups/wuling-$(date -u +%Y%m%d-%H%M%S).sql.gz"
    mkdir -p "$(dirname "$out")"
    echo "→ dumping into $out"
    docker compose -f docker-compose.prod.yml exec -T postgres \
        pg_dump --no-owner --no-privileges --clean --if-exists -U wuling wuling \
        | gzip -9 > "$out"
    echo "  done ($(wc -c <"$out" | tr -d ' ') bytes)."
    echo "  restore with: deploy/production/postgres/restore.sh $out"
}

compose_down() {
    if confirm "Stop all production services? (volumes are preserved)" n; then
        cd "$PROD_DIR"
        docker compose -f docker-compose.prod.yml down
    fi
}

# ─── path B: nix ───────────────────────────────────────────────────────────

deploy_nix() {
    require nix

    if ! nix flake metadata --no-write-lock-file >/dev/null 2>&1; then
        echo "error: nix flakes are not enabled." >&2
        echo "  Run with: nix --extra-experimental-features 'nix-command flakes' ..." >&2
        echo "  Or add to /etc/nix/nix.conf: experimental-features = nix-command flakes" >&2
        exit 1
    fi

    echo
    echo "Nix flake actions:"
    local action
    action=$(pick "Action: " \
        "Build everything (api + migrate + frontend)" \
        "Build wuling-api" \
        "Build wuling-migrate" \
        "Build wuling-frontend" \
        "nix flake check" \
        "Show NixOS module wiring instructions")

    case "$action" in
        1)
            nix build .#wuling-api      --out-link result-api
            nix build .#wuling-migrate  --out-link result-migrate
            nix build .#wuling-frontend --out-link result-frontend
            echo
            echo "→ outputs:"
            echo "    result-api/bin/wuling-api"
            echo "    result-migrate/bin/wuling-migrate"
            echo "    result-frontend/   (static dist/ for nginx/caddy)"
            ;;
        2) nix build .#wuling-api      --out-link result-api      && echo "→ result-api/bin/wuling-api" ;;
        3) nix build .#wuling-migrate  --out-link result-migrate  && echo "→ result-migrate/bin/wuling-migrate" ;;
        4) nix build .#wuling-frontend --out-link result-frontend && echo "→ result-frontend/" ;;
        5) nix flake check ;;
        6) nix_instructions ;;
    esac
}

nix_instructions() {
    cat <<'EOF'

──────────────────────────────────────────────────────────────────────
 NixOS module wiring (see deploy/production/README.md §4.2)
──────────────────────────────────────────────────────────────────────
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
──────────────────────────────────────────────────────────────────────
EOF
}

# ─── path C: kubernetes ────────────────────────────────────────────────────

deploy_k8s() {
    require kubectl

    if ! kubectl cluster-info >/dev/null 2>&1; then
        echo "error: kubectl can't reach a cluster. Check your KUBECONFIG / context." >&2
        exit 1
    fi

    echo
    echo "Kubernetes actions:"
    local action
    action=$(pick "Action: " \
        "First-time deploy (apply -k + migrate)" \
        "Apply updates (apply -k again)" \
        "Status (pods / svc / ingress)" \
        "Run migrations" \
        "Tail api logs" \
        "Delete the namespace (DESTRUCTIVE)")

    case "$action" in
        1) k8s_first_deploy ;;
        2) kubectl apply -k "$PROD_DIR/k8s/" ;;
        3) k8s_status ;;
        4) kubectl -n wuling exec deploy/wuling-api -- wuling-migrate ;;
        5) kubectl -n wuling logs deploy/wuling-api -f --tail=200 ;;
        6) k8s_destroy ;;
    esac
}

k8s_first_deploy() {
    if [ ! -f "$PROD_DIR/k8s/secret.yaml" ]; then
        cp "$PROD_DIR/k8s/secret.example.yaml" "$PROD_DIR/k8s/secret.yaml"
        echo "→ created k8s/secret.yaml from secret.example.yaml."

        if command -v openssl >/dev/null 2>&1 && confirm "Generate a random WULING_JWT_SECRET into secret.yaml?"; then
            local jwt
            jwt=$(openssl rand -hex 48)
            # Only replace the JWT line — leave DB password as REPLACE_ME so
            # the user still has to make a choice for it.
            awk -v jwt="$jwt" '
                /WULING_JWT_SECRET:/ { sub(/"REPLACE_ME"/, "\"" jwt "\"") }
                { print }
            ' "$PROD_DIR/k8s/secret.yaml" > "$PROD_DIR/k8s/secret.yaml.new"
            mv "$PROD_DIR/k8s/secret.yaml.new" "$PROD_DIR/k8s/secret.yaml"
            echo "  inserted JWT secret."
        fi

        cat <<EOF

Edit these files now:
  $PROD_DIR/k8s/secret.yaml   — set DB password (both REPLACE_ME spots)
                                 + JWT secret if you skipped the auto-fill
  $PROD_DIR/k8s/ingress.yaml  — set your real hostname + TLS issuer

EOF
        if ! confirm "Continue once both files are filled in?"; then
            echo "Aborting."
            exit 1
        fi
    fi

    if grep -q "REPLACE_ME" "$PROD_DIR/k8s/secret.yaml"; then
        echo "error: secret.yaml still contains REPLACE_ME — fix and re-run." >&2
        exit 1
    fi

    echo "→ applying manifests…"
    kubectl apply -k "$PROD_DIR/k8s/"

    echo "→ waiting for wuling-api rollout (5 min max)…"
    if ! kubectl -n wuling rollout status deployment/wuling-api --timeout=300s; then
        echo "warning: rollout didn't complete. Check pod status with:" >&2
        echo "         kubectl -n wuling get pods" >&2
        exit 1
    fi

    echo "→ running migrations…"
    kubectl -n wuling exec deploy/wuling-api -- wuling-migrate

    echo
    k8s_status
}

k8s_status() {
    kubectl -n wuling get pods,svc,ingress
}

k8s_destroy() {
    echo "WARNING: this deletes the 'wuling' namespace, including the postgres PVC"
    echo "         and the repo storage PVC. All repositories and DB data will be lost."
    if confirm "Are you absolutely sure?" n; then
        kubectl delete -k "$PROD_DIR/k8s/"
    fi
}

# ─── main ──────────────────────────────────────────────────────────────────

banner

cat <<INTRO

Production deployment helper. Three paths are supported, matching
deploy/production/README.md:

  A. Docker Compose  — single host, self-managed VPS, home server
  B. Nix flake       — NixOS module or bare-metal binaries via Nix
  C. Kubernetes      — existing cluster (kubectl apply -k)

The dev compose at deploy/docker-compose.yml is NOT for production.

INTRO

if ! confirm "Continue?"; then
    echo "Aborting."
    exit 1
fi

choice=$(pick "Pick a deployment path: " \
    "Docker Compose" \
    "Nix flake / NixOS" \
    "Kubernetes")

case "$choice" in
    1) deploy_compose ;;
    2) deploy_nix ;;
    3) deploy_k8s ;;
esac
