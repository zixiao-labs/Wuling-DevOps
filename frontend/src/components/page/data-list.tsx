/**
 * page/data-list.tsx — dense list pattern used for orgs / projects / repos /
 * issues / MRs. Two flavours:
 *
 *   <DataList>           wraps rows with hairline dividers inside a Surface.
 *   <ListRow>            a single row: optional icon → title → meta on the right
 *
 * Always pair with <Surface noPad>:
 *
 *   <Surface><SurfaceBody noPad><DataList>{rows}</DataList></SurfaceBody></Surface>
 */

import type { ReactNode } from "react";
import { Link } from "chen-the-dawnstreak";

interface DataListProps {
  children: ReactNode;
}

export function DataList({ children }: DataListProps) {
  return (
    <ul className="divide-y divide-[var(--separator)] list-none m-0 p-0">{children}</ul>
  );
}

interface ListRowProps {
  /** if set, the entire row becomes a Link to this target */
  to?: string;
  icon?: ReactNode;
  /** primary headline (truncates on overflow) */
  title: ReactNode;
  /** small subhead below the title (e.g. slug, description) */
  subtitle?: ReactNode;
  /** right-aligned secondary content */
  meta?: ReactNode;
  /** override the default padding */
  dense?: boolean;
  children?: ReactNode;
}

export function ListRow({ to, icon, title, subtitle, meta, dense, children }: ListRowProps) {
  const padY = dense ? "py-2" : "py-2.5";
  const body = (
    <div className={["flex items-center gap-3 px-4", padY].join(" ")}>
      {icon ? <span className="shrink-0 text-fg/70">{icon}</span> : null}
      <div className="min-w-0 flex-1">
        <div className="truncate text-[13px] font-medium text-fg">{title}</div>
        {subtitle ? (
          <div className="mt-0.5 truncate text-[11.5px] text-muted">{subtitle}</div>
        ) : null}
        {children}
      </div>
      {meta ? <div className="shrink-0 text-right text-[11.5px] text-muted">{meta}</div> : null}
    </div>
  );

  if (to) {
    return (
      <li>
        <Link
          to={to}
          className="block hover:bg-[var(--surface-secondary)] transition-colors duration-75"
        >
          {body}
        </Link>
      </li>
    );
  }
  return <li className="block">{body}</li>;
}
