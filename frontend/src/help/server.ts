import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import path from "node:path";
import { renderHelpPage } from "./entry-server";
import data from "virtual:help-docs";
import searchIndex from "virtual:help-search-index";
import { readShellAssetsFromDist } from "./shell-assets.js";
import { slugToHref } from "./lookup";

const PORT = Number(process.env.WULING_HELP_PORT ?? 8081);
const STATIC_ROOT = process.env.WULING_HELP_STATIC_ROOT ?? "/srv/wuling-frontend";
const BASE_URL = process.env.WULING_HELP_BASE_URL ?? "";
const SELF = path.dirname(new URL(import.meta.url).pathname);

const IMMUTABLE = { "Cache-Control": "public, max-age=31536000, immutable" };

let shellAssetsCache: ReturnType<typeof readShellAssetsFromDist> | null = null;
let shellAssetsAt = 0;

function readShellAssets() {
  const now = Date.now();
  if (!shellAssetsCache || now - shellAssetsAt > 30_000) {
    shellAssetsCache = readShellAssetsFromDist(STATIC_ROOT, { baseUrl: BASE_URL });
    shellAssetsAt = now;
  }
  return shellAssetsCache;
}

function end(
  res: import("node:http").ServerResponse,
  status: number,
  type: string | null,
  body: string,
  extra: Record<string, string> = {},
) {
  if (type) res.setHeader("Content-Type", type);
  for (const [k, v] of Object.entries(extra)) res.setHeader(k, v);
  res.statusCode = status;
  res.end(body);
}

const enhanceJs = readFileSync(path.join(SELF, "enhance.js"), "utf8");
const buildId = createHash("sha256")
  .update(enhanceJs + data.buildId)
  .digest("hex")
  .slice(0, 12);

const pages = new Map<string, { status: number; html: string; etag: string }>();

function sitemap(): string {
  const urls = data.docs.map((d) => {
    const loc = `${BASE_URL}${slugToHref(d.slug)}`;
    return `  <url><loc>${loc}</loc><lastmod>${d.updatedAt.slice(0, 10)}</lastmod></url>`;
  });
  return `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls.join("\n")}\n</urlset>`;
}

createServer((req, res) => {
  if (req.method !== "GET" && req.method !== "HEAD") {
    return end(res, 405, "text/plain", "method not allowed");
  }
  const url = new URL(req.url ?? "/", "http://local");
  let p = url.pathname.replace(/\/+$/, "") || "/help";

  if (p === "/_healthz") return end(res, 200, "text/plain", "ok");
  if (p === "/help/_assets/enhance.js") {
    return end(res, 200, "text/javascript; charset=utf-8", enhanceJs, IMMUTABLE);
  }
  if (p === "/help/_assets/search-index.json") {
    return end(
      res,
      200,
      "application/json; charset=utf-8",
      JSON.stringify(searchIndex),
      IMMUTABLE,
    );
  }
  if (p === "/help/sitemap.xml") return end(res, 200, "application/xml", sitemap());
  if (p !== "/help" && !p.startsWith("/help/")) return end(res, 404, "text/plain", "not found");

  const key = p === "/help/search" ? null : p;
  let hit = key ? pages.get(key) : undefined;
  if (!hit) {
    const { status, html } = renderHelpPage(p, url.search, {
      ...readShellAssets(),
      enhanceSrc: `/help/_assets/enhance.js?v=${buildId}`,
      baseUrl: BASE_URL,
    });
    hit = {
      status,
      html,
      etag: `"${createHash("sha256").update(html).digest("hex").slice(0, 16)}"`,
    };
    if (key) pages.set(key, hit);
  }

  if (req.headers["if-none-match"] === hit.etag) {
    return end(res, 304, null, "");
  }

  if (req.method === "HEAD") {
    return end(res, hit.status, null, "", {
      ETag: hit.etag,
      "Cache-Control": "public, max-age=60",
      "X-Content-Type-Options": "nosniff",
    });
  }

  end(res, hit.status, "text/html; charset=utf-8", hit.html, {
    ETag: hit.etag,
    "Cache-Control": "public, max-age=60",
    "X-Content-Type-Options": "nosniff",
  });
}).listen(PORT, "0.0.0.0", () => {
  console.log(`[help] listening on :${PORT}, static root ${STATIC_ROOT}`);
});
