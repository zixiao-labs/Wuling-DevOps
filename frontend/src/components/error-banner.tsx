import { ApiError } from "@/api/errors";

export function ErrorBanner({ error }: { error: unknown }) {
  if (!error) return null;
  const msg =
    error instanceof ApiError
      ? `${error.code}: ${error.message}`
      : error instanceof Error
        ? error.message
        : String(error);
  return (
    <div
      role="alert"
      style={{
        padding: "0.75rem 1rem",
        border: "1px solid var(--danger)",
        background: "color-mix(in oklab, var(--danger) 8%, var(--surface))",
        color: "var(--danger)",
        borderRadius: "var(--radius)",
        margin: "0.5rem 0",
        fontSize: "0.9rem",
      }}
    >
      {msg}
    </div>
  );
}
