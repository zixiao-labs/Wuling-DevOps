import { Card, Tabs } from "@heroui/react";
import { useEffect, useState } from "react";

import { insights as insightsApi, repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type {
  ActivityDay,
  ContributorStat,
  LanguageStats,
  Repo,
} from "@/api/types";

export default function InsightsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();

  const [repos, setRepos] = useState<Repo[] | null>(null);
  const [selectedRepo, setSelectedRepo] = useState<string>("");

  const [activity, setActivity] = useState<ActivityDay[] | null>(null);
  const [contribs, setContribs] = useState<ContributorStat[] | null>(null);
  const [langs, setLangs] = useState<LanguageStats | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    reposApi.list(org.slug, project.slug).then((r) => {
      setRepos(r);
      if (r.length > 0 && !selectedRepo) setSelectedRepo(r[0]!.slug);
    });
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

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <h1 style={{ margin: 0, fontSize: "1.5rem" }}>Insights</h1>
      <ErrorBanner error={error} />

      <Tabs defaultSelectedKey="activity">
        <Tabs.ListContainer>
          <Tabs.List aria-label="Insights">
            <Tabs.Tab id="activity">活动（30 天） <Tabs.Indicator /></Tabs.Tab>
            <Tabs.Tab id="contributors">贡献者 <Tabs.Indicator /></Tabs.Tab>
            <Tabs.Tab id="languages">语言 <Tabs.Indicator /></Tabs.Tab>
          </Tabs.List>
        </Tabs.ListContainer>

        <Tabs.Panel id="activity">
          {activity === null ? <Loading /> : <ActivityChart days={activity} />}
        </Tabs.Panel>

        <Tabs.Panel id="contributors">
          <RepoSelect repos={repos} value={selectedRepo} onChange={setSelectedRepo} />
          {contribs === null ? (
            <Loading />
          ) : contribs.length === 0 ? (
            <div style={{ color: "var(--muted)" }}>（无贡献者数据）</div>
          ) : (
            <ContributorsList list={contribs} />
          )}
        </Tabs.Panel>

        <Tabs.Panel id="languages">
          <RepoSelect repos={repos} value={selectedRepo} onChange={setSelectedRepo} />
          {langs === null ? <Loading /> : <Languages stats={langs} />}
        </Tabs.Panel>
      </Tabs>
    </div>
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
  if (repos.length === 0)
    return <div style={{ color: "var(--muted)" }}>项目里还没有仓库。</div>;
  return (
    <label style={{ display: "inline-flex", gap: "0.5rem", alignItems: "center", margin: "0.5rem 0" }}>
      <span style={{ fontSize: "0.85rem" }}>仓库</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          padding: "0.25rem 0.5rem",
          background: "var(--field-background)",
          color: "var(--field-foreground)",
          border: "1px solid var(--border)",
          borderRadius: "var(--field-radius)",
        }}
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
    return <div style={{ color: "var(--muted)" }}>（无活动）</div>;
  }
  // Tiny stacked bars: commits + issues_opened + mrs_merged per day.
  const max = Math.max(
    1,
    ...days.map((d) => d.commits + d.issues_opened + d.mrs_merged + d.issues_closed + d.mrs_opened),
  );
  return (
    <Card>
      <Card.Content>
        <div style={{ display: "flex", alignItems: "flex-end", height: 120, gap: 2 }}>
          {days.map((d) => {
            const total = d.commits + d.issues_opened + d.mrs_merged;
            const h = Math.max(2, (total / max) * 110);
            return (
              <div
                key={d.date}
                title={`${d.date}\ncommits: ${d.commits}\nissues_opened: ${d.issues_opened}\nmrs_merged: ${d.mrs_merged}`}
                style={{
                  flex: 1,
                  minWidth: 4,
                  height: h,
                  background: "var(--accent)",
                  opacity: total === 0 ? 0.25 : 1,
                  borderRadius: 2,
                }}
              />
            );
          })}
        </div>
        <div style={{ display: "flex", justifyContent: "space-between", fontSize: "0.75rem", color: "var(--muted)", marginTop: 4 }}>
          <span>{days[0]?.date}</span>
          <span>{days[days.length - 1]?.date}</span>
        </div>
      </Card.Content>
    </Card>
  );
}

function ContributorsList({ list }: { list: ContributorStat[] }) {
  const max = Math.max(1, ...list.map((c) => c.commits));
  return (
    <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
      {list.map((c) => (
        <li key={c.email + c.name} style={{ padding: "0.5rem 0", borderBottom: "1px solid var(--separator)" }}>
          <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 4, fontSize: "0.9rem" }}>
            <span>
              {c.name} <span style={{ color: "var(--muted)" }}>&lt;{c.email}&gt;</span>
            </span>
            <span style={{ color: "var(--muted)" }}>{c.commits} 提交</span>
          </div>
          <div style={{ height: 6, background: "var(--surface-secondary)", borderRadius: 3, overflow: "hidden" }}>
            <div style={{ width: `${(c.commits / max) * 100}%`, height: "100%", background: "var(--accent)" }} />
          </div>
        </li>
      ))}
    </ul>
  );
}

function Languages({ stats }: { stats: LanguageStats }) {
  const entries = Object.entries(stats.bytes).sort((a, b) => b[1] - a[1]);
  const total = entries.reduce((s, [, b]) => s + b, 0) || 1;
  return (
    <>
      {stats.truncated ? (
        <div style={{ color: "var(--warning)", marginBottom: "0.5rem", fontSize: "0.85rem" }}>
          ⚠ 仓库过大，统计被截断，下面的数字是下界。
        </div>
      ) : null}
      <div style={{ display: "flex", height: 12, borderRadius: 6, overflow: "hidden" }}>
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
      <ul style={{ listStyle: "none", padding: 0, margin: "0.5rem 0 0", display: "flex", flexWrap: "wrap", gap: "1rem" }}>
        {entries.map(([lang, bytes], i) => (
          <li key={lang} style={{ display: "flex", alignItems: "center", gap: "0.4rem", fontSize: "0.85rem" }}>
            <span
              style={{
                display: "inline-block",
                width: 10,
                height: 10,
                background: `oklch(70% 0.12 ${(i * 47) % 360})`,
                borderRadius: 2,
              }}
            />
            <span>{lang}</span>
            <span style={{ color: "var(--muted)" }}>
              {((bytes / total) * 100).toFixed(1)}% · {stats.files[lang] ?? 0} 文件
            </span>
          </li>
        ))}
      </ul>
    </>
  );
}
