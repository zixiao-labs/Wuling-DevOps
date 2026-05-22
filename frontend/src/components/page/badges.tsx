/**
 * page/badges.tsx — atomic display chips used across pages.
 *
 *   <StateBadge state />     issue / MR state pill (open / closed / merged)
 *   <VisibilityBadge v />    private / internal / public icon + label
 *   <StatusDot tone />       small coloured dot used inline next to titles
 *   <Tag tone />             generic neutral chip
 *   <Pill tone />            small tonal pill (success / warning / danger / info)
 *
 * All tones derive from CSS variables — no inline hex values.
 */

import type { ReactNode } from "react";

import Lock from "@gravity-ui/icons/Lock";
import Globe from "@gravity-ui/icons/Globe";
import Layers from "@gravity-ui/icons/Layers";
import CircleCheckFill from "@gravity-ui/icons/CircleCheckFill";
import CircleFill from "@gravity-ui/icons/CircleFill";
import CodePullRequest from "@gravity-ui/icons/CodePullRequest";

import type { IssueState, MRState, Visibility } from "@/api/types";

/* ---------------------------------------------------------- StateBadge */

export function StateBadge({ state }: { state: IssueState | MRState | "draft" }) {
  if (state === "open") {
    return (
      <Pill tone="success" icon={<CircleFill width={9} height={9} />}>
        Open
      </Pill>
    );
  }
  if (state === "merged") {
    return (
      <Pill tone="info" icon={<CodePullRequest width={11} height={11} />}>
        Merged
      </Pill>
    );
  }
  if (state === "draft") {
    return <Pill tone="neutral">Draft</Pill>;
  }
  return (
    <Pill tone="danger" icon={<CircleCheckFill width={11} height={11} />}>
      Closed
    </Pill>
  );
}

/* ---------------------------------------------------- VisibilityBadge / icon */

const VIS_META: Record<Visibility, { icon: typeof Lock; label: string }> = {
  private: { icon: Lock, label: "私有" },
  internal: { icon: Layers, label: "内部" },
  public: { icon: Globe, label: "公开" },
};

export function VisibilityBadge({
  v,
  hideLabel,
}: {
  v: Visibility;
  hideLabel?: boolean;
}) {
  const meta = VIS_META[v];
  const Icon = meta.icon;
  return (
    <span className="inline-flex items-center gap-1 rounded-sm border border-[var(--border)] bg-[var(--surface-secondary)] px-1.5 py-0.5 text-[11px] text-muted">
      <Icon width={11} height={11} />
      {hideLabel ? null : meta.label}
    </span>
  );
}

export function VisibilityIcon({ v, size = 14 }: { v: Visibility; size?: number }) {
  const Icon = VIS_META[v].icon;
  return <Icon width={size} height={size} aria-label={VIS_META[v].label} />;
}

/* ---------------------------------------------------------- StatusDot */

type Tone = "success" | "warning" | "danger" | "info" | "neutral";

const TONE_DOT: Record<Tone, string> = {
  success: "bg-[var(--success)]",
  warning: "bg-[var(--warning)]",
  danger:  "bg-[var(--danger)]",
  info:    "bg-[var(--accent)]",
  neutral: "bg-[var(--muted)]",
};

export function StatusDot({ tone }: { tone: Tone }) {
  return <span aria-hidden className={["inline-block h-2 w-2 rounded-full", TONE_DOT[tone]].join(" ")} />;
}

/* ---------------------------------------------------------- Pill */

const PILL_STYLES: Record<Tone, string> = {
  success:
    "bg-[color-mix(in_oklch,var(--success)_18%,transparent)] text-[var(--success-foreground)] " +
    "ring-1 ring-inset ring-[color-mix(in_oklch,var(--success)_45%,transparent)]",
  warning:
    "bg-[color-mix(in_oklch,var(--warning)_22%,transparent)] text-[var(--warning-foreground)] " +
    "ring-1 ring-inset ring-[color-mix(in_oklch,var(--warning)_50%,transparent)]",
  danger:
    "bg-[color-mix(in_oklch,var(--danger)_18%,transparent)] text-[var(--danger-foreground)] " +
    "ring-1 ring-inset ring-[color-mix(in_oklch,var(--danger)_45%,transparent)]",
  info:
    "bg-[color-mix(in_oklch,var(--accent)_15%,transparent)] text-[var(--accent-foreground)] " +
    "ring-1 ring-inset ring-[color-mix(in_oklch,var(--accent)_40%,transparent)]",
  neutral:
    "bg-[var(--surface-secondary)] text-fg ring-1 ring-inset ring-[var(--border)]",
};

interface PillProps {
  tone?: Tone;
  icon?: ReactNode;
  children: ReactNode;
  className?: string;
}

export function Pill({ tone = "neutral", icon, children, className }: PillProps) {
  return (
    <span
      className={[
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium leading-none",
        PILL_STYLES[tone],
        className ?? "",
      ].join(" ")}
    >
      {icon ? <span className="shrink-0 leading-none">{icon}</span> : null}
      {children}
    </span>
  );
}

/* ---------------------------------------------------------- Tag (neutral) */

export function Tag({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span
      className={[
        "inline-flex items-center rounded-sm border border-[var(--border)] bg-[var(--surface-secondary)] px-1.5 py-0.5 text-[11px] text-muted",
        className ?? "",
      ].join(" ")}
    >
      {children}
    </span>
  );
}

/* ---------------------------------------------------------- Stat card */

export function Stat({
  label,
  value,
  hint,
}: {
  label: ReactNode;
  value: ReactNode;
  hint?: ReactNode;
}) {
  return (
    <div className="rounded-md border border-[var(--border)] bg-[var(--surface)] px-4 py-3">
      <div className="text-[11px] uppercase tracking-wider text-muted">{label}</div>
      <div className="mt-1 text-[22px] font-semibold tabular-nums leading-none text-fg">{value}</div>
      {hint ? <div className="mt-1.5 text-[11.5px] text-muted">{hint}</div> : null}
    </div>
  );
}
