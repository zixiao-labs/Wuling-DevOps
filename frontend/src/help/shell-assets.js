import { readFileSync, existsSync } from "node:fs";
import path from "node:path";

let warnedAssets = false;

/** @param {string} html @param {Partial<import("./shell-assets.d.ts").ShellAssets>} [overrides] */
export function readShellAssetsFromHtml(html, overrides = {}) {
  const cssMatch = html.match(/<link[^>]+rel=["']stylesheet["'][^>]+href=["']([^"']+)["']/);
  const themeMatch = html.match(/<script[^>]+src=["'](\/assets\/theme-init[^"']+\.js)["']/);
  return {
    cssHref: overrides.cssHref ?? cssMatch?.[1] ?? "/assets/main.css",
    themeSrc:
      overrides.themeSrc !== undefined ? overrides.themeSrc : (themeMatch?.[1] ?? "/assets/theme-init.js"),
    enhanceSrc: overrides.enhanceSrc ?? "/help/_assets/enhance.js",
    baseUrl: overrides.baseUrl ?? "",
  };
}

/** @param {string} distDir @param {Partial<import("./shell-assets.d.ts").ShellAssets>} [overrides] */
export function readShellAssetsFromDist(distDir, overrides = {}) {
  if (process.env.WULING_HELP_CSS_HREF) {
    return {
      cssHref: process.env.WULING_HELP_CSS_HREF,
      themeSrc: process.env.WULING_HELP_THEME_SRC ?? null,
      enhanceSrc: overrides.enhanceSrc ?? "/help/_assets/enhance.js",
      baseUrl: overrides.baseUrl ?? process.env.WULING_HELP_BASE_URL ?? "",
    };
  }
  const indexPath = path.join(distDir, "index.html");
  if (!existsSync(indexPath)) {
    if (!warnedAssets) {
      console.warn("[help] index.html not found at", indexPath, "— using fallback asset URLs");
      warnedAssets = true;
    }
    return readShellAssetsFromHtml("", overrides);
  }
  const html = readFileSync(indexPath, "utf8");
  const assets = readShellAssetsFromHtml(html, overrides);
  if (!assets.cssHref.includes("/assets/") && !warnedAssets) {
    console.warn("[help] could not parse stylesheet href from index.html");
    warnedAssets = true;
  }
  return assets;
}
