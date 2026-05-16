# Wuling DevOps · Nix flake
#
# This flake exposes the API binary, the migrate tool, the frontend dist,
# a dev shell, and a NixOS module so you can run the whole thing on NixOS
# without docker.
#
# Build:
#   nix build .#wuling-api
#   nix build .#wuling-migrate
#   nix build .#wuling-frontend
#
# Develop:
#   nix develop
#
# NixOS:
#   inputs.wuling.url = "github:zixiao-labs/Wuling-DevOps";
#   modules = [ wuling.nixosModules.default ];
#   services.wuling.enable = true;

{
  description = "Wuling DevOps — opinionated DevOps platform for the Arknights universe";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        repoRoot = ./.;

        # ── API ──────────────────────────────────────────────────────────
        # libgit2 is cgo, so we can't use buildGoModule's pure-Go default.
        wuling-api = pkgs.buildGoModule {
          pname = "wuling-api";
          version = self.shortRev or "dev";
          src = repoRoot;
          # `vendorHash` needs an update whenever go.sum changes. To compute:
          #   nix run nixpkgs#nix-prefetch -- "{ vendorHash }: (builtins.getFlake (toString ./.)).packages.x86_64-linux.wuling-api.goModules.overrideAttrs (_: { inherit vendorHash; })"
          # Or simpler: set to lib.fakeHash and let nix tell you the right one.
          # !! Must be a real sha256 before tagging a release — `nix build` fails
          #    on fakeHash, and the CI release pipeline depends on a buildable flake.
          vendorHash = pkgs.lib.fakeHash;

          subPackages = [ "cmd/wuling-api" ];

          nativeBuildInputs = with pkgs; [ pkg-config ];
          buildInputs = with pkgs; [ libgit2 ];

          # cgo + libgit2. buildGoModule already sets CGO_ENABLED=1 by default
          # when buildInputs are present, and current nixpkgs rejects passing it
          # both as a derivation arg and via `env`. Don't add it back here.
          # Strip the binary; reduces size by ~30%.
          ldflags = [ "-s" "-w" ];

          meta = with pkgs.lib; {
            description = "Wuling DevOps API server";
            homepage = "https://github.com/zixiao-labs/Wuling-DevOps";
            license = licenses.asl20;
            mainProgram = "wuling-api";
          };
        };

        wuling-migrate = wuling-api.overrideAttrs (_: {
          pname = "wuling-migrate";
          subPackages = [ "cmd/wuling-migrate" ];
          meta = wuling-api.meta // { mainProgram = "wuling-migrate"; };
        });

        # ── Frontend ─────────────────────────────────────────────────────
        # Nasti is a Vite-compatible bundler. We run it inside a buildNpmPackage
        # so node_modules end up reproducible by hash.
        wuling-frontend = pkgs.buildNpmPackage {
          pname = "wuling-frontend";
          version = self.shortRev or "dev";
          src = repoRoot + "/frontend";

          # See `nix build .#wuling-frontend` — npm will tell you the right hash
          # the first time. Or set to lib.fakeHash and read the error.
          # !! Must be a real sha256 before tagging a release.
          npmDepsHash = pkgs.lib.fakeHash;

          # Generate API types as part of the build so type errors are caught here.
          npmBuildScript = "build";

          # The OpenAPI spec lives outside frontend/, so make sure the build can
          # see it. buildNpmPackage runs prepublishOnly by default; ours just
          # calls `nasti build` which only reads from src.
          dontNpmInstall = false;

          installPhase = ''
            runHook preInstall
            mkdir -p $out
            cp -r dist/* $out/
            runHook postInstall
          '';

          meta = with pkgs.lib; {
            description = "Wuling DevOps frontend (static bundle)";
            license = licenses.asl20;
          };
        };

      in
      {
        packages = {
          inherit wuling-api wuling-migrate wuling-frontend;
          default = wuling-api;
        };

        # `nix develop` drops you into a shell with everything to hack on both
        # frontend & backend.
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            # Backend
            go_1_25
            pkg-config
            libgit2
            git
            # Frontend
            nodejs_22
            # Useful extras
            postgresql_18.lib
            jq
            curl
          ];
        };

        # Check that the flake itself builds (used by CI).
        checks = {
          inherit wuling-api wuling-migrate;
        };
      })
    // {
      # System-agnostic outputs.

      nixosModules.default = import ./nix/module.nix;
      nixosModules.wuling = self.nixosModules.default;
    };
}
