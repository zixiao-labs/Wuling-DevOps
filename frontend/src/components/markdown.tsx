/**
 * Minimal markdown renderer for issue/MR bodies (server returns raw markdown
 * for these, only Wiki gets sanitized HTML server-side).
 *
 * Implementation lives in @/lib/markdown-core.js — this wrapper keeps the
 * React component API unchanged.
 */

import { useMemo } from "react";
import { renderBlocks } from "@/lib/markdown-core.js";

export function Markdown({ source }: { source: string }) {
  const html = useMemo(() => renderBlocks(source ?? ""), [source]);
  return (
    <div
      className="wuling-prose"
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

/** For wiki pages: the server already returns sanitized HTML. */
export function RawMarkdownHtml({ html }: { html: string }) {
  return <div className="wuling-prose" dangerouslySetInnerHTML={{ __html: html }} />;
}
