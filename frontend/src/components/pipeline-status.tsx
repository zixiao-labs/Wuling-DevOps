/**
 * pipeline-status.tsx — status chips for pipeline runs / jobs / steps.
 * Reuses the tonal Pill from page/badges so colours stay consistent.
 */

import CircleFill from "@gravity-ui/icons/CircleFill";
import CircleCheckFill from "@gravity-ui/icons/CircleCheckFill";
import CircleXmarkFill from "@gravity-ui/icons/CircleXmarkFill";
import Clock from "@gravity-ui/icons/Clock";
import ArrowsRotateRight from "@gravity-ui/icons/ArrowsRotateRight";
import Ban from "@gravity-ui/icons/Ban";

import { Pill } from "@/components/page/badges";
import type { RunStatus, StepStatus } from "@/api/types";

const RUN_META: Record<RunStatus, { tone: "success" | "danger" | "info" | "neutral" | "warning"; label: string }> = {
  queued: { tone: "neutral", label: "排队中" },
  running: { tone: "info", label: "运行中" },
  success: { tone: "success", label: "成功" },
  failed: { tone: "danger", label: "失败" },
  canceled: { tone: "neutral", label: "已取消" },
};

export function RunStatusBadge({ status }: { status: RunStatus }) {
  const meta = RUN_META[status] ?? RUN_META.queued;
  const icon =
    status === "success" ? <CircleCheckFill width={10} height={10} /> :
    status === "failed" ? <CircleXmarkFill width={10} height={10} /> :
    status === "running" ? <ArrowsRotateRight width={10} height={10} /> :
    status === "canceled" ? <Ban width={10} height={10} /> :
    <Clock width={10} height={10} />;
  return (
    <Pill tone={meta.tone} icon={icon}>
      {meta.label}
    </Pill>
  );
}

/** A small status dot for a single step, with an accessible title. */
export function StepStatusIcon({ status, size = 13 }: { status: StepStatus; size?: number }) {
  const common = { width: size, height: size };
  switch (status) {
    case "success":
      return <CircleCheckFill {...common} className="text-[var(--success)]" aria-label="成功" />;
    case "failed":
      return <CircleXmarkFill {...common} className="text-[var(--danger)]" aria-label="失败" />;
    case "running":
      return <ArrowsRotateRight {...common} className="text-[var(--info)] animate-spin" aria-label="运行中" />;
    case "skipped":
      return <Ban {...common} className="text-muted" aria-label="跳过" />;
    case "canceled":
      return <Ban {...common} className="text-muted" aria-label="已取消" />;
    default:
      return <CircleFill {...common} className="text-muted/50" aria-label="排队中" />;
  }
}
