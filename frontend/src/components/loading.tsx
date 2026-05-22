import { Spinner } from "@heroui/react";

export function Loading({ label = "加载中…" }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-3 px-4 py-10 text-[13px] text-muted">
      <Spinner size="sm" />
      <span>{label}</span>
    </div>
  );
}

/** Skeleton row used inline within DataLists while content is fetching. */
export function SkeletonRows({ count = 5 }: { count?: number }) {
  return (
    <ul className="divide-y divide-[var(--separator)] list-none m-0 p-0">
      {Array.from({ length: count }).map((_, i) => (
        <li key={i} className="flex items-center gap-3 px-4 py-3">
          <div className="h-7 w-7 shrink-0 animate-pulse rounded-md bg-[var(--surface-secondary)]" />
          <div className="min-w-0 flex-1">
            <div className="h-3 w-1/3 animate-pulse rounded bg-[var(--surface-secondary)]" />
            <div className="mt-1.5 h-2.5 w-1/2 animate-pulse rounded bg-[var(--surface-secondary)]" />
          </div>
          <div className="h-3 w-20 shrink-0 animate-pulse rounded bg-[var(--surface-secondary)]" />
        </li>
      ))}
    </ul>
  );
}
