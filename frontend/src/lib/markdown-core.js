/**
 * markdown-core.js — the repo's single markdown engine. Plain JS so three
 * consumers can share it: src/components/markdown.tsx (browser),
 * build/help-docs-plugin.js (imported from nasti.config.ts → runs in raw Node),
 * and src/help/search.ts.
 */

export function escapeHtml(s) {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/** CJK-safe. "流水线 配置" -> "流水线-配置"; browsers percent-encode in hrefs. */
export function slugify(text) {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, "-")
    .replace(/^-+|-+$/g, "");
}

function inlineMarkdown(s, opts = {}) {
  s = s.replace(/`([^`\n]+?)`/g, (_, code) => `<code>${escapeHtml(code)}</code>`);
  const parts = [];
  let i = 0;
  while (i < s.length) {
    const open = s.indexOf("<code>", i);
    if (open === -1) {
      parts.push(escapeNonCode(s.slice(i), opts));
      break;
    }
    parts.push(escapeNonCode(s.slice(i, open), opts));
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

function escapeNonCode(s, opts = {}) {
  let out = escapeHtml(s);
  out = out.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
  if (opts.links) {
    out = out.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, text, url) => {
      const u = url.trim();
      if (/^javascript:/i.test(u)) return `[${text}](${url})`;
      if (!/^(https?:\/\/|\/)/.test(u)) return `[${text}](${url})`;
      const safe = u.startsWith("/") ? u : u.replace(/"/g, "&quot;");
      return `<a href="${safe}">${text}</a>`;
    });
  }
  out = out.replace(
    /(https?:\/\/[^\s<>"']+)/g,
    (m) => `<a href="${m}" target="_blank" rel="noopener noreferrer">${m}</a>`,
  );
  return out;
}

function parseTableRow(line) {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((c) => c.trim());
}

function isTableSeparator(line) {
  return /^\|?[\s:-]+\|[\s|:-]+\|?$/.test(line.trim());
}

/**
 * @returns {{ html: string, headings: {level:number,id:string,text:string}[] }}
 */
export function renderDoc(raw, opts = {}) {
  const lines = raw.replace(/\r\n/g, "\n").split("\n");
  const out = [];
  const headings = [];

  let i = 0;
  while (i < lines.length) {
    const line = lines[i];

    if (/^```/.test(line)) {
      const lang = line.slice(3).trim();
      const codeLines = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) {
        codeLines.push(lines[i]);
        i++;
      }
      i++;
      const langAttr = lang ? ` data-lang="${escapeHtml(lang)}"` : "";
      out.push(`<pre${langAttr}><code>${escapeHtml(codeLines.join("\n"))}</code></pre>`);
      continue;
    }

    if (opts.hr && /^---+$/.test(line.trim())) {
      out.push("<hr>");
      i++;
      continue;
    }

    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      const level = h[1].length;
      const text = h[2];
      let idAttr = "";
      if (opts.headingIds && level >= 2) {
        const id = slugify(text.replace(/[*_`[\]()]/g, ""));
        idAttr = ` id="${escapeHtml(id)}"`;
        headings.push({ level, id, text: text.replace(/[*_`[\]()]/g, "") });
      }
      out.push(`<h${level}${idAttr}>${inlineMarkdown(text, opts)}</h${level}>`);
      i++;
      continue;
    }

    if (/^>\s?/.test(line)) {
      const buf = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^>\s?/, ""));
        i++;
      }
      out.push(`<blockquote>${inlineMarkdown(buf.join("<br>"), opts)}</blockquote>`);
      continue;
    }

    if (opts.tables && /^\|/.test(line) && i + 1 < lines.length && isTableSeparator(lines[i + 1])) {
      const headerCells = parseTableRow(line);
      i += 2;
      const bodyRows = [];
      while (i < lines.length && /^\|/.test(lines[i])) {
        bodyRows.push(parseTableRow(lines[i]));
        i++;
      }
      const thead = `<thead><tr>${headerCells.map((c) => `<th>${inlineMarkdown(c, opts)}</th>`).join("")}</tr></thead>`;
      const tbody = bodyRows
        .map((row) => `<tr>${row.map((c) => `<td>${inlineMarkdown(c, opts)}</td>`).join("")}</tr>`)
        .join("");
      out.push(`<table>${thead}<tbody>${tbody}</tbody></table>`);
      continue;
    }

    if (/^[-*]\s+/.test(line)) {
      const items = [];
      while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
        items.push(`<li>${inlineMarkdown(lines[i].replace(/^[-*]\s+/, ""), opts)}</li>`);
        i++;
      }
      out.push(`<ul>${items.join("")}</ul>`);
      continue;
    }

    if (line.trim() === "") {
      i++;
      continue;
    }
    const buf = [];
    while (i < lines.length && lines[i].trim() !== "") {
      buf.push(lines[i]);
      i++;
    }
    out.push(`<p>${inlineMarkdown(buf.join("<br>"), opts)}</p>`);
  }

  return { html: out.join("\n"), headings };
}

/** Back-compat: byte-identical to the pre-extraction markdown.tsx output. */
export function renderBlocks(raw) {
  return renderDoc(raw, {}).html;
}

/** Minimal front matter parser: --- k: v --- blocks; no YAML dependency. */
export function parseFrontMatter(raw) {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n([\s\S]*)$/.exec(raw);
  if (!m) return { meta: {}, body: raw };
  const meta = {};
  for (const line of m[1].split("\n")) {
    const kv = /^(\w+):\s*(.*)$/.exec(line.trim());
    if (kv) {
      const val = kv[2].trim();
      meta[kv[1]] = /^\d+$/.test(val) ? Number(val) : val;
    }
  }
  return { meta, body: m[2] };
}

/** Strip markdown markers for search indexing. */
export function toPlainText(raw) {
  const { body } = parseFrontMatter(raw);
  return body
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/`[^`]+`/g, " ")
    .replace(/[#>*\-|]/g, " ")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .replace(/\s+/g, " ")
    .trim();
}
