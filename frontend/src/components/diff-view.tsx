/**
 * Side-by-side unified-diff renderer. Accepts the `patch` string from
 * MRDiffEntry (a single file's unified diff including @@ hunk headers).
 *
 * No syntax highlighting — just coloured gutters per line.
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

const LINE_BG: Record<DiffLine["kind"], string> = {
  context: "",
  add:    "bg-[color-mix(in_oklch,var(--success)_14%,transparent)]",
  del:    "bg-[color-mix(in_oklch,var(--danger)_14%,transparent)]",
  hunk:   "bg-[var(--surface-secondary)] text-muted",
  meta:   "text-muted",
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
    return <div className="px-3 py-2 text-[12px] text-muted">（无差异）</div>;
  }
  return (
    <div className="overflow-auto rounded-md border border-[var(--border)] bg-[var(--surface)] font-mono text-[12px] leading-[1.55]">
      {lines.map((l, i) => (
        <div
          key={i}
          className={["grid grid-cols-[3rem_3rem_1.25rem_1fr]", LINE_BG[l.kind]].join(" ")}
        >
          <span className="px-1.5 text-right tabular-nums text-muted/80">{l.oldNo ?? ""}</span>
          <span className="px-1.5 text-right tabular-nums text-muted/80">{l.newNo ?? ""}</span>
          <span className="text-center text-muted/80">{MARK[l.kind]}</span>
          <span className="whitespace-pre-wrap break-all px-1.5">{l.text}</span>
        </div>
      ))}
    </div>
  );
}
