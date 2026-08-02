import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import data from "virtual:help-docs";
import { renderHelpPage } from "./entry-server";
import { readShellAssetsFromDist } from "./shell-assets.js";
import { slugToHref } from "./lookup";

const distDir = process.argv[2] ?? "dist";
const assets = readShellAssetsFromDist(path.resolve(distDir), {
  enhanceSrc: "/help/_assets/enhance.js",
});

function writePage(outPath: string, pathname: string, search = "") {
  const { status, html } = renderHelpPage(pathname, search, assets);
  mkdirSync(path.dirname(outPath), { recursive: true });
  writeFileSync(outPath, html, "utf8");
  console.log(`[prerender] ${status} ${pathname} → ${outPath}`);
}

for (const doc of data.docs) {
  const pathname = slugToHref(doc.slug);
  const rel = doc.slug === "" ? "help/index.html" : `help/${doc.slug}/index.html`;
  writePage(path.join(distDir, rel), pathname);
}

writePage(path.join(distDir, "help/404/index.html"), "/help/__nonexistent__");
console.log("[prerender] done", data.docs.length, "pages");
