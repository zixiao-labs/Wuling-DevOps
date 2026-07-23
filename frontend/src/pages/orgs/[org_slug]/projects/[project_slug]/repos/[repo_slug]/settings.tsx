import { Button, Checkbox, Description, Input, Label, Switch, TextField } from "@heroui/react";
import { useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { repositorySettings } from "@/api/endpoints";
import type { RepoSettings } from "@/api/types";
import { ApiError } from "@/api/errors";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { Breadcrumbs, PageContainer, PageHeader, Surface, SurfaceBody, SurfaceHeader } from "@/components/page/primitives";

type Strategy = RepoSettings["merge_strategies"][number];

export default function RepositorySettingsPage() {
  const org = useOrgCtx(); const project = useProjectCtx(); const params = useParams();
  const repo = params.repo_slug ?? ""; const [settings, setSettings] = useState<RepoSettings | null>(null);
  const [topics, setTopics] = useState(""); const [error, setError] = useState<ApiError | null>(null); const [saving, setSaving] = useState(false);
  useEffect(() => { repositorySettings.get(org.slug, project.slug, repo).then((next) => { setSettings(next); setTopics(next.topics.join(", ")); }).catch((err) => setError(err as ApiError)); }, [org.slug, project.slug, repo]);
  if (!settings) return <Loading />;
  function toggleStrategy(strategy: Strategy, enabled: boolean) { setSettings((current) => { if (!current) return current; const next = enabled ? [...current.merge_strategies, strategy] : current.merge_strategies.filter((item) => item !== strategy); return { ...current, merge_strategies: Array.from(new Set(next)) }; }); }
  async function save(e: React.SyntheticEvent<HTMLFormElement>) { e.preventDefault(); if (!settings) return; setSaving(true); try { const next = await repositorySettings.update(org.slug, project.slug, repo, { ...settings, topics: topics.split(",").map((item) => item.trim()).filter(Boolean) }); setSettings(next); setTopics(next.topics.join(", ")); } catch (err) { setError(err as ApiError); } finally { setSaving(false); } }
  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repo)}`;
  return <PageContainer><Breadcrumbs items={[{ label: project.display_name || project.slug, to: `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}` }, { label: repo, to: base }, { label: "Settings" }]} /><PageHeader eyebrow="REPOSITORY · SETUP" title={`${repo} 设置`} description="GitHub 风格的仓库功能、Topic 与合并策略配置。" /><ErrorBanner error={error} />
    <form onSubmit={save} className="grid gap-4"><Surface><SurfaceHeader title="General" /><SurfaceBody className="grid max-w-2xl gap-4"><TextField value={settings.default_branch} onChange={(value) => setSettings({ ...settings, default_branch: value })}><Label>默认分支</Label><Input /><Description>需要指向仓库中已经存在的分支。</Description></TextField><TextField value={topics} onChange={setTopics}><Label>Topics</Label><Input placeholder="devops, go, react" /><Description>最多 20 个，用逗号分隔。</Description></TextField><Switch isSelected={settings.issues_enabled} onChange={(issues_enabled) => setSettings({ ...settings, issues_enabled })}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control>启用 Issues</Switch.Content></Switch><Switch isSelected={settings.wiki_enabled} onChange={(wiki_enabled) => setSettings({ ...settings, wiki_enabled })}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control>启用 Wiki</Switch.Content></Switch></SurfaceBody></Surface>
      <Surface><SurfaceHeader title="Pull / Merge Request" description="选择仓库允许使用的合并方式。" /><SurfaceBody className="grid gap-3"><StrategyCheckbox label="Create a merge commit" selected={settings.merge_strategies.includes("merge")} onChange={(value) => toggleStrategy("merge", value)} /><StrategyCheckbox label="Squash merging" selected={settings.merge_strategies.includes("squash")} onChange={(value) => toggleStrategy("squash", value)} /><StrategyCheckbox label="Rebase merging" selected={settings.merge_strategies.includes("rebase")} onChange={(value) => toggleStrategy("rebase", value)} /><Switch isSelected={settings.delete_branch_on_merge} onChange={(delete_branch_on_merge) => setSettings({ ...settings, delete_branch_on_merge })}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control>合并后自动删除源分支</Switch.Content></Switch></SurfaceBody></Surface>
      <div><Button type="submit" isPending={saving} isDisabled={settings.merge_strategies.length === 0}>{saving ? "保存中…" : "保存仓库设置"}</Button></div>
    </form>
  </PageContainer>;
}

function StrategyCheckbox({ label, selected, onChange }: { label: string; selected: boolean; onChange: (value: boolean) => void }) {
  return <Checkbox isSelected={selected} onChange={onChange}><Checkbox.Content><Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>{label}</Checkbox.Content></Checkbox>;
}
