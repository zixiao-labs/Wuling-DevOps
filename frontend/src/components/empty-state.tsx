import type { ReactNode } from "react";

export function EmptyState({
  title,
  description,
  action,
  icon,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div
      style={{
        padding: "3rem 1rem",
        textAlign: "center",
        color: "var(--muted)",
        border: "1px dashed var(--border)",
        borderRadius: "var(--radius)",
        background: "var(--surface)",
      }}
    >
      {icon ? <div style={{ marginBottom: "0.75rem", opacity: 0.8 }}>{icon}</div> : null}
      <div style={{ color: "var(--foreground)", fontWeight: 600, marginBottom: "0.25rem" }}>
        {title}
      </div>
      {description ? <div style={{ fontSize: "0.9rem" }}>{description}</div> : null}
      {action ? <div style={{ marginTop: "1rem" }}>{action}</div> : null}
    </div>
  );
}
