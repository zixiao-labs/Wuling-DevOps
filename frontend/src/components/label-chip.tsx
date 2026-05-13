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
}: {
  label: Label;
  onClick?: () => void;
  onRemove?: () => void;
}) {
  const bg = normalizeHex(label.color);
  const fg = isLight(bg) ? "#1a1a1a" : "#fafafa";
  return (
    <span
      onClick={onClick}
      title={label.description || label.name}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: "0.25rem",
        background: bg,
        color: fg,
        padding: "0.1rem 0.5rem",
        borderRadius: "999px",
        fontSize: "0.75rem",
        cursor: onClick ? "pointer" : "default",
        userSelect: "none",
      }}
    >
      {label.name}
      {onRemove ? (
        <button
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          style={{
            border: "none",
            background: "transparent",
            color: fg,
            cursor: "pointer",
            padding: 0,
            lineHeight: 1,
          }}
          aria-label={`Remove ${label.name}`}
        >
          ×
        </button>
      ) : null}
    </span>
  );
}
