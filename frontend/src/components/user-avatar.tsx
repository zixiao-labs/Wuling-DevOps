import { useEffect, useState } from "react";

import type { UserRef, User } from "@/api/types";

/** Lightweight superset of UserRef so callers can pass either shape. */
type AvatarLike =
  | (Pick<UserRef, "username" | "display_name"> & { avatar_url?: string | null })
  | (Pick<User, "username" | "display_name" | "avatar_url">)
  | null
  | undefined;

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
  user: AvatarLike;
  size?: number;
}) {
  const name = user?.display_name || user?.username || "?";
  const h = hueFor(name);
  const url = (user as { avatar_url?: string | null } | null)?.avatar_url ?? "";
  // When the image fails to load (404, network error, or simply no uploaded
  // avatar), fall back to the initials tile. Track failure locally so we don't
  // retry on every re-render. Reset when the url changes so a fresh upload
  // gets a chance to load.
  const [imgFailed, setImgFailed] = useState(false);
  useEffect(() => {
    setImgFailed(false);
  }, [url]);
  const showImage = !!url && !imgFailed;

  if (showImage) {
    return (
      <img
        src={url}
        alt={name}
        title={user?.username}
        loading="lazy"
        onError={() => setImgFailed(true)}
        className="inline-block shrink-0 rounded-full object-cover ring-1 ring-black/5"
        style={{ width: size, height: size }}
      />
    );
  }

  return (
    <span
      title={user?.username}
      className="inline-flex shrink-0 items-center justify-center rounded-full font-semibold text-[#1a1a1a] ring-1 ring-black/5"
      style={{
        width: size,
        height: size,
        background: `oklch(74% 0.11 ${h})`,
        fontSize: `${Math.max(9, size * 0.4)}px`,
        letterSpacing: "0.02em",
      }}
    >
      {initials(name)}
    </span>
  );
}
