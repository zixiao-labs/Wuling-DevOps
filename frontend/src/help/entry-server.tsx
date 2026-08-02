import { renderToHTML, ChenSSRRouter } from "chen-the-dawnstreak/ssr";
import { escapeHtml } from "@/lib/markdown-core.js";
import { HelpRoutes } from "./routes";
import { lookupDoc } from "./lookup";

export interface ShellAssets {
  cssHref: string;
  themeSrc: string | null;
  enhanceSrc: string;
  baseUrl: string;
}

export function renderHelpPage(pathname: string, search: string, assets: ShellAssets) {
  const doc = lookupDoc(pathname);
  const status = pathname.startsWith("/help/search") ? 200 : doc ? 200 : 404;
  const title = doc
    ? `${doc.title} · 武陵 DevOps 帮助中心`
    : "页面不存在 · 武陵 DevOps 帮助中心";

  const raw = renderToHTML(
    <ChenSSRRouter location={pathname + search}>
      <HelpRoutes />
    </ChenSSRRouter>,
    {
      lang: "zh-CN",
      title: escapeHtml(title),
      cssHref: assets.cssHref,
      clientEntry: assets.enhanceSrc,
      rootId: "help-root",
      headTags: [
        `<meta name="description" content="${escapeHtml(doc?.description ?? "武陵 DevOps 帮助中心")}">`,
        `<link rel="canonical" href="${assets.baseUrl}${pathname}">`,
        `<meta property="og:title" content="${escapeHtml(title)}">`,
        `<meta property="og:type" content="article">`,
        `<meta property="og:image" content="${assets.baseUrl}/og-image.png">`,
        `<link rel="icon" href="/favicon.ico" sizes="48x48">`,
        assets.themeSrc ? `<script src="${assets.themeSrc}"></script>` : "",
        status === 404 ? `<meta name="robots" content="noindex">` : "",
      ].filter(Boolean),
    },
  );

  const patched = raw.replace("<html lang=", '<html data-theme="clean" lang=');
  if (patched === raw) {
    throw new Error("[help] failed to patch <html> tag for data-theme — chen SSR shell changed");
  }
  return { status, html: patched };
}
