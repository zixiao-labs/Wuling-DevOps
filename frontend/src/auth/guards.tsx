import { Navigate, useLocation } from "chen-the-dawnstreak";
import type { ReactNode } from "react";
import { authStore } from "./store";

/**
 * Wraps protected sub-trees: if no token in the store, redirect to /login
 * preserving the original location for post-login restoration.
 */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { token } = authStore.useStore();
  const location = useLocation();
  if (!token) {
    return <Navigate to="/login" state={{ from: location.pathname + location.search }} replace />;
  }
  return <>{children}</>;
}

/** Convenience for "if already logged in, bounce away from /login". */
export function RequireAnon({ children, to = "/" }: { children: ReactNode; to?: string }) {
  const { token } = authStore.useStore();
  if (token) return <Navigate to={to} replace />;
  return <>{children}</>;
}
