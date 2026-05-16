/**
 * Relative-time pill ("3 days ago"), updates lazily on remount.
 * Uses native Intl.RelativeTimeFormat — no extra dep.
 */

import { useMemo } from "react";

const rtf = typeof Intl !== "undefined" ? new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" }) : null;

const UNITS: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
  ["second", 1],
];

export function relativeTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const diff = Math.round((t - Date.now()) / 1000);
  const abs = Math.abs(diff);
  for (const [unit, secs] of UNITS) {
    if (abs >= secs || unit === "second") {
      return rtf ? rtf.format(Math.round(diff / secs), unit) : `${Math.round(diff / secs)} ${unit}`;
    }
  }
  return iso;
}

export function RelativeTime({ iso, title }: { iso: string | null | undefined; title?: boolean }) {
  const formatted = useMemo(() => relativeTime(iso), [iso]);
  return <time title={title !== false ? iso ?? "" : undefined}>{formatted}</time>;
}
