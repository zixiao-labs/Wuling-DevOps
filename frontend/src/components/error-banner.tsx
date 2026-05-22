import { ApiError } from "@/api/errors";

/**
 * Inline error banner used by forms and lists.
 *
 * Coloured with `--danger` tones via Tailwind `color-mix(...)` arbitrary
 * values so it tracks the active theme without us hand-wiring a soft red.
 */
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
      className={[
        "my-2 flex items-start gap-2 rounded-md px-3 py-2 text-[12.5px]",
        "border border-[color-mix(in_oklch,var(--danger)_45%,transparent)]",
        "bg-[color-mix(in_oklch,var(--danger)_10%,var(--surface))]",
        "text-[var(--danger)]",
      ].join(" ")}
    >
      <span aria-hidden className="mt-0.5 inline-block h-4 w-4 shrink-0 rounded-full bg-[var(--danger)] text-center text-[10px] font-bold leading-4 text-[var(--danger-foreground)]">
        !
      </span>
      <span className="min-w-0 flex-1 break-words leading-relaxed">{msg}</span>
    </div>
  );
}
