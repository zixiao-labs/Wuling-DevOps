/**
 * build/help-docs-plugin.js — compile src/help/content markdown at build time.
 * Serves virtual:help-docs and virtual:help-search-index; dev SSR middleware.
 */

import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { renderDoc, parseFrontMatter, toPlainText } from "../src/lib/markdown-core.js";

const DOCS = "virtual:help-docs";
const INDEX = "virtual:help-search-index";

function fileToSlug(rel) {
  const noExt = rel.replace(/\.md$/, "");
  if (noExt === "index") return "";
  if (noExt.endsWith("/index")) return noExt.slice(0, -"/index".length);
  return noExt;
}

function walkMd(dir, base = dir) {
  const out = [];
  if (!fs.existsSync(dir)) return out;
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) out.push(...walkMd(full, base));
    else if (ent.name.endsWith(".md")) out.push(path.relative(base, full));
  }
  return out;
}

const DEV_ASSETS = {
  cssHref: "/src/styles/globals.css",
  themeSrc: "/src/theme-init.js",
  enhanceSrc: "/src/help/client/enhance.ts",
  baseUrl: "",
};

export function helpDocsPlugin({ contentDir = "src/help/content" } = {}) {
  let dir;
  let cache = null;

  function compile() {
    const files = walkMd(dir);
    const docs = [];
    for (const rel of files) {
      const abs = path.join(dir, rel);
      const raw = fs.readFileSync(abs, "utf8");
      const { meta, body } = parseFrontMatter(raw);
      const { html, headings } = renderDoc(body, {
        links: true,
        headingIds: true,
        hr: true,
        tables: true,
      });
      const stat = fs.statSync(abs);
      docs.push({
        slug: fileToSlug(rel.replace(/\\/g, "/")),
        title: String(meta.title ?? rel),
        group: String(meta.group ?? "其他"),
        order: Number(meta.order ?? 999),
        description: String(meta.description ?? ""),
        html,
        headings,
        updatedAt: stat.mtime.toISOString(),
      });
    }

    docs.sort((a, b) => {
      if (a.group !== b.group) return a.group.localeCompare(b.group, "zh-CN");
      if (a.order !== b.order) return a.order - b.order;
      return a.title.localeCompare(b.title, "zh-CN");
    });

    const groupMap = new Map();
    for (const d of docs) {
      if (!groupMap.has(d.group)) groupMap.set(d.group, []);
      groupMap.get(d.group).push({ slug: d.slug, title: d.title });
    }
    const nav = [...groupMap.entries()].map(([group, items]) => ({ group, items }));

    const searchIndex = docs.map((d) => ({
      slug: d.slug,
      title: d.title,
      group: d.group,
      description: d.description,
      text: toPlainText(fs.readFileSync(path.join(dir, slugToFile(d.slug)), "utf8")),
    }));

    const buildId = crypto
      .createHash("sha256")
      .update(JSON.stringify(docs.map((d) => d.slug)))
      .digest("hex")
      .slice(0, 12);

    return { docs, nav, searchIndex, buildId };
  }

  function slugToFile(slug) {
    if (slug === "") return "index.md";
    return `${slug}.md`;
  }

  return {
    name: "wuling:help-docs",
    enforce: "pre",
    configResolved(config) {
      dir = path.resolve(config.root, contentDir);
    },
    buildStart() {
      cache = null;
    },
    resolveId(s) {
      if (s === DOCS || s === INDEX) return "\0" + s;
      return null;
    },
    load(id) {
      if (id !== "\0" + DOCS && id !== "\0" + INDEX) return null;
      cache ??= compile();
      const payload =
        id === "\0" + DOCS
          ? { docs: cache.docs, nav: cache.nav, buildId: cache.buildId }
          : cache.searchIndex;
      return `export default /* @__PURE__ */ JSON.parse(${JSON.stringify(JSON.stringify(payload))});`;
    },
    configureServer(server) {
      if (!fs.existsSync(dir)) return;
      server.watcher.add(dir);
      for (const ev of ["add", "change", "unlink"]) {
        server.watcher.on(ev, (f) => {
          if (f.startsWith(dir)) {
            cache = null;
            server.ws.send({ type: "full-reload" });
          }
        });
      }
      server.middlewares.use(async (req, res, next) => {
        if (!/^\/help(\/|$)/.test(req.url ?? "")) return next();
        try {
          const m = await server.ssrLoadModule("/src/help/entry-server.tsx");
          const url = new URL(req.url ?? "/", "http://localhost");
          const { status, html } = m.renderHelpPage(url.pathname, url.search, DEV_ASSETS);
          res.writeHead(status, { "Content-Type": "text/html; charset=utf-8" }).end(html);
        } catch (err) {
          next(err);
        }
      });
    },
  };
}
