import { Navigate } from "chen-the-dawnstreak";

/**
 * `/settings` index — bounce to the first settings section (profile).
 *
 * Without this, `/settings` renders `_layout.tsx` with an empty `<Outlet/>`
 * (the settings chrome with a blank main area). This index renders inside that
 * already-auth-guarded layout and forwards to `/settings/profile`.
 */
export default function SettingsIndexRedirect() {
  return <Navigate to="/settings/profile" replace />;
}
