// nasti.config.ts runs in Node, but the project's tsconfig deliberately scopes
// "types" to the chen vite-plugin client so app code can't accidentally rely on
// node globals. Pull in the one node module we need here with @ts-ignore so
// we don't have to add @types/node just for this dev-only config file. (`URL`
// is a global in both Node and the DOM lib that's already in scope.)
// @ts-ignore — node builtin, no types in this tsconfig
import http from "node:http";

import { defineConfig, type NastiPlugin } from "@nasti-toolchain/nasti";
import { chen } from "chen-the-dawnstreak/vite-plugin";

declare const process: { env: Record<string, string | undefined> };

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

export default defineConfig({
  plugins: [
    devProxyPlugin(API_TARGET),
    ...chen({
      routes: true,
      pwa: {
        name: "武陵 DevOps",
        shortName: "武陵",
        themeColor: "#5a8fb0",
        backgroundColor: "#f5f7fa",
        display: "standalone",
        icons: [
          { src: "/icon-192.png", sizes: "192x192", type: "image/png" },
          { src: "/icon-512.png", sizes: "512x512", type: "image/png" },
        ],
      },
    }),
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
