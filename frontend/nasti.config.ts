import { defineConfig } from "@nasti-toolchain/nasti";
import { chen } from "chen-the-dawnstreak/vite-plugin";

declare const process: { env: Record<string, string | undefined> };

const API_TARGET = process.env.WULING_API_URL ?? "http://localhost:8080";

// Git smart HTTP lives at the root (e.g. /myorg/myproj/myrepo.git/info/refs),
// so we can't proxy by a single prefix. We list every well-known path and
// fall back to a regex that matches the .git suffix.
const apiProxy = {
  target: API_TARGET,
  changeOrigin: true,
};

export default defineConfig({
  plugins: [
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
    proxy: {
      "/api": apiProxy,
      "/healthz": apiProxy,
      "/version": apiProxy,
      // Git smart HTTP: /{org}/{project}/{repo}.git/...
      // Nasti's proxy keys are matched as path prefixes; the rule below uses a
      // regex-style prefix that captures anything ending in `.git/` and the
      // sub-paths underneath it.
      "^/[^/]+/[^/]+/[^/]+\\.git/": apiProxy,
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
});
