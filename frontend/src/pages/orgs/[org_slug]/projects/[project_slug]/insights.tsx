import { useEffect, useState } from "react";

import { insights as insightsApi, repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
import { Stat } from "@/components/page/badges";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type {
  ActivityDay,
  ContributorStat,
  LanguageStats,
  Repo,
} from "@/api/types";

type Tab = "activity" | "contributors" | "languages";

export default function InsightsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();

  const [tab, setTab] = useState<Tab>("activity");
  const [repos, setRepos] = useState<Repo[] | null>(null);
  const [selectedRepo, setSelectedRepo] = useState<string>("");

  const [activity, setActivity] = useState<ActivityDay[] | null>(null);
  const [contribs, setContribs] = useState<ContributorStat[] | null>(null);
  const [langs, setLangs] = useState<LanguageStats | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    let stale = false;
    reposApi
      .list(org.slug, project.slug)
      .then((r) => {
        if (stale) return;
        setRepos(r);
        setSelectedRepo(r.length > 0 ? r[0]!.slug : "");
      })
      .catch((e) => {
        if (stale) return;
        setError(e as ApiError);
      });
    return () => {
      stale = true;
    };
  }, [org.slug, project.slug]);

  useEffect(() => {
    setActivity(null);
    insightsApi.activity(org.slug, project.slug, "30d").then(setActivity).catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug]);

  useEffect(() => {
    if (!selectedRepo) return;
    setContribs(null);
    setLangs(null);
    insightsApi
      .contributors(org.slug, project.slug, { repo: selectedRepo, since: "90d", limit: 20 })
      .then(setContribs)
      .catch((e) => setError(e as ApiError));
    insightsApi
      .languages(org.slug, project.slug, { repo: selectedRepo })
      .then(setLangs)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, selectedRepo]);

  const totals = activity
    ? activity.reduce(
        (acc, d) => {
          acc.commits += d.commits;
          acc.issues_opened += d.issues_opened;
          acc.issues_closed += d.issues_closed;
          acc.mrs_opened += d.mrs_opened;
          acc.mrs_merged += d.mrs_merged;
          return acc;
        },
        { commits: 0, issues_opened: 0, issues_closed: 0, mrs_opened: 0, mrs_merged: 0 },
      )
    : null;

  return (
    <PageContainer>
      <PageHeader
        title="Insights"
        description="过去 30 天的活动概要与按仓库分解的贡献/语言统计。"
      />
      <ErrorBanner error={error} />

      {totals ? (
        <div className="mb-5 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat label="近 30 天提交" value={totals.commits} />
          <Stat label="新增 Issue" value={totals.issues_opened} hint={`关闭 ${totals.issues_closed}`} />
          <Stat label="新增 MR" value={totals.mrs_opened} hint={`合入 ${totals.mrs_merged}`} />
          <Stat label="仓库总数" value={repos?.length ?? "—"} />
        </div>
      ) : null}

      <PageTabsAdHoc tab={tab} onChange={setTab} />

      {tab === "activity" ? (
        <Surface>
          <SurfaceBody>
            {activity === null ? <Loading /> : <ActivityChart days={activity} />}
          </SurfaceBody>
        </Surface>
      ) : null}
      {tab === "contributors" ? (
        <Surface>
          <SurfaceBody>
            <RepoSelect repos={repos} value={selectedRepo} onChange={setSelectedRepo} />
            {contribs === null ? (
              <Loading />
            ) : contribs.length === 0 ? (
              <div className="px-1 py-4 text-[13px] text-muted">（无贡献者数据）</div>
            ) : (
              <ContributorsList list={contribs} />
            )}
          </SurfaceBody>
        </Surface>
      ) : null}
      {tab === "languages" ? (
        <Surface>
          <SurfaceBody>
            <RepoSelect repos={repos} value={selectedRepo} onChange={setSelectedRepo} />
            {langs === null ? <Loading /> : <Languages stats={langs} />}
          </SurfaceBody>
        </Surface>
      ) : null}
    </PageContainer>
  );
}

function RepoSelect({
  repos,
  value,
  onChange,
}: {
  repos: Repo[] | null;
  value: string;
  onChange: (s: string) => void;
}) {
  if (!repos) return <Loading />;
  if (repos.length === 0) return <div className="text-[13px] text-muted">项目里还没有仓库。</div>;
  return (
    <label className="my-2 inline-flex items-center gap-2 text-[12.5px]">
      <span className="text-muted">仓库</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-7 rounded-sm border border-[var(--border)] bg-[var(--field-background)] px-2 text-[var(--field-foreground)]"
      >
        {repos.map((r) => (
          <option key={r.id} value={r.slug}>
            {r.slug}
          </option>
        ))}
      </select>
    </label>
  );
}

function ActivityChart({ days }: { days: ActivityDay[] }) {
  if (days.length === 0) {
    return <div className="text-[13px] text-muted">（无活动）</div>;
  }
  const max = Math.max(
    1,
    ...days.map((d) => d.commits + d.issues_opened + d.mrs_merged),
  );
  return (
    <div>
      <div className="flex h-32 items-end gap-[2px]">
        {days.map((d) => {
          const total = d.commits + d.issues_opened + d.mrs_merged;
          const h = Math.max(2, (total / max) * 120);
          return (
            <div
              key={d.date}
              title={`${d.date}\ncommits: ${d.commits}\nissues_opened: ${d.issues_opened}\nmrs_merged: ${d.mrs_merged}`}
              className="flex-1 min-w-[3px] rounded-[2px] transition-opacity"
              style={{
                height: h,
                background: "var(--accent)",
                opacity: total === 0 ? 0.18 : 0.85,
              }}
            />
          );
        })}
      </div>
      <div className="mt-1 flex justify-between font-mono text-[10.5px] text-muted">
        <span>{days[0]?.date}</span>
        <span>{days[days.length - 1]?.date}</span>
      </div>
    </div>
  );
}

function ContributorsList({ list }: { list: ContributorStat[] }) {
  const max = Math.max(1, ...list.map((c) => c.commits));
  return (
    <ul className="list-none divide-y divide-[var(--separator)] p-0 m-0">
      {list.map((c) => (
        <li key={c.email + c.name} className="py-2">
          <div className="mb-1 flex items-baseline justify-between gap-3 text-[13px]">
            <span className="min-w-0 truncate">
              <span className="font-medium text-fg">{c.name}</span>
              <span className="ml-1 font-mono text-[11.5px] text-muted">&lt;{c.email}&gt;</span>
            </span>
            <span className="shrink-0 text-[11.5px] tabular-nums text-muted">
              {c.commits} 提交
            </span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-[var(--surface-secondary)]">
            <div
              className="h-full bg-[var(--accent)]"
              style={{ width: `${(c.commits / max) * 100}%` }}
            />
          </div>
        </li>
      ))}
    </ul>
  );
}

function PageTabsAdHoc({ tab, onChange }: { tab: Tab; onChange: (t: Tab) => void }) {
  const items: Array<{ id: Tab; label: string }> = [
    { id: "activity", label: "活动（30 天）" },
    { id: "contributors", label: "贡献者" },
    { id: "languages", label: "语言" },
  ];
  return (
    <nav
      role="tablist"
      aria-label="Insights 视图"
      className="mb-4 flex items-center gap-0 overflow-x-auto border-b border-[var(--separator)]"
    >
      {items.map((it) => {
        const active = tab === it.id;
        return (
          <button
            key={it.id}
            role="tab"
            type="button"
            aria-selected={active}
            onClick={() => onChange(it.id)}
            className={[
              "relative inline-flex items-center whitespace-nowrap px-3 py-2 text-[13px] transition-colors",
              active ? "text-fg" : "text-fg/65 hover:text-fg",
            ].join(" ")}
          >
            {it.label}
            {active ? (
              <span
                aria-hidden
                className="absolute inset-x-2 -bottom-px h-[2px] rounded-full bg-accent"
              />
            ) : null}
          </button>
        );
      })}
    </nav>
  );
}

function Languages({ stats }: { stats: LanguageStats }) {
  const entries = Object.entries(stats.bytes).sort((a, b) => b[1] - a[1]);
  const total = entries.reduce((s, [, b]) => s + b, 0) || 1;
  return (
    <>
      {stats.truncated ? (
        <div className="mb-2 rounded-md border border-[var(--warning)]/40 bg-[color-mix(in_oklch,var(--warning)_10%,transparent)] px-3 py-2 text-[12.5px] text-[var(--warning)]">
          ⚠ 仓库过大，统计被截断，下面的数字是下界。
        </div>
      ) : null}
      <div className="flex h-3 overflow-hidden rounded-md ring-1 ring-inset ring-[var(--border)]">
        {entries.map(([lang, bytes], i) => (
          <div
            key={lang}
            title={`${lang} · ${bytes} bytes`}
            style={{
              width: `${(bytes / total) * 100}%`,
              background: `oklch(70% 0.12 ${(i * 47) % 360})`,
            }}
          />
        ))}
      </div>
      <ul className="mt-3 flex list-none flex-wrap gap-x-5 gap-y-1.5 p-0 m-0">
        {entries.map(([lang, bytes], i) => (
          <li key={lang} className="inline-flex items-center gap-1.5 text-[12px]">
            <span
              aria-hidden
              className="inline-block h-2.5 w-2.5 rounded-sm"
              style={{ background: `oklch(70% 0.12 ${(i * 47) % 360})` }}
            />
            <span className="font-medium text-fg">{lang}</span>
            <span className="text-muted">
              {((bytes / total) * 100).toFixed(1)}% · {stats.files[lang] ?? 0} 文件
            </span>
          </li>
        ))}
      </ul>
    </>
  );
}
