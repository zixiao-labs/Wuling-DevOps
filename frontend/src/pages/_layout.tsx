/**
 * Root layout. Picks between the authenticated AppShell and a minimal
 * unauthenticated chrome depending on the current route.
 *
 * /login and /register render against a plain background so the auth pages
 * can take the full viewport. Everything else (including the OAuth consent
 * flows which can be visited by signed-in users) renders inside the AppShell.
 */

import { Outlet, useLocation } from "chen-the-dawnstreak";

import { AppShell } from "@/components/shell/app-shell";

export default function RootLayout() {
  const { pathname } = useLocation();
  // Auth-chrome routes: the user isn't (necessarily) signed in yet, and the
  // page itself wants the full viewport for a centred login/redirect card.
  const isAuthRoute =
    pathname === "/login" ||
    pathname === "/register" ||
    pathname === "/oauth/callback";

  if (isAuthRoute) {
    return <AuthChrome />;
  }
  return <AppShell />;
}

/**
 * Background for /login + /register. A subtle layered gradient that picks up
 * the active theme's accent, with a faint grid overlay so the page never
 * looks like a flat-white admin form.
 */
function AuthChrome() {
  return (
    <div className="relative grid min-h-full place-items-center overflow-hidden bg-bg px-4 py-10 text-fg">
      {/* radial wash anchored to the accent */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0"
        style={{
          backgroundImage:
            "radial-gradient(circle at 22% 18%, color-mix(in oklch, var(--accent) 18%, transparent) 0%, transparent 38%), " +
            "radial-gradient(circle at 82% 86%, color-mix(in oklch, var(--accent) 12%, transparent) 0%, transparent 42%)",
        }}
      />
      {/* faint grid */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 opacity-[0.18]"
        style={{
          backgroundImage:
            "linear-gradient(var(--border) 1px, transparent 1px), linear-gradient(90deg, var(--border) 1px, transparent 1px)",
          backgroundSize: "48px 48px",
          maskImage:
            "radial-gradient(ellipse at center, black 35%, transparent 75%)",
        }}
      />
      <div className="relative z-10 w-full max-w-[420px]">
        <BrandLockup />
        <Outlet />
        <div className="mt-6 text-center text-[11px] text-muted">
          武陵 DevOps · 紫霄实验室
        </div>
      </div>
    </div>
  );
}

function BrandLockup() {
  return (
    <div className="mb-7 flex flex-col items-center gap-2">
      <img
        src="/brand-wordmark.png"
        alt="武陵 DevOps"
        width={130}
        height={60}
        className="h-[38px] w-auto"
      />
      <span className="text-[11px] text-muted">Stage 2 · 紫霄实验室</span>
    </div>
  );
}
