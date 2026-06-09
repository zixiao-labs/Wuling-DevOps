// Copy everything under public/ into the build output (dist/) after `nasti build`.
//
// Why this exists: Nasti's dev server serves the public/ directory via sirv,
// but `nasti build` (1.6.x) does NOT copy public/ into the bundle — so static
// assets like favicon.svg and the PWA icons never reach dist/, and in
// production Caddy/nginx 404s them. (Dev looked fine, prod had no favicon.)
// This restores dev/prod parity until the toolchain copies public/ itself.
//
// index.html is owned by Nasti (it rewrites hashed <script> srcs), so we never
// let a stray public/index.html clobber the built one.
import { existsSync, cpSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const publicDir = resolve(root, "public");
const distDir = resolve(root, "dist");

if (!existsSync(publicDir)) {
  console.log("[copy-public] no public/ directory — nothing to copy");
  process.exit(0);
}
if (!existsSync(distDir)) {
  console.error("[copy-public] dist/ not found — run `nasti build` first");
  process.exit(1);
}

cpSync(publicDir, distDir, {
  recursive: true,
  force: true,
  filter: (src) => !/(^|[\\/])index\.html$/.test(src),
});

const copied = readdirSync(publicDir).filter((f) => f !== "index.html");
console.log(`[copy-public] copied ${copied.length} item(s) → dist/: ${copied.join(", ")}`);
