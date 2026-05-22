import { useParams, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RefPicker } from "@/components/ref-picker";
import { RelativeTime } from "@/components/relative-time";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Commit } from "@/api/types";

export default function CommitsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const [search, setSearch] = useSearchParams();

  const repoSlug = params.repo_slug ?? "";
  const ref = search.get("ref") ?? "";

  const [commits, setCommits] = useState<Commit[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setCommits(null);
    setError(null);
    reposApi
      .commits(org.slug, project.slug, repoSlug, { ref: ref || undefined, limit: 100 })
      .then(setCommits)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, repoSlug, ref]);

  // Group consecutive commits by author-when date for a GitLab-like timeline.
  const groups = commits ? groupByDate(commits) : null;

  return (
    <PageContainer wide>
      <PageHeader
        title="提交"
        description={`仓库 ${repoSlug} · 默认每页 100 条。`}
        actions={
          <RefPicker
            org={org.slug}
            project={project.slug}
            repo={repoSlug}
            value={ref}
            onChange={(next) => {
              const sp = new URLSearchParams(search);
              if (next) sp.set("ref", next);
              else sp.delete("ref");
              setSearch(sp);
            }}
          />
        }
      />

      <ErrorBanner error={error} />

      {commits === null ? (
        <Loading />
      ) : commits.length === 0 ? (
        <Surface>
          <SurfaceBody>
            <div className="text-[13px] text-muted">（无提交）</div>
          </SurfaceBody>
        </Surface>
      ) : (
        <div className="flex flex-col gap-4">
          {groups!.map((group) => (
            <div key={group.date}>
              <div className="mb-1.5 text-[11px] uppercase tracking-wider text-muted">
                {group.date}
              </div>
              <Surface>
                <SurfaceBody noPad>
                  <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
                    {group.items.map((c) => (
                      <li key={c.oid} className="flex items-start gap-3 px-4 py-3">
                        <div className="grid h-6 w-6 shrink-0 place-items-center rounded-full bg-[var(--surface-secondary)] text-fg/70">
                          <span className="font-mono text-[10px]">{c.author.name.slice(0, 1)}</span>
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="text-[13px] font-medium text-fg">
                            {firstLine(c.message)}
                          </div>
                          {restOfMessage(c.message) ? (
                            <pre className="mt-1 overflow-x-auto whitespace-pre-wrap rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] p-2.5 font-mono text-[11.5px] text-muted">
                              {restOfMessage(c.message)}
                            </pre>
                          ) : null}
                          <div className="mt-1 text-[11px] text-muted">
                            <span className="font-medium text-fg/85">{c.author.name}</span>
                            <span className="mx-1">·</span>
                            <RelativeTime iso={c.author.when} />
                          </div>
                        </div>
                        <code className="shrink-0 rounded-sm border border-[var(--border)] bg-[var(--surface-secondary)] px-1.5 py-0.5 font-mono text-[11px] text-muted">
                          {c.oid.slice(0, 8)}
                        </code>
                      </li>
                    ))}
                  </ul>
                </SurfaceBody>
              </Surface>
            </div>
          ))}
        </div>
      )}
    </PageContainer>
  );
}

function firstLine(s: string): string {
  const i = s.indexOf("\n");
  return i === -1 ? s : s.slice(0, i);
}
function restOfMessage(s: string): string {
  const i = s.indexOf("\n");
  return i === -1 ? "" : s.slice(i + 1).trim();
}

function groupByDate(commits: Commit[]): Array<{ date: string; items: Commit[] }> {
  const groups: Array<{ date: string; items: Commit[] }> = [];
  for (const c of commits) {
    const d = new Date(c.author.when);
    const key = isNaN(d.getTime()) ? "未知" : d.toISOString().slice(0, 10);
    const last = groups[groups.length - 1];
    if (last && last.date === key) last.items.push(c);
    else groups.push({ date: key, items: [c] });
  }
  return groups;
}
