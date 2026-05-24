import type { ReactNode } from "react";

export function EmptyState({
  title,
  description,
  action,
  icon,
  inset,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  icon?: ReactNode;
  /** when true, removes the outer dashed border — for use inside Surfaces */
  inset?: boolean;
}) {
  return (
    <div
      className={[
        "flex flex-col items-center justify-center text-center",
        "px-6 py-12 text-muted",
        inset
          ? ""
          : "rounded-md border border-dashed border-[var(--border)] bg-[var(--surface)]",
      ].join(" ")}
    >
      {icon ? (
        <div className="mb-3 grid h-12 w-12 place-items-center rounded-full bg-[var(--surface-secondary)] text-fg/60">
          {icon}
        </div>
      ) : null}
      <div className="text-[14px] font-semibold text-fg">{title}</div>
      {description ? (
        <div className="mt-1 max-w-[42ch] text-[12.5px]">{description}</div>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}
