import { Button, Input, Label, TextField } from "@heroui/react";
import Plus from "@gravity-ui/icons/Plus";
import { useEffect, useState } from "react";

import { iterations as iterationsApi } from "@/api/endpoints";
import type { IterationState, ProjectIteration } from "@/api/types";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { Pill } from "@/components/page/badges";
import { DataList, ListRow } from "@/components/page/data-list";
import { PageContainer, PageHeader, Surface, SurfaceBody, SurfaceHeader } from "@/components/page/primitives";

const STATE_LABEL: Record<IterationState, string> = { planned: "计划中", current: "当前 Sprint", closed: "已关闭" };

export default function SprintsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [items, setItems] = useState<ProjectIteration[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState("");
  const [goal, setGoal] = useState("");
  const [startsAt, setStartsAt] = useState("");
  const [endsAt, setEndsAt] = useState("");

  function load() {
    iterationsApi.list(org.slug, project.slug).then(setItems).catch((err) => setError(err as ApiError));
  }
  useEffect(load, [org.slug, project.slug]);

  async function create(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    try {
      await iterationsApi.create(org.slug, project.slug, { name, goal, starts_at: startsAt, ends_at: endsAt });
      setName(""); setGoal(""); setStartsAt(""); setEndsAt(""); setShowCreate(false); load();
    } catch (err) { setError(err as ApiError); }
  }

  async function setState(item: ProjectIteration, state: IterationState) {
    try {
      await iterationsApi.update(org.slug, project.slug, item.id, { state });
      load();
    } catch (err) { setError(err as ApiError); }
  }

  return (
    <PageContainer>
      <PageHeader eyebrow="PLAN · ITERATIONS" title="Sprints" description="按固定节奏规划交付目标，并把 Work Item 纳入当前迭代。" actions={<Button onPress={() => setShowCreate((v) => !v)}><Plus width={14} height={14} /> 新建 Sprint</Button>} />
      <ErrorBanner error={error} />
      {showCreate ? (
        <Surface className="mb-4">
          <SurfaceHeader title="新建 Sprint" />
          <SurfaceBody>
            <form onSubmit={create} className="grid gap-3 md:grid-cols-2">
              <TextField value={name} onChange={setName} isRequired><Label>名称</Label><Input placeholder="Sprint 1" /></TextField>
              <TextField value={goal} onChange={setGoal}><Label>Sprint Goal</Label><Input placeholder="本次迭代希望交付什么？" /></TextField>
              <TextField value={startsAt} onChange={setStartsAt} isRequired><Label>开始日期</Label><Input type="date" /></TextField>
              <TextField value={endsAt} onChange={setEndsAt} isRequired><Label>结束日期</Label><Input type="date" /></TextField>
              <div className="md:col-span-2 flex justify-end"><Button type="submit" isDisabled={!name || !startsAt || !endsAt}>创建 Sprint</Button></div>
            </form>
          </SurfaceBody>
        </Surface>
      ) : null}
      <Surface>
        <SurfaceHeader dense><span className="text-[12px] font-medium">迭代 · {items?.length ?? 0}</span></SurfaceHeader>
        {items === null ? <SkeletonRows count={5} /> : (
          <DataList>
            {items.map((item) => (
              <ListRow key={item.id} title={<span className="inline-flex items-center gap-2">{item.name}<Pill tone={item.state === "current" ? "success" : item.state === "closed" ? "neutral" : "warning"}>{STATE_LABEL[item.state]}</Pill></span>} subtitle={item.goal || "尚未设置 Sprint Goal"} meta={<span className="inline-flex items-center gap-2"><span>{new Date(item.starts_at).toLocaleDateString()} – {new Date(item.ends_at).toLocaleDateString()}</span>{item.state === "planned" ? <button className="text-[var(--accent)] hover:underline" onClick={() => void setState(item, "current")}>设为当前</button> : null}{item.state === "current" ? <button className="text-[var(--accent)] hover:underline" onClick={() => void setState(item, "closed")}>完成</button> : null}</span>} />
            ))}
          </DataList>
        )}
      </Surface>
    </PageContainer>
  );
}
