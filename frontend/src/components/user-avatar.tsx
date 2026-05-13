import type { UserRef } from "@/api/types";

function initials(s: string): string {
  if (!s) return "?";
  const parts = s.trim().split(/\s+/);
  if (parts.length >= 2) return (parts[0]![0]! + parts[1]![0]!).toUpperCase();
  return s.slice(0, 2).toUpperCase();
}

// Stable hue derived from the name so the same user always gets the same color.
function hueFor(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h % 360;
}

export function UserAvatar({
  user,
  size = 24,
}: {
  user: Pick<UserRef, "username" | "display_name"> | null | undefined;
  size?: number;
}) {
  const name = user?.display_name || user?.username || "?";
  const h = hueFor(name);
  return (
    <span
      title={user?.username}
      style={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        width: size,
        height: size,
        borderRadius: "50%",
        background: `oklch(70% 0.12 ${h})`,
        color: "#1a1a1a",
        fontWeight: 600,
        fontSize: `${size * 0.4}px`,
        flex: "0 0 auto",
      }}
    >
      {initials(name)}
    </span>
  );
}
