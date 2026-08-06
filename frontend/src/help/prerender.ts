import { mkdirSync, writeFileSync, existsSync, copyFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import data from "virtual:help-docs";
import searchIndex from "virtual:help-search-index";
import { renderHelpPage } from "./entry-server";
import { readShellAssetsFromDist } from "./shell-assets.js";
import { slugToHref } from "./lookup";

const distDir = process.argv[2] ?? "dist";
const selfDir = path.dirname(fileURLToPath(import.meta.url));
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

writePage(path.join(distDir, "help/search/index.html"), "/help/search");
writePage(path.join(distDir, "help/404/index.html"), "/help/__nonexistent__");

// Ship progressive-enhancement assets beside the HTML so Caddy/nginx can
// serve /help without the Node SSR container (compose fallback / k8s).
const assetsDir = path.join(distDir, "help/_assets");
mkdirSync(assetsDir, { recursive: true });
const enhanceSrc = path.join(selfDir, "enhance.js");
if (!existsSync(enhanceSrc)) {
  throw new Error(`[prerender] missing ${enhanceSrc}`);
}
copyFileSync(enhanceSrc, path.join(assetsDir, "enhance.js"));
writeFileSync(path.join(assetsDir, "search-index.json"), JSON.stringify(searchIndex), "utf8");

const sitemapUrls = data.docs.map((d) => {
  const loc = slugToHref(d.slug);
  return `  <url><loc>${loc}</loc><lastmod>${d.updatedAt.slice(0, 10)}</lastmod></url>`;
});
writeFileSync(
  path.join(distDir, "help/sitemap.xml"),
  `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${sitemapUrls.join("\n")}\n</urlset>\n`,
  "utf8",
);

console.log("[prerender] done", data.docs.length, "pages + _assets");
