import { Button, Description, Input, Label, ListBox, Select, Switch, TextField } from "@heroui/react";
import { useEffect, useState } from "react";

import { projectSuite } from "@/api/endpoints";
import type { ProcessTemplate, ProjectSettings } from "@/api/types";
import { ApiError } from "@/api/errors";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { PageContainer, PageHeader, Surface, SurfaceBody, SurfaceHeader } from "@/components/page/primitives";

export default function ProjectSettingsPage() {
  const org = useOrgCtx(); const project = useProjectCtx();
  const [settings, setSettings] = useState<ProjectSettings | null>(null);
  const [error, setError] = useState<ApiError | null>(null); const [saving, setSaving] = useState(false);
  useEffect(() => { projectSuite.settings(org.slug, project.slug).then(setSettings).catch((err) => setError(err as ApiError)); }, [org.slug, project.slug]);
  if (!settings) return <Loading />;
  async function save(e: React.SyntheticEvent<HTMLFormElement>) { e.preventDefault(); if (!settings) return; setSaving(true); try { setSettings(await projectSuite.updateSettings(org.slug, project.slug, settings)); } catch (err) { setError(err as ApiError); } finally { setSaving(false); } }
  return <PageContainer><PageHeader eyebrow="PROJECT · SETUP" title="项目设置" description="Azure DevOps 风格的项目过程配置；更改 Process 不会删除已有 Work Item。" /><ErrorBanner error={error} />
    <Surface><SurfaceHeader title="Project Setup" description="选择团队使用的工作过程、编号前缀和迭代节奏。" /><SurfaceBody><form onSubmit={save} className="grid max-w-2xl gap-5">
      <Select value={settings.process_template} onChange={(value) => value && setSettings({ ...settings, process_template: String(value) as ProcessTemplate })}><Label>Process</Label><Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger><Select.Popover><ListBox><ListBox.Item id="scrum" textValue="Scrum">Scrum<ListBox.ItemIndicator /></ListBox.Item><ListBox.Item id="kanban" textValue="Kanban">Kanban<ListBox.ItemIndicator /></ListBox.Item><ListBox.Item id="basic" textValue="Basic">Basic<ListBox.ItemIndicator /></ListBox.Item></ListBox></Select.Popover><Description>Scrum 提供完整 Backlog 与 Sprint；Kanban 以持续流为主。</Description></Select>
      <div className="grid gap-4 sm:grid-cols-2"><TextField value={settings.work_item_prefix} onChange={(value) => setSettings({ ...settings, work_item_prefix: value.toUpperCase() })}><Label>Work Item 前缀</Label><Input placeholder="WL" /><Description>例如 WL-128；留空则显示 #128。</Description></TextField><TextField value={String(settings.iteration_length_days)} onChange={(value) => setSettings({ ...settings, iteration_length_days: Number(value) || 1 })}><Label>默认 Sprint 天数</Label><Input type="number" min="1" max="90" /></TextField></div>
      <Switch isSelected={settings.archived} onChange={(archived) => setSettings({ ...settings, archived })}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control>归档项目</Switch.Content><Description>归档后作为只读历史项目保留。</Description></Switch>
      <div><Button type="submit" isPending={saving}>{saving ? "保存中…" : "保存设置"}</Button></div>
    </form></SurfaceBody></Surface>
  </PageContainer>;
}
