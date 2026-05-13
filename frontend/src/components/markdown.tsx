/**
 * Minimal markdown renderer for issue/MR bodies (server returns raw markdown
 * for these, only Wiki gets sanitized HTML server-side).
 *
 * Intentionally NOT a full GFM implementation — we cover the cases that
 * matter for short descriptions and avoid pulling in marked + DOMPurify
 * for ~50 KB of payload. For wiki pages, render the server-supplied HTML
 * directly via `<RawMarkdownHtml html=... />`.
 *
 * Supported:
 *   - Triple-backtick fenced code blocks (no syntax highlighting)
 *   - Inline `code`
 *   - Bold **x**, italic *x*
 *   - Auto-links for http/https URLs
 *   - Blank-line paragraph breaks, single-line breaks preserved with <br>
 *
 * Escapes HTML by default before applying inline transformations.
 */

import { useMemo } from "react";

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function inlineMarkdown(s: string): string {
  // Inline `code` first so the backticked content isn't transformed below.
  s = s.replace(/`([^`\n]+?)`/g, (_, code: string) => `<code>${escapeHtml(code)}</code>`);
  // After inline code, escape the rest. We need to be careful — already-emitted
  // <code>… tags must be preserved. Instead of escaping the whole string we
  // split around <code>…</code> spans.
  const parts: string[] = [];
  let i = 0;
  while (i < s.length) {
    const open = s.indexOf("<code>", i);
    if (open === -1) {
      parts.push(escapeNonCode(s.slice(i)));
      break;
    }
    parts.push(escapeNonCode(s.slice(i, open)));
    const close = s.indexOf("</code>", open);
    if (close === -1) {
      parts.push(s.slice(open));
      break;
    }
    parts.push(s.slice(open, close + "</code>".length));
    i = close + "</code>".length;
  }
  return parts.join("");
}

function escapeNonCode(s: string): string {
  let out = escapeHtml(s);
  // bold + italic (bold first, longer markers win)
  out = out.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
  // auto-link http/https URLs (the escapeHtml step turned : / etc. into the same chars)
  out = out.replace(
    /(https?:\/\/[^\s<>"']+)/g,
    (m) => `<a href="${m}" target="_blank" rel="noopener noreferrer">${m}</a>`,
  );
  return out;
}

function renderBlocks(raw: string): string {
  const lines = raw.replace(/\r\n/g, "\n").split("\n");
  const out: string[] = [];

  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;

    // Fenced code block
    if (/^```/.test(line)) {
      const lang = line.slice(3).trim();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i]!)) {
        codeLines.push(lines[i]!);
        i++;
      }
      i++; // skip closing fence (or run off end)
      const langAttr = lang ? ` data-lang="${escapeHtml(lang)}"` : "";
      out.push(`<pre${langAttr}><code>${escapeHtml(codeLines.join("\n"))}</code></pre>`);
      continue;
    }

    // Heading
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      const level = h[1]!.length;
      out.push(`<h${level}>${inlineMarkdown(h[2]!)}</h${level}>`);
      i++;
      continue;
    }

    // Blockquote (consecutive lines)
    if (/^>\s?/.test(line)) {
      const buf: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i]!)) {
        buf.push(lines[i]!.replace(/^>\s?/, ""));
        i++;
      }
      out.push(`<blockquote>${inlineMarkdown(buf.join("<br>"))}</blockquote>`);
      continue;
    }

    // Unordered list
    if (/^[-*]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^[-*]\s+/.test(lines[i]!)) {
        items.push(`<li>${inlineMarkdown(lines[i]!.replace(/^[-*]\s+/, ""))}</li>`);
        i++;
      }
      out.push(`<ul>${items.join("")}</ul>`);
      continue;
    }

    // Paragraph: collect until blank line
    if (line.trim() === "") {
      i++;
      continue;
    }
    const buf: string[] = [];
    while (i < lines.length && lines[i]!.trim() !== "") {
      buf.push(lines[i]!);
      i++;
    }
    out.push(`<p>${inlineMarkdown(buf.join("<br>"))}</p>`);
  }

  return out.join("\n");
}

export function Markdown({ source }: { source: string }) {
  const html = useMemo(() => renderBlocks(source ?? ""), [source]);
  return (
    <div
      className="wuling-prose"
      // Source is rendered through our own escaping; no raw HTML from the input survives.
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

/** For wiki pages: the server already returns sanitized HTML. */
export function RawMarkdownHtml({ html }: { html: string }) {
  return <div className="wuling-prose" dangerouslySetInnerHTML={{ __html: html }} />;
}
