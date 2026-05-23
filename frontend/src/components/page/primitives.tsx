/**
 * page/primitives.tsx — composition pieces shared by every page inside the
 * AppShell.
 *
 *   PageContainer         outermost wrapper, applies the page padding & max-width
 *   PageHeader            title row with optional eyebrow, description, actions
 *   PageTabs              GitLab-style underlined secondary nav inside a page
 *   Breadcrumbs           "/" separated path; final crumb is non-link
 *   Surface / SurfaceHeader / SurfaceBody / SurfaceFooter
 *                         GitLab-card replacement that consumes our tokens
 *                         directly so we can drop HeroUI's `Card` without
 *                         losing the layout vocabulary.
 *   SectionHeader         h2-equivalent inside a Surface
 *
 * These are headless except for spacing/typography — feel free to drop them
 * into any page without restyling the children.
 */

import type { ComponentType, ReactNode, SVGProps } from "react";
import { Link, NavLink } from "chen-the-dawnstreak";

import ArrowChevronRight from "@gravity-ui/icons/ArrowChevronRight";

/* --------------------------------------------------- PageContainer / header */

interface PageContainerProps {
  children: ReactNode;
  /** when true, removes the centred max-width — for repo tree etc. */
  wide?: boolean;
  className?: string;
}

export function PageContainer({ children, wide, className }: PageContainerProps) {
  return (
    <div
      className={[
        "mx-auto w-full px-4 pb-12 pt-4 md:px-6",
        wide ? "max-w-none" : "max-w-[1180px]",
        className ?? "",
      ].join(" ")}
    >
      {children}
    </div>
  );
}

interface PageHeaderProps {
  /** small text above the title (e.g. "组织 / Project") */
  eyebrow?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  /** right-aligned actions, usually a Button cluster */
  actions?: ReactNode;
  /** dense GitLab-style avatar slot to the left of the title */
  icon?: ReactNode;
  /** divider beneath the header; default true */
  divider?: boolean;
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
  icon,
  divider = true,
}: PageHeaderProps) {
  return (
    <header
      className={[
        "mb-4 flex flex-col gap-3 pb-3 md:flex-row md:items-start md:justify-between",
        divider ? "border-b border-[var(--separator)]" : "",
      ].join(" ")}
    >
      <div className="flex min-w-0 items-start gap-3">
        {icon ? (
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] text-fg/70">
            {icon}
          </div>
        ) : null}
        <div className="min-w-0">
          {eyebrow ? (
            <div className="mb-0.5 text-[11px] uppercase tracking-wider text-muted">{eyebrow}</div>
          ) : null}
          <h1 className="m-0 truncate text-[20px] font-semibold leading-tight text-fg">{title}</h1>
          {description ? (
            <div className="mt-1 max-w-[68ch] text-[13px] text-muted">{description}</div>
          ) : null}
        </div>
      </div>
      {actions ? (
        <div className="flex shrink-0 items-center gap-2 self-start md:self-center">{actions}</div>
      ) : null}
    </header>
  );
}

/* ----------------------------------------------------------------- PageTabs */

export interface PageTab {
  to: string;
  label: ReactNode;
  badge?: ReactNode;
  /** end-match — use for "overview" parent tab to avoid sticking active on /repos */
  end?: boolean;
  icon?: ComponentType<SVGProps<SVGSVGElement>>;
}

export function PageTabs({ items }: { items: PageTab[] }) {
  return (
    <nav
      aria-label="二级导航"
      className="-mt-1 mb-4 flex items-center gap-0 overflow-x-auto border-b border-[var(--separator)]"
    >
      {items.map((it) => {
        const Icon = it.icon;
        return (
          <NavLink
            key={it.to}
            to={it.to}
            end={it.end ?? false}
            className={({ isActive }) =>
              [
                "relative inline-flex items-center gap-1.5 whitespace-nowrap px-3 py-2 text-[13px]",
                "transition-colors duration-75",
                isActive
                  ? "text-fg"
                  : "text-fg/65 hover:text-fg",
              ].join(" ")
            }
          >
            {({ isActive }) => (
              <>
                {Icon ? <Icon width={14} height={14} className="opacity-80" /> : null}
                <span>{it.label}</span>
                {it.badge !== undefined && it.badge !== null ? (
                  <span className="rounded-full bg-[var(--surface-tertiary)] px-1.5 py-px text-[10px] tabular-nums text-muted">
                    {it.badge}
                  </span>
                ) : null}
                {isActive ? (
                  <span
                    aria-hidden
                    className="absolute inset-x-2 -bottom-px h-[2px] rounded-full bg-accent"
                  />
                ) : null}
              </>
            )}
          </NavLink>
        );
      })}
    </nav>
  );
}

/* --------------------------------------------------------------- Breadcrumbs */

export interface BreadcrumbItem {
  label: ReactNode;
  to?: string;
}

export function Breadcrumbs({ items }: { items: BreadcrumbItem[] }) {
  return (
    <nav aria-label="位置" className="mb-2 flex items-center gap-1 text-[12px] text-muted">
      {items.map((it, i) => {
        const isLast = i === items.length - 1;
        return (
          <span key={i} className="inline-flex items-center gap-1">
            {it.to && !isLast ? (
              <Link to={it.to} className="rounded-sm hover:bg-[var(--surface-secondary)] hover:text-fg px-1 py-0.5">
                {it.label}
              </Link>
            ) : (
              <span className={isLast ? "text-fg font-medium px-1 py-0.5" : "px-1 py-0.5"}>{it.label}</span>
            )}
            {!isLast ? (
              <ArrowChevronRight width={11} height={11} className="opacity-60" />
            ) : null}
          </span>
        );
      })}
    </nav>
  );
}

/* ------------------------------------------------------------------- Surface */

interface SurfaceProps {
  children: ReactNode;
  /** drop the border + bg — when we want a "panel" feel sitting flush on the bg */
  flush?: boolean;
  className?: string;
}

export function Surface({ children, flush, className }: SurfaceProps) {
  return (
    <section
      className={[
        "overflow-hidden",
        flush
          ? ""
          : "rounded-md border border-[var(--border)] bg-[var(--surface)]",
        className ?? "",
      ].join(" ")}
    >
      {children}
    </section>
  );
}

interface SurfaceHeaderProps {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  /** thinner padding row used for "list table" headers */
  dense?: boolean;
  children?: ReactNode;
}

export function SurfaceHeader({ title, description, actions, dense, children }: SurfaceHeaderProps) {
  if (children) {
    return (
      <div
        className={[
          "flex items-center justify-between gap-3 border-b border-[var(--separator)] bg-[var(--surface-secondary)]/40",
          dense ? "px-3 py-1.5" : "px-4 py-2.5",
        ].join(" ")}
      >
        {children}
      </div>
    );
  }
  return (
    <div
      className={[
        "flex items-start justify-between gap-3 border-b border-[var(--separator)]",
        dense ? "px-3 py-2" : "px-4 py-3",
      ].join(" ")}
    >
      <div className="min-w-0">
        {title ? <div className="text-[13px] font-semibold text-fg">{title}</div> : null}
        {description ? <div className="mt-0.5 text-[12px] text-muted">{description}</div> : null}
      </div>
      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </div>
  );
}

export function SurfaceBody({
  children,
  noPad,
  className,
}: {
  children: ReactNode;
  /** disable the default px-4 py-3 padding (useful for full-bleed lists) */
  noPad?: boolean;
  className?: string;
}) {
  return (
    <div className={[noPad ? "" : "px-4 py-3", className ?? ""].join(" ")}>{children}</div>
  );
}

export function SurfaceFooter({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 border-t border-[var(--separator)] bg-[var(--surface-secondary)]/30 px-4 py-2.5 text-[12px] text-muted">
      {children}
    </div>
  );
}

export function SectionHeader({
  title,
  description,
  actions,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-2 flex items-end justify-between gap-3">
      <div>
        <h2 className="m-0 text-[14px] font-semibold text-fg">{title}</h2>
        {description ? <div className="mt-0.5 text-[12px] text-muted">{description}</div> : null}
      </div>
      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </div>
  );
}
