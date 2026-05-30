import { Navigate } from "chen-the-dawnstreak";

/**
 * `/admin` index — bounce to the primary admin section (user approval).
 *
 * `/admin` has no landing of its own. Without this, the file router emits a
 * `<Route path="admin">` with no element (only the `users` / `oauth-apps`
 * children), so bare `/admin` matches an empty leaf and renders a blank page.
 * The target (`/admin/users`) owns the auth gate (RequireAuth + admin check),
 * so logged-out users land on /login and non-admins bounce to /orgs.
 */
export default function AdminIndexRedirect() {
  return <Navigate to="/admin/users" replace />;
}
