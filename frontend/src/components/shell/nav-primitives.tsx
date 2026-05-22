/**
 * shell/nav-primitives.tsx — sidebar navigation building blocks.
 *
 * Three pieces:
 *  - SidebarSection: a labelled group of items (small caps header).
 *  - NavItem:        a single nav row with icon + label + optional badge.
 *  - NavGroup:       a collapsible group (e.g. "Settings", "Plan") whose
 *                    children are nested NavItems.
 *
 * Active state uses Tailwind classes that consume the bridged token colors
 * (`bg-surface-2`, `text-fg`, etc.). The 2px left indicator is the only piece
 * that has to track the theme's accent — we use `bg-accent`.
 */

import { NavLink, useLocation } from "chen-the-dawnstreak";
import ArrowChevronRight from "@gravity-ui/icons/ArrowChevronRight";
import ArrowChevronDown from "@gravity-ui/icons/ArrowChevronDown";
import { useState, type ComponentType, type SVGProps, type ReactNode } from "react";

export type IconCmp = ComponentType<SVGProps<SVGSVGElement>>;

interface SidebarSectionProps {
  label?: string;
  children: ReactNode;
}

export function SidebarSection({ label, children }: SidebarSectionProps) {
  return (
    <div className="py-2">
      {label ? (
        <div className="px-3 pb-1 text-[10px] font-semibold uppercase tracking-wider text-muted">
          {label}
        </div>
      ) : null}
      <div className="flex flex-col">{children}</div>
    </div>
  );
}

interface NavItemProps {
  to: string;
  icon?: IconCmp;
  label: ReactNode;
  badge?: ReactNode;
  /** when true, NavLink uses `end` (exact match) — needed for parent overview routes */
  exact?: boolean;
  /** indent depth, multiplied by 0.75rem on the left padding */
  indent?: number;
}

export function NavItem({ to, icon: Icon, label, badge, exact, indent = 0 }: NavItemProps) {
  const padLeft = 12 + indent * 16;
  return (
    <NavLink
      to={to}
      end={exact ?? false}
      className={({ isActive }) =>
        [
          "relative flex h-7 items-center gap-2 rounded-sm pr-2 text-[13px]",
          "transition-colors duration-75 outline-none",
          isActive
            ? "bg-[var(--surface-secondary)] text-fg font-medium"
            : "text-fg/85 hover:bg-[var(--surface-secondary)] hover:text-fg",
        ].join(" ")
      }
      style={{ paddingLeft: padLeft }}
    >
      {({ isActive }) => (
        <>
          {isActive ? (
            <span
              aria-hidden
              className="absolute left-0 top-1/2 h-4 w-[3px] -translate-y-1/2 rounded-full bg-accent"
            />
          ) : null}
          {Icon ? <Icon width={14} height={14} className="shrink-0 opacity-80" /> : null}
          <span className="flex-1 truncate">{label}</span>
          {badge !== undefined && badge !== null ? (
            <span className="ml-auto rounded-full bg-[var(--surface-tertiary)] px-1.5 py-px text-[10px] tabular-nums text-muted">
              {badge}
            </span>
          ) : null}
        </>
      )}
    </NavLink>
  );
}

interface NavGroupProps {
  icon?: IconCmp;
  label: string;
  /** auto-open if the current pathname starts with this prefix */
  match?: string;
  children: ReactNode;
}

/**
 * Collapsible group. Disclosure state is local — auto-opens when the current
 * URL matches `match` (or any of `matches`), so deep links land already
 * expanded.
 */
export function NavGroup({ icon: Icon, label, match, children }: NavGroupProps) {
  const { pathname } = useLocation();
  const auto = match ? pathname.startsWith(match) : false;
  const [open, setOpen] = useState(auto);
  // Re-sync when the URL changes underneath us. (useEffect would be cleaner
  // but the cost of a tiny re-render here is negligible.)
  const effectiveOpen = open || auto;

  return (
    <div className="flex flex-col">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex h-7 items-center gap-2 rounded-sm pl-3 pr-2 text-[13px] text-fg/85 hover:bg-[var(--surface-secondary)] hover:text-fg"
      >
        {Icon ? <Icon width={14} height={14} className="shrink-0 opacity-80" /> : null}
        <span className="flex-1 text-left">{label}</span>
        {effectiveOpen ? (
          <ArrowChevronDown width={12} height={12} className="opacity-70" />
        ) : (
          <ArrowChevronRight width={12} height={12} className="opacity-70" />
        )}
      </button>
      {effectiveOpen ? <div className="flex flex-col">{children}</div> : null}
    </div>
  );
}
