/**
 * shell/app-shell.tsx — main authenticated chrome.
 *
 * Layout (GitLab 17+ inspired):
 *
 *   ┌────────────┬─────────────────────────────────┐
 *   │ Brand zone │  Top utility bar (search · + · 🌓 · 👤) │  ← 44px
 *   ├──┬─────────┼─────────────────────────────────┤
 *   │  │         │                                 │
 *   │R │ Sidebar │   Outlet (page content)         │
 *   │a │         │                                 │
 *   │i │         │                                 │
 *   │l │         │                                 │
 *   └──┴─────────┴─────────────────────────────────┘
 *
 * Widths:  rail = 48px · sidebar = 248px (collapsible to 0)
 *
 * The sidebar's contextual content lives in `sidebar-content.tsx`. The rail
 * holds global shortcuts (Home / Orgs / Settings / Admin / Help / Theme).
 */

import { useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "chen-the-dawnstreak";

import House from "@gravity-ui/icons/House";
import Layers from "@gravity-ui/icons/Layers";
import Gear from "@gravity-ui/icons/Gear";
import ShieldCheck from "@gravity-ui/icons/ShieldCheck";
import CircleQuestion from "@gravity-ui/icons/CircleQuestion";
import Magnifier from "@gravity-ui/icons/Magnifier";
import Sun from "@gravity-ui/icons/Sun";
import Moon from "@gravity-ui/icons/Moon";
import LayoutSideContent from "@gravity-ui/icons/LayoutSideContent";
import LayoutColumns from "@gravity-ui/icons/LayoutColumns";

import { authStore, clearSession, setMode, setTheme, type ThemeName } from "@/auth/store";
import { UserAvatar } from "@/components/user-avatar";
import { SidebarContent, matchRoute } from "./sidebar-content";

const COLLAPSE_KEY = "wuling.sidebarCollapsed";

function readCollapsed(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return localStorage.getItem(COLLAPSE_KEY) === "1";
  } catch {
    return false;
  }
}

export function AppShell() {
  const [collapsed, setCollapsed] = useState(readCollapsed);

  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSE_KEY, collapsed ? "1" : "0");
    } catch {
      /* ignore quota errors */
    }
  }, [collapsed]);

  return (
    <div className="grid h-full w-full grid-rows-[44px_1fr] bg-bg text-fg">
      <BrandAndTopBar collapsed={collapsed} onToggle={() => setCollapsed((v) => !v)} />
      <div className="grid min-h-0 grid-cols-[48px_auto_1fr]">
        <ContextRail />
        <aside
          className={[
            "min-w-0 overflow-y-auto border-r border-[var(--border)] bg-[var(--surface)]",
            "transition-[width] duration-150 ease-out",
            collapsed ? "w-0" : "w-[248px]",
          ].join(" ")}
        >
          <div className={collapsed ? "hidden" : "block py-2"}>
            <SidebarContent />
          </div>
        </aside>
        <main className="min-w-0 overflow-x-hidden overflow-y-auto">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

/* ============================================================ top zone */

function BrandAndTopBar({
  collapsed,
  onToggle,
}: {
  collapsed: boolean;
  onToggle: () => void;
}) {
  const { token, user } = authStore.useStore();
  const navigate = useNavigate();

  return (
    <header className="flex items-stretch border-b border-[var(--border)] bg-[var(--surface)]">
      {/* Brand zone — sits above rail + sidebar */}
      <div
        className={[
          "flex items-center gap-1 border-r border-[var(--border)] pl-2.5 pr-1",
          collapsed ? "w-[48px]" : "w-[296px]",
          "transition-[width] duration-150 ease-out",
        ].join(" ")}
      >
        <button
          type="button"
          onClick={onToggle}
          aria-label={collapsed ? "展开侧边栏" : "折叠侧边栏"}
          title={collapsed ? "展开侧边栏" : "折叠侧边栏"}
          className="grid h-7 w-7 place-items-center rounded-sm text-fg/80 hover:bg-[var(--surface-secondary)] hover:text-fg"
        >
          {collapsed ? (
            <LayoutColumns width={16} height={16} />
          ) : (
            <LayoutSideContent width={16} height={16} />
          )}
        </button>
        {!collapsed ? (
          <Link
            to="/orgs"
            className="flex min-w-0 items-center gap-1.5 rounded-sm px-1.5 py-1 text-fg hover:bg-[var(--surface-secondary)]"
          >
            <BrandMark />
            <div className="flex min-w-0 flex-col leading-none">
              <span className="text-[13px] font-semibold tracking-tight">武陵</span>
              <span className="truncate text-[10px] text-muted">DevOps · 紫霄</span>
            </div>
          </Link>
        ) : null}
      </div>

      {/* Utility bar */}
      <div className="flex flex-1 items-center gap-2 px-3">
        <SearchSlot />
        <div className="ml-auto flex items-center gap-1">
          <ThemeMenu />
          <ModeToggle />
          {token && user ? (
            <UserMenu
              displayName={user.display_name || user.username}
              onLogout={() => {
                clearSession();
                navigate("/login", { replace: true });
              }}
            />
          ) : (
            <NavLink
              to="/login"
              className="rounded-sm border border-[var(--border)] bg-[var(--surface)] px-2.5 py-1 text-[12px] text-fg hover:bg-[var(--surface-secondary)]"
            >
              登录
            </NavLink>
          )}
        </div>
      </div>
    </header>
  );
}

/** Brand glyph. Pure SVG so we don't pull a logo asset; intent is a subtle
 *  geometric mark, not the org logo. */
function BrandMark() {
  return (
    <svg
      width="22"
      height="22"
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      className="shrink-0"
    >
      <rect x="2" y="2" width="20" height="20" rx="5" fill="var(--accent)" />
      <path
        d="M7 8.5 12 16l5-7.5"
        stroke="var(--accent-foreground)"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <circle cx="12" cy="6.5" r="1.2" fill="var(--accent-foreground)" />
    </svg>
  );
}

function SearchSlot() {
  // Placeholder search field — wires up to nothing yet, but the visual anchor
  // matters for the GitLab aesthetic. Pressing Enter currently no-ops; later
  // we can hook this to a routed search results page.
  const [q, setQ] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        inputRef.current?.focus();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <label
      className="flex h-7 flex-1 max-w-md items-center gap-1.5 rounded-sm border border-[var(--border)] bg-[var(--surface-secondary)] px-2 text-[12px] text-muted focus-within:border-[var(--accent)] focus-within:bg-[var(--surface)]"
      aria-label="搜索"
    >
      <Magnifier width={13} height={13} className="opacity-70" />
      <input
        ref={inputRef}
        type="search"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="搜索仓库、Issue、MR、Wiki…"
        className="h-full flex-1 min-w-0 bg-transparent text-[12px] text-fg placeholder:text-muted/80 focus:outline-none"
      />
      <kbd className="hidden h-5 items-center rounded-sm border border-[var(--border)] bg-[var(--surface)] px-1 font-mono text-[10px] text-muted sm:inline-flex">
        ⌘K
      </kbd>
    </label>
  );
}

/* ============================================================ rail */

const RAIL_TOP: Array<{ to: string; icon: typeof House; label: string; matchPrefix?: string }> = [
  { to: "/orgs", icon: House, label: "首页", matchPrefix: "/" },
  { to: "/orgs", icon: Layers, label: "全部组织", matchPrefix: "/orgs" },
];
const RAIL_BOTTOM: Array<{ to: string; icon: typeof House; label: string; matchPrefix?: string; admin?: boolean }> = [
  { to: "/settings/profile", icon: Gear, label: "设置", matchPrefix: "/settings" },
  { to: "/admin/users", icon: ShieldCheck, label: "系统管理", matchPrefix: "/admin", admin: true },
];

function ContextRail() {
  const { user } = authStore.useStore();
  const { pathname } = useLocation();
  const route = matchRoute(pathname);

  // Highlight rule for the top "首页" icon: only active when there's no
  // contextual sub-section. So /orgs/foo lights up "全部组织" instead.
  const inOrgsScope = pathname === "/orgs" || route.kind === "org" || route.kind === "project";

  return (
    <nav
      aria-label="主导航"
      className="flex h-full flex-col items-center gap-0.5 border-r border-[var(--border)] bg-[var(--surface-secondary)] py-2"
    >
      {RAIL_TOP.map((it, i) => {
        const Icon = it.icon;
        let active = false;
        if (i === 0) active = pathname === "/";
        if (i === 1) active = inOrgsScope;
        return <RailButton key={`${it.to}#${i}`} to={it.to} icon={Icon} label={it.label} active={active} />;
      })}
      <div className="flex-1" />
      {RAIL_BOTTOM.filter((it) => !it.admin || user?.is_admin).map((it) => {
        const Icon = it.icon;
        const active = it.matchPrefix ? pathname.startsWith(it.matchPrefix) : false;
        return <RailButton key={it.to} to={it.to} icon={Icon} label={it.label} active={active} />;
      })}
      <RailButton
        to="/orgs"
        icon={CircleQuestion}
        label="帮助"
        active={false}
        // Help link is a placeholder — points back home for now.
      />
    </nav>
  );
}

function RailButton({
  to,
  icon: Icon,
  label,
  active,
}: {
  to: string;
  icon: typeof House;
  label: string;
  active: boolean;
}) {
  return (
    <Link
      to={to}
      title={label}
      aria-label={label}
      className={[
        "group relative grid h-9 w-9 place-items-center rounded-md",
        "transition-colors duration-75",
        active
          ? "bg-[var(--surface)] text-fg shadow-sm"
          : "text-fg/70 hover:bg-[var(--surface)] hover:text-fg",
      ].join(" ")}
    >
      {active ? (
        <span aria-hidden className="absolute left-0 top-1/2 h-5 w-[3px] -translate-y-1/2 rounded-full bg-accent" />
      ) : null}
      <Icon width={18} height={18} />
    </Link>
  );
}

/* ============================================================ topbar pieces */

function ModeToggle() {
  const { mode } = authStore.useStore();
  const next = mode === "dark" ? "light" : "dark";
  return (
    <button
      type="button"
      onClick={() => setMode(next)}
      aria-label={next === "dark" ? "切到深色" : "切到浅色"}
      title={next === "dark" ? "切到深色" : "切到浅色"}
      className="grid h-7 w-7 place-items-center rounded-sm text-fg/80 hover:bg-[var(--surface-secondary)] hover:text-fg"
    >
      {mode === "dark" ? <Sun width={15} height={15} /> : <Moon width={15} height={15} />}
    </button>
  );
}

const THEMES: Array<{ id: ThemeName; label: string; swatch: string }> = [
  { id: "clean", label: "Clean", swatch: "#5a8fb0" },
  { id: "green", label: "Green", swatch: "#a3d150" },
  { id: "zixiaolabsvi", label: "ZX Violet", swatch: "#8b5cf6" },
];

function ThemeMenu() {
  const { theme } = authStore.useStore();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    }
    function onEsc(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onEsc);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onEsc);
    };
  }, [open]);

  const current = THEMES.find((t) => t.id === theme) ?? THEMES[0]!;
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        title="切换主题"
        className="grid h-7 w-7 place-items-center rounded-sm text-fg/80 hover:bg-[var(--surface-secondary)] hover:text-fg"
      >
        <span
          aria-hidden
          className="inline-block h-3.5 w-3.5 rounded-full border border-[var(--border)]"
          style={{ background: current.swatch }}
        />
      </button>
      {open ? (
        <div
          role="menu"
          className="absolute right-0 top-9 z-50 w-44 rounded-md border border-[var(--border)] bg-[var(--overlay)] py-1 shadow-lg"
        >
          <div className="px-2.5 py-1 text-[10px] uppercase tracking-wider text-muted">主题</div>
          {THEMES.map((t) => (
            <button
              key={t.id}
              type="button"
              role="menuitemradio"
              aria-checked={t.id === theme}
              onClick={() => {
                setTheme(t.id);
                setOpen(false);
              }}
              className={[
                "flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-[12px]",
                t.id === theme
                  ? "bg-[var(--surface-secondary)] text-fg"
                  : "text-fg/85 hover:bg-[var(--surface-secondary)] hover:text-fg",
              ].join(" ")}
            >
              <span
                aria-hidden
                className="inline-block h-3 w-3 rounded-full border border-[var(--border)]"
                style={{ background: t.swatch }}
              />
              {t.label}
              {t.id === theme ? <span className="ml-auto text-[10px] text-muted">✓</span> : null}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function UserMenu({ displayName, onLogout }: { displayName: string; onLogout: () => void }) {
  const { user } = authStore.useStore();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    }
    function onEsc(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onEsc);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onEsc);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        className="flex h-7 items-center gap-1.5 rounded-sm pl-1 pr-1.5 text-fg hover:bg-[var(--surface-secondary)]"
      >
        {user ? <UserAvatar user={user} /> : null}
      </button>
      {open ? (
        <div
          role="menu"
          className="absolute right-0 top-9 z-50 w-56 rounded-md border border-[var(--border)] bg-[var(--overlay)] py-1 shadow-lg"
        >
          <div className="border-b border-[var(--border)] px-3 py-2">
            <div className="truncate text-[12px] font-medium text-fg">{displayName}</div>
            {user?.username ? <div className="truncate text-[11px] text-muted">@{user.username}</div> : null}
          </div>
          <MenuLink to="/settings/profile" label="个人资料" onSelect={() => setOpen(false)} />
          <MenuLink to="/settings/tokens" label="访问令牌" onSelect={() => setOpen(false)} />
          <MenuLink to="/settings/ssh-keys" label="SSH 公钥" onSelect={() => setOpen(false)} />
          {user?.is_admin ? (
            <MenuLink to="/admin/users" label="系统管理" onSelect={() => setOpen(false)} />
          ) : null}
          <div className="my-1 border-t border-[var(--border)]" />
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              onLogout();
            }}
            className="w-full px-3 py-1.5 text-left text-[12px] text-fg/85 hover:bg-[var(--surface-secondary)] hover:text-fg"
          >
            退出登录
          </button>
        </div>
      ) : null}
    </div>
  );
}

function MenuLink({
  to,
  label,
  onSelect,
}: {
  to: string;
  label: string;
  onSelect: () => void;
}) {
  return (
    <Link
      to={to}
      onClick={onSelect}
      className="flex items-center px-3 py-1.5 text-[12px] text-fg/85 hover:bg-[var(--surface-secondary)] hover:text-fg"
    >
      {label}
    </Link>
  );
}
