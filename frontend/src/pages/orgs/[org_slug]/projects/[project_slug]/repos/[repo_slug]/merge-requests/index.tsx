import { Button } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import CodePullRequest from "@gravity-ui/icons/CodePullRequest";
import { Link, useParams, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { mergeRequests as mrApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
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
import type { MRState, MergeRequest } from "@/api/types";

const STATES: Array<{ id: MRState | "all"; label: string }> = [
  { id: "open", label: "Open" },
  { id: "merged", label: "Merged" },
  { id: "closed", label: "Closed" },
  { id: "all", label: "全部" },
];

export default function MRListPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const [search, setSearch] = useSearchParams();
  const repoSlug = params.repo_slug ?? "";

  const state = (search.get("state") ?? "open") as MRState | "all";
  const author = search.get("author") ?? "";
  const [authorDraft, setAuthorDraft] = useState(author);
  useEffect(() => setAuthorDraft(author), [author]);

  const [items, setItems] = useState<MergeRequest[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setItems(null);
    setError(null);
    mrApi
      .list(org.slug, project.slug, repoSlug, {
        state: state === "all" ? undefined : state,
        author: author || undefined,
        limit: 100,
      })
      .then(setItems)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, repoSlug, state, author]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repoSlug)}/merge-requests`;

  function commitAuthor() {
    const sp = new URLSearchParams(search);
    if (authorDraft) sp.set("author", authorDraft);
    else sp.delete("author");
    setSearch(sp);
  }

  return (
    <PageContainer wide>
      <PageHeader
        title="合并请求"
        description={`仓库 ${repoSlug} 的所有 MR，按更新时间倒序。`}
        actions={
          <Link to={`${base}/new`}>
            <Button>
              <PlusIcon width={14} height={14} /> 新建 MR
            </Button>
          </Link>
        }
      />

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
            <label className="inline-flex h-7 items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--field-background)] px-2 text-[12px] focus-within:border-[var(--accent)]">
              <span className="text-[11px] uppercase tracking-wider text-muted">作者</span>
              <input
                type="text"
                value={authorDraft}
                onChange={(e) => setAuthorDraft(e.target.value)}
                onBlur={commitAuthor}
                onKeyDown={(e) => {
                  if (e.key === "Enter") commitAuthor();
                }}
                placeholder="username 或 UUID"
                className="h-full w-[10rem] bg-transparent text-fg placeholder:text-muted/80 focus:outline-none"
              />
            </label>
          </div>
        </SurfaceBody>
      </Surface>

      <ErrorBanner error={error} />

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            合并请求{items ? ` · ${items.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <SkeletonRows count={5} />
          ) : items.length === 0 ? (
            <EmptyState
              inset
              icon={<CodePullRequest width={20} height={20} />}
              title="没有匹配的 MR"
              description="尝试切换状态过滤，或新建一个 MR。"
              action={
                <Link to={`${base}/new`}>
                  <Button>
                    <PlusIcon width={14} height={14} /> 新建 MR
                  </Button>
                </Link>
              }
            />
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {items.map((mr) => (
                <li key={mr.id} className="px-4 py-2.5 hover:bg-[var(--surface-secondary)]/40">
                  <div className="flex items-baseline gap-3">
                    <StateBadge state={mr.state} />
                    <Link
                      to={`${base}/${mr.number}`}
                      className="min-w-0 flex-1 truncate text-[13.5px] font-medium text-fg hover:text-[var(--accent)] hover:underline"
                    >
                      {mr.title}
                    </Link>
                    <span className="shrink-0 font-mono text-[11.5px] text-muted">
                      #{mr.number}
                    </span>
                  </div>
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[11.5px] text-muted">
                    <span className="inline-flex items-center gap-1.5">
                      <UserAvatar user={mr.author} size={16} />
                      {mr.author.username}
                    </span>
                    <span className="inline-flex items-center gap-1 font-mono text-[11px]">
                      <code className="rounded-sm bg-[var(--surface-secondary)] px-1.5 py-px text-fg">
                        {shortRef(mr.source_ref)}
                      </code>
                      <span>→</span>
                      <code className="rounded-sm bg-[var(--surface-secondary)] px-1.5 py-px text-fg">
                        {shortRef(mr.target_ref)}
                      </code>
                    </span>
                    <span>
                      <RelativeTime iso={mr.updated_at} /> 更新
                    </span>
                    {mr.review_count > 0 ? <span>{mr.review_count} 评审</span> : null}
                    {mr.comment_count > 0 ? <span>{mr.comment_count} 评论</span> : null}
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

function shortRef(r: string): string {
  return r.replace(/^refs\/(heads|tags)\//, "");
}
