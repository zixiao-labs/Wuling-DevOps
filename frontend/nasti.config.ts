// nasti.config.ts runs in Node, but the project's tsconfig deliberately scopes
// "types" to the chen vite-plugin client so app code can't accidentally rely on
// node globals. Pull in the one node module we need here with @ts-ignore so
// we don't have to add @types/node just for this dev-only config file. (`URL`
// is a global in both Node and the DOM lib that's already in scope.)
// @ts-ignore — node builtin, no types in this tsconfig
import http from "node:http";
// @ts-ignore — node builtin, no types in this tsconfig
import fs from "node:fs";
// @ts-ignore — node builtin, no types in this tsconfig
import path from "node:path";

import { defineConfig, type NastiPlugin } from "@nasti-toolchain/nasti";
import { chen } from "chen-the-dawnstreak/vite-plugin";

// @ts-ignore — plain-JS build helper, deliberately outside the app tsconfig
import { serviceWorkerPlugin } from "./build/service-worker-plugin.js";

declare const process: { env: Record<string, string | undefined>; cwd(): string };

const API_TARGET = process.env.WULING_API_URL ?? "http://localhost:8080";

// Nasti 1.6.x accepts a `server.proxy` field but the dev server never wires
// it to any middleware, so requests fall through to sirv and return 404. We
// register the proxy ourselves via `configureServer` until upstream supports it.
const apiPrefixes = ["/api/", "/healthz", "/version"];
const gitRoute = /^\/[^/]+\/[^/]+\/[^/]+\.git(\/|$)/;

function shouldProxy(url: string): boolean {
  if (url === "/healthz" || url === "/version") return true;
  if (apiPrefixes.some((p) => url.startsWith(p))) return true;
  return gitRoute.test(url);
}

function devProxyPlugin(target: string): NastiPlugin {
  const upstream = new URL(target);
  const port = upstream.port || (upstream.protocol === "https:" ? "443" : "80");
  return {
    name: "wuling-dev-proxy",
    configureServer(server) {
      server.middlewares.use(
        (req: any, res: any, next: (err?: unknown) => void) => {
          const url: string = req.url ?? "/";
          if (!shouldProxy(url)) {
            next();
            return;
          }
          const headers = { ...req.headers, host: upstream.host };
          const proxyReq = http.request(
            {
              protocol: upstream.protocol,
              hostname: upstream.hostname,
              port,
              method: req.method,
              path: url,
              headers,
            },
            (proxyRes: any) => {
              res.writeHead(proxyRes.statusCode ?? 502, proxyRes.headers);
              proxyRes.pipe(res);
            },
          );
          proxyReq.on("error", (err: Error) => {
            if (!res.headersSent) {
              res.statusCode = 502;
              res.setHeader("Content-Type", "application/json");
            }
            res.end(
              JSON.stringify({
                error: {
                  code: "bad_gateway",
                  message: `dev proxy: ${err.message}`,
                },
              }),
            );
          });
          req.pipe(proxyReq);
        },
      );
    },
  };
}

// Nasti serves `public/` through sirv in the dev server (src/server/index.ts)
// but its build never copies the directory into outDir — so every file under
// public/ silently vanishes in a production bundle, which is why the Docker
// image shipped without a favicon. Copy the tree ourselves once the bundle is
// closed. Existing files are not overwritten: a build artifact of the same
// name (Nasti writes index.html and manifest.json into outDir itself) wins.
function copyPublicDirPlugin(): NastiPlugin {
  // `nasti build` is always invoked through the package's npm script, so cwd is
  // the frontend package root — same assumption `build.outDir: "dist"` below
  // already makes. (This file is ESM, so there is no __dirname to use.)
  const root = process.cwd();
  const publicDir = path.resolve(root, "public");
  const outDir = path.resolve(root, "dist");
  return {
    name: "wuling-copy-public",
    closeBundle() {
      if (!fs.existsSync(publicDir)) return;
      for (const entry of fs.readdirSync(publicDir, { withFileTypes: true })) {
        const from = path.join(publicDir, entry.name);
        const to = path.join(outDir, entry.name);
        if (fs.existsSync(to)) continue;
        fs.cpSync(from, to, { recursive: true });
      }
    },
  };
}

// Manifest icons. `purpose` is absent from chen's ChenPWAOptions["icons"] type
// even though the plugin spreads the array into the manifest verbatim, so this
// is declared as a variable rather than inline — excess-property checking only
// fires on fresh object literals at the call site.
const pwaIcons = [
  { src: "/icon-192.png", sizes: "192x192", type: "image/png" },
  { src: "/icon-512.png", sizes: "512x512", type: "image/png" },
  {
    src: "/icon-maskable-512.png",
    sizes: "512x512",
    type: "image/png",
    purpose: "maskable",
  },
];

export default defineConfig({
  plugins: [
    devProxyPlugin(API_TARGET),
    copyPublicDirPlugin(),
    ...chen({
      routes: true,
      pwa: {
        name: "武陵 DevOps",
        shortName: "武陵",
        themeColor: "#46828F",
        backgroundColor: "#D0E0E3",
        display: "standalone",
        icons: pwaIcons,
      },
    }),
    // Must come after chen(): both write sw.js in closeBundle and the last
    // write wins. See build/service-worker-plugin.js for why chen's generated
    // worker cannot be shipped as-is.
    serviceWorkerPlugin(),
  ],
  resolve: {
    alias: {
      "@": "/src",
    },
  },
  server: {
    port: 3000,
    host: true,
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
});
