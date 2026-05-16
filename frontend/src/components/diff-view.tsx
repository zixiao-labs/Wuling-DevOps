/**
 * Side-by-side unified-diff renderer. Accepts the `patch` string from
 * MRDiffEntry (a single file's unified diff including @@ hunk headers).
 *
 * No syntax highlighting — just colored gutters per line. About 100 lines on
 * purpose: pulls in zero deps and behaves predictably for big diffs.
 */

import { useMemo } from "react";

interface DiffLine {
  kind: "context" | "add" | "del" | "hunk" | "meta";
  text: string;
  oldNo?: number;
  newNo?: number;
}

function parsePatch(patch: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldNo = 0;
  let newNo = 0;
  for (const line of patch.split("\n")) {
    if (line.startsWith("@@")) {
      const m = /@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/.exec(line);
      if (m) {
        oldNo = parseInt(m[1]!, 10);
        newNo = parseInt(m[2]!, 10);
      }
      out.push({ kind: "hunk", text: line });
      continue;
    }
    if (line.startsWith("+++") || line.startsWith("---") || line.startsWith("diff ") || line.startsWith("index ")) {
      out.push({ kind: "meta", text: line });
      continue;
    }
    if (line.startsWith("+")) {
      out.push({ kind: "add", text: line.slice(1), newNo: newNo++ });
    } else if (line.startsWith("-")) {
      out.push({ kind: "del", text: line.slice(1), oldNo: oldNo++ });
    } else {
      out.push({ kind: "context", text: line.replace(/^ /, ""), oldNo: oldNo++, newNo: newNo++ });
    }
  }
  return out;
}

const STYLES: Record<DiffLine["kind"], React.CSSProperties> = {
  context: { background: "transparent" },
  add: { background: "color-mix(in oklab, var(--success) 12%, var(--surface))" },
  del: { background: "color-mix(in oklab, var(--danger) 12%, var(--surface))" },
  hunk: { background: "var(--surface-secondary)", color: "var(--muted)" },
  meta: { color: "var(--muted)" },
};

const MARK: Record<DiffLine["kind"], string> = {
  context: " ",
  add: "+",
  del: "−",
  hunk: "@",
  meta: " ",
};

export function DiffView({ patch }: { patch: string }) {
  const lines = useMemo(() => parsePatch(patch ?? ""), [patch]);
  if (lines.length === 0) {
    return <div style={{ color: "var(--muted)", padding: "0.5rem" }}>（无差异）</div>;
  }
  return (
    <div
      style={{
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
        fontSize: "0.8rem",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
        overflow: "auto",
        background: "var(--surface)",
      }}
    >
      {lines.map((l, i) => (
        <div
          key={i}
          style={{
            display: "grid",
            gridTemplateColumns: "3rem 3rem 1.25rem 1fr",
            ...STYLES[l.kind],
          }}
        >
          <span style={{ color: "var(--muted)", textAlign: "right", padding: "0 0.4rem" }}>
            {l.oldNo ?? ""}
          </span>
          <span style={{ color: "var(--muted)", textAlign: "right", padding: "0 0.4rem" }}>
            {l.newNo ?? ""}
          </span>
          <span style={{ color: "var(--muted)", textAlign: "center" }}>{MARK[l.kind]}</span>
          <span style={{ whiteSpace: "pre-wrap", padding: "0 0.4rem" }}>{l.text}</span>
        </div>
      ))}
    </div>
  );
}
