import type { Label } from "@/api/types";

function normalizeHex(c: string): string {
  if (!c) return "#888888";
  return c.startsWith("#") ? c : `#${c}`;
}

// Approximate brightness from a 6-digit hex.
function isLight(hex: string): boolean {
  const h = hex.replace("#", "");
  if (h.length !== 6) return false;
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return (r * 0.299 + g * 0.587 + b * 0.114) > 160;
}

export function LabelChip({
  label,
  onClick,
  onRemove,
  size = "md",
}: {
  label: Label;
  onClick?: () => void;
  onRemove?: () => void;
  size?: "sm" | "md";
}) {
  const bg = normalizeHex(label.color);
  const fg = isLight(bg) ? "#1a1a1a" : "#fafafa";
  const padding = size === "sm" ? "px-1.5 py-0 text-[10.5px]" : "px-2 py-px text-[11.5px]";
  return (
    <span
      onClick={onClick}
      onKeyDown={
        onClick
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick();
              }
            }
          : undefined
      }
      role={onClick ? "button" : undefined}
      tabIndex={onClick ? 0 : undefined}
      title={label.description || label.name}
      className={[
        "inline-flex items-center gap-1 select-none rounded-full font-medium leading-none ring-1 ring-inset ring-black/10",
        padding,
        onClick ? "cursor-pointer hover:brightness-110" : "cursor-default",
      ].join(" ")}
      style={{ background: bg, color: fg }}
    >
      <span aria-hidden className="inline-block h-1.5 w-1.5 rounded-full" style={{ background: fg, opacity: 0.45 }} />
      <span>{label.name}</span>
      {onRemove ? (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className="ml-0.5 grid h-3.5 w-3.5 place-items-center rounded-full leading-none hover:bg-black/15"
          style={{ color: fg }}
          aria-label={`Remove ${label.name}`}
        >
          ×
        </button>
      ) : null}
    </span>
  );
}
