import { Button, Input, Label, ListBox, Select, TextField } from "@heroui/react";
import Plus from "@gravity-ui/icons/Plus";
import { useEffect, useMemo, useState } from "react";

import { projectSuite, workItems as workItemsApi } from "@/api/endpoints";
import type { ProjectSettings, WorkItem, WorkItemState, WorkItemType } from "@/api/types";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { Pill } from "@/components/page/badges";
import { PageContainer, PageHeader, Surface, SurfaceBody, SurfaceHeader } from "@/components/page/primitives";
import {
  groupWorkItems,
  shortWorkItemID,
  totalStoryPoints,
  WORK_ITEM_STATES,
  WORK_ITEM_TYPES,
} from "@/features/stage2/planning";

export default function BacklogsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [items, setItems] = useState<WorkItem[] | null>(null);
  const [settings, setSettings] = useState<ProjectSettings | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [title, setTitle] = useState("");
  const [type, setType] = useState<WorkItemType>("user_story");
  const [points, setPoints] = useState("");
  const [creating, setCreating] = useState(false);

  function load() {
    setError(null);
    Promise.all([
      workItemsApi.list(org.slug, project.slug),
      projectSuite.settings(org.slug, project.slug),
    ])
      .then(([nextItems, nextSettings]) => {
        setItems(nextItems);
        setSettings(nextSettings);
      })
      .catch((err) => setError(err as ApiError));
  }

  useEffect(load, [org.slug, project.slug]);

  const grouped = useMemo(() => groupWorkItems(items ?? []), [items]);

  async function create(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    try {
      await workItemsApi.create(org.slug, project.slug, {
        title,
        type,
        story_points: points ? Number(points) : undefined,
      });
      setTitle("");
      setPoints("");
      setShowCreate(false);
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  async function move(item: WorkItem, state: WorkItemState) {
    if (item.state === state) return;
    setItems((current) => current?.map((value) => value.id === item.id ? { ...value, state } : value) ?? null);
    try {
      await workItemsApi.update(org.slug, project.slug, item.number, { state });
    } catch (err) {
      setError(err as ApiError);
      load();
    }
  }

  return (
    <PageContainer wide>
      <PageHeader
        eyebrow="PLAN · BACKLOG"
        title="Backlog 与 Scrum 看板"
        description="所有 Epic、Feature、User Story、Task 和 Bug 都在同一项目 Backlog 中排序与流转。"
        actions={
          <Button onPress={() => setShowCreate((value) => !value)}>
            <Plus width={14} height={14} /> {showCreate ? "取消" : "新建 Work Item"}
          </Button>
        }
      />

      <ErrorBanner error={error} />

      {showCreate ? (
        <Surface className="mb-4">
          <SurfaceHeader title="添加 Backlog 项" description="创建后默认进入“待办”列。" />
          <SurfaceBody>
            <form onSubmit={create} className="grid gap-3 md:grid-cols-[minmax(0,1fr)_180px_120px_auto] md:items-end">
              <TextField value={title} onChange={setTitle} isRequired>
                <Label>标题</Label>
                <Input placeholder="描述用户价值或待完成的工作" />
              </TextField>
              <Select value={type} onChange={(value) => value && setType(String(value) as WorkItemType)}>
                <Label>类型</Label>
                <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {WORK_ITEM_TYPES.map((item) => (
                      <ListBox.Item key={item.id} id={item.id} textValue={item.label}>
                        {item.label}<ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>
              <TextField value={points} onChange={setPoints}>
                <Label>Story Points</Label>
                <Input type="number" min="0" step="0.5" placeholder="—" />
              </TextField>
              <Button type="submit" isDisabled={creating || !title.trim()}>
                {creating ? "创建中…" : "添加"}
              </Button>
            </form>
          </SurfaceBody>
        </Surface>
      ) : null}

      <div className="mb-3 flex flex-wrap items-center gap-2 text-[11.5px] text-muted">
        <Pill>{items?.length ?? 0} items</Pill>
        <Pill>{totalStoryPoints(items ?? [])} points</Pill>
        <span>拖动卡片可更新状态 · Process: {settings?.process_template ?? "…"}</span>
      </div>

      {items === null ? (
        <Surface><SkeletonRows count={6} /></Surface>
      ) : (
        <div className="grid min-w-[920px] grid-cols-4 gap-3 overflow-x-auto pb-4">
          {WORK_ITEM_STATES.map((column) => (
            <section
              key={column.id}
              className="min-h-[420px] rounded-md border border-[var(--border)] bg-[var(--surface-secondary)]"
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => {
                const id = e.dataTransfer.getData("text/work-item-id");
                const item = items.find((value) => value.id === id);
                if (item) void move(item, column.id);
              }}
            >
              <div className="flex h-10 items-center justify-between border-b border-[var(--border)] px-3">
                <span className="text-[12.5px] font-semibold text-fg">{column.label}</span>
                <span className="rounded-full bg-[var(--surface)] px-2 py-0.5 text-[10px] text-muted">{grouped[column.id].length}</span>
              </div>
              <div className="grid gap-2 p-2">
                {grouped[column.id].map((item) => (
                  <article
                    key={item.id}
                    draggable
                    onDragStart={(e) => e.dataTransfer.setData("text/work-item-id", item.id)}
                    className="cursor-grab rounded-md border border-[var(--border)] bg-[var(--surface)] p-3 shadow-sm active:cursor-grabbing"
                  >
                    <div className="mb-2 flex items-center justify-between gap-2">
                      <span className="font-mono text-[10.5px] text-muted">{shortWorkItemID(settings?.work_item_prefix ?? "", item.number)}</span>
                      <span className="text-[10px] uppercase tracking-wide text-muted">{WORK_ITEM_TYPES.find((value) => value.id === item.type)?.label}</span>
                    </div>
                    <div className="text-[13px] font-medium leading-5 text-fg">{item.title}</div>
                    <div className="mt-3 flex items-center justify-between text-[10.5px] text-muted">
                      <span>P{item.priority}</span>
                      <span>{item.story_points == null ? "未估点" : `${item.story_points} pts`}</span>
                    </div>
                    <div
                      className="mt-3"
                      draggable={false}
                      onDragStart={(e) => e.stopPropagation()}
                    >
                      <Select
                        value={item.state}
                        variant="secondary"
                        onChange={(value) => value && void move(item, String(value) as WorkItemState)}
                      >
                        <Label className="sr-only">移动 “{item.title}” 到</Label>
                        <Select.Trigger className="min-h-8 text-[11px]">
                          <Select.Value />
                          <Select.Indicator />
                        </Select.Trigger>
                        <Select.Popover>
                          <ListBox>
                            {WORK_ITEM_STATES.map((state) => (
                              <ListBox.Item key={state.id} id={state.id} textValue={state.label}>
                                {state.label}<ListBox.ItemIndicator />
                              </ListBox.Item>
                            ))}
                          </ListBox>
                        </Select.Popover>
                      </Select>
                    </div>
                  </article>
                ))}
                {grouped[column.id].length === 0 ? (
                  <div className="rounded-md border border-dashed border-[var(--border)] px-3 py-8 text-center text-[11.5px] text-muted">拖动到这里</div>
                ) : null}
              </div>
            </section>
          ))}
        </div>
      )}
    </PageContainer>
  );
}
