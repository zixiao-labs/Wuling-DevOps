import { Spinner } from "@heroui/react";

export function Loading({ label = "加载中…" }: { label?: string }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        gap: "0.75rem",
        padding: "2rem",
        color: "var(--muted)",
      }}
    >
      <Spinner size="sm" />
      <span>{label}</span>
    </div>
  );
}
