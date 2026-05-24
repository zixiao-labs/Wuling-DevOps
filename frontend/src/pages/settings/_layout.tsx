import { Outlet } from "chen-the-dawnstreak";

import { RequireAuth } from "@/auth/guards";

/**
 * Settings _layout — the contextual sidebar lives in the AppShell, so the
 * settings sub-routes only need to wrap the outlet in an auth guard.
 */
export default function SettingsLayout() {
  return (
    <RequireAuth>
      <Outlet />
    </RequireAuth>
  );
}
