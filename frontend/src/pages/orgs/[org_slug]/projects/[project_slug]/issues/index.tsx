import { Button } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import Magnifier from "@gravity-ui/icons/Magnifier";
import CircleQuestion from "@gravity-ui/icons/CircleQuestion";
import { Link, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { issues as issuesApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { LabelChip } from "@/components/label-chip";
import { SkeletonRows } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { StateBadge } from "@/components/page/badges";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Issue, IssueState } from "@/api/types";

const STATES: Array<{ id: IssueState | "all"; label: string }> = [
  { id: "open", label: "Open" },
  { id: "closed", label: "Closed" },
  { id: "all", label: "全部" },
];

export default function IssuesIndex() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [search, setSearch] = useSearchParams();

  const state = (search.get("state") ?? "open") as IssueState | "all";
  const label = search.get("label") ?? "";
  const assignee = search.get("assignee") ?? "";
  const author = search.get("author") ?? "";
  const q = search.get("search") ?? "";

  const [items, setItems] = useState<Issue[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  // Mirror the URL search box in local state so typing doesn't pound the
  // router on every keystroke — commit on blur or Enter.
  const [searchDraft, setSearchDraft] = useState(q);
  useEffect(() => setSearchDraft(q), [q]);

  useEffect(() => {
    setItems(null);
    setError(null);
    issuesApi
      .list(org.slug, project.slug, {
        state: state === "all" ? undefined : state,
        label: label || undefined,
        assignee: assignee || undefined,
        author: author || undefined,
        search: q || undefined,
        limit: 100,
      })
      .then(setItems)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, state, label, assignee, author, q]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/issues`;

  function update(key: string, value: string) {
    const sp = new URLSearchParams(search);
    if (value) sp.set(key, value);
    else sp.delete(key);
    setSearch(sp);
  }

  return (
    <PageContainer wide>
      <PageHeader
        title="Issues"
        description="任务、缺陷和讨论。可以用标签、作者、被指派人过滤。"
        actions={
          <Link to={`${base}/new`}>
            <Button>
              <PlusIcon width={14} height={14} /> 新建 Issue
            </Button>
          </Link>
        }
      />

      {/* State toggle + filters */}
      <Surface className="mb-3">
        <SurfaceBody>
          <div className="flex flex-wrap items-center gap-3">
            <div className="inline-flex h-7 items-center overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)]">
              {STATES.map((s, i) => {
                const active = s.id === state;
                return (
                  <button
                    key={s.id}
                    onClick={() => {
                      const sp = new URLSearchParams(search);
                      if (s.id === "open") sp.delete("state");
                      else sp.set("state", s.id);
                      setSearch(sp);
                    }}
                    className={[
                      "h-full px-3 text-[12px]",
                      i > 0 ? "border-l border-[var(--border)]" : "",
                      active
                        ? "bg-[var(--surface-secondary)] font-medium text-fg"
                        : "text-fg/70 hover:bg-[var(--surface-secondary)] hover:text-fg",
                    ].join(" ")}
                  >
                    {s.label}
                  </button>
                );
              })}
            </div>

            <label className="inline-flex h-7 flex-1 min-w-[200px] max-w-md items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--field-background)] px-2 text-[12px] focus-within:border-[var(--accent)]">
              <Magnifier width={13} height={13} className="opacity-60" />
              <input
                type="search"
                value={searchDraft}
                onChange={(e) => setSearchDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") update("search", searchDraft);
                }}
                onBlur={() => update("search", searchDraft)}
                placeholder="搜索标题或正文…"
                className="h-full flex-1 bg-transparent text-fg placeholder:text-muted/80 focus:outline-none"
              />
            </label>

            <FilterChip label="标签" value={label} onChange={(v) => update("label", v)} />
            <FilterChip label="作者" value={author} onChange={(v) => update("author", v)} />
            <FilterChip label="指派" value={assignee} onChange={(v) => update("assignee", v)} />
          </div>
        </SurfaceBody>
      </Surface>

      <ErrorBanner error={error} />

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            Issues{items ? ` · ${items.length}` : ""}
          </span>
          <span className="text-[11.5px] text-muted">按更新时间倒序</span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <SkeletonRows count={6} />
          ) : items.length === 0 ? (
            <EmptyState
              inset
              icon={<CircleQuestion width={20} height={20} />}
              title="没有匹配的 Issue"
              description="试着换个过滤条件，或新建一个。"
              action={
                <Link to={`${base}/new`}>
                  <Button>
                    <PlusIcon width={14} height={14} /> 新建 Issue
                  </Button>
                </Link>
              }
            />
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {items.map((it) => (
                <li key={it.id} className="px-4 py-2.5 hover:bg-[var(--surface-secondary)]/40">
                  <div className="flex items-baseline gap-3">
                    <StateBadge state={it.state} />
                    <Link
                      to={`${base}/${it.number}`}
                      className="min-w-0 flex-1 truncate text-[13.5px] font-medium text-fg hover:text-[var(--accent)] hover:underline"
                    >
                      {it.title}
                    </Link>
                    <span className="shrink-0 font-mono text-[11.5px] text-muted">
                      #{it.number}
                    </span>
                  </div>
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[11.5px] text-muted">
                    <span className="inline-flex items-center gap-1.5">
                      <UserAvatar user={it.author} size={16} />
                      {it.author.username}
                    </span>
                    <span>
                      <RelativeTime iso={it.updated_at} /> 更新
                    </span>
                    {it.comment_count > 0 ? (
                      <span className="inline-flex items-center gap-1">
                        <CircleQuestion width={11} height={11} />
                        {it.comment_count}
                      </span>
                    ) : null}
                    {it.labels.length > 0 ? (
                      <span className="inline-flex flex-wrap gap-1">
                        {it.labels.map((l) => (
                          <LabelChip key={l.id} label={l} size="sm" />
                        ))}
                      </span>
                    ) : null}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}

function FilterChip({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <label className="inline-flex h-7 items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--field-background)] px-2 text-[12px] focus-within:border-[var(--accent)]">
      <span className="text-[11px] uppercase tracking-wider text-muted">{label}</span>
      <input
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => onChange(draft)}
        onKeyDown={(e) => {
          if (e.key === "Enter") onChange(draft);
        }}
        className="h-full w-[7rem] bg-transparent text-fg focus:outline-none"
      />
    </label>
  );
}
