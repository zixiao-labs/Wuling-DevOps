import { Button, Input, Label as HLabel, TextField } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import TagIcon from "@gravity-ui/icons/Tag";
import { useEffect, useState } from "react";

import { labels as labelsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { LabelChip } from "@/components/label-chip";
import { Loading } from "@/components/loading";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Label as LabelType } from "@/api/types";

const SUGGESTED_COLORS = [
  "FC6D26", "F4A261", "F1C40F", "61B763", "3CB6A8",
  "5A8FB0", "8B5CF6", "EC4899", "EF4444", "6B7280",
];

export default function LabelsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [items, setItems] = useState<LabelType[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [name, setName] = useState("");
  const [color, setColor] = useState("888888");
  const [description, setDescription] = useState("");
  const [creating, setCreating] = useState(false);

  function load() {
    setError(null);
    labelsApi.list(org.slug, project.slug).then(setItems).catch((e) => setError(e as ApiError));
  }
  useEffect(load, [org.slug, project.slug]);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    try {
      await labelsApi.create(org.slug, project.slug, {
        name,
        color: color || undefined,
        description: description || undefined,
      });
      setName("");
      setColor("888888");
      setDescription("");
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  async function onDelete(id: string) {
    if (!confirm("删除这个标签？已绑定的 issue 将自动解绑。")) return;
    try {
      await labelsApi.delete(org.slug, project.slug, id);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <PageContainer>
      <PageHeader
        title="标签"
        description="给 Issue / MR 分类与过滤；仅组织所有者 / 管理员可以创建。"
      />

      <Surface className="mb-4">
        <SurfaceHeader title="新建标签" />
        <SurfaceBody>
          <form onSubmit={onCreate} className="flex flex-col gap-3.5">
            <TextField name="name" value={name} onChange={setName} isRequired>
              <HLabel>名字</HLabel>
              <Input placeholder="bug / enhancement / …" />
            </TextField>
            <div className="grid gap-3.5 sm:grid-cols-2">
              <TextField name="color" value={color} onChange={setColor}>
                <HLabel>颜色（6 位十六进制，无 #）</HLabel>
                <Input placeholder="888888" />
              </TextField>
              <TextField name="description" value={description} onChange={setDescription}>
                <HLabel>说明</HLabel>
                <Input />
              </TextField>
            </div>
            <div>
              <div className="mb-1.5 text-[11.5px] uppercase tracking-wider text-muted">建议颜色</div>
              <div className="flex flex-wrap gap-1.5">
                {SUGGESTED_COLORS.map((c) => (
                  <button
                    key={c}
                    type="button"
                    onClick={() => setColor(c)}
                    aria-label={`使用颜色 #${c}`}
                    className={[
                      "h-5 w-5 rounded-md ring-1 ring-inset transition-transform",
                      color.toLowerCase() === c.toLowerCase()
                        ? "ring-fg scale-110"
                        : "ring-[var(--border)] hover:scale-105",
                    ].join(" ")}
                    style={{ background: `#${c}` }}
                  />
                ))}
              </div>
            </div>
            <ErrorBanner error={error} />
            <div className="flex items-center gap-3">
              <Button type="submit" isDisabled={creating || !name}>
                <PlusIcon width={14} height={14} /> {creating ? "创建中…" : "创建标签"}
              </Button>
              <span className="text-[11.5px] text-muted">预览：</span>
              <LabelChip
                label={{
                  id: "_",
                  project_id: "_",
                  name: name || "示例",
                  color: color || "888888",
                  description,
                  created_at: "",
                }}
              />
            </div>
          </form>
        </SurfaceBody>
      </Surface>

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            全部标签{items ? ` · ${items.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <Loading />
          ) : items.length === 0 ? (
            <EmptyState
              inset
              icon={<TagIcon width={20} height={20} />}
              title="项目还没有标签"
              description="标签让 Issue 分类与过滤变得更顺手。"
            />
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {items.map((l) => (
                <li key={l.id} className="flex items-center gap-3 px-4 py-2.5">
                  <LabelChip label={l} />
                  <span className="min-w-0 flex-1 truncate text-[12.5px] text-muted">
                    {l.description || "—"}
                  </span>
                  <button
                    type="button"
                    onClick={() => onDelete(l.id)}
                    className="grid h-7 w-7 place-items-center rounded-sm text-fg/60 hover:bg-[color-mix(in_oklch,var(--danger)_12%,transparent)] hover:text-[var(--danger)]"
                    aria-label={`删除 ${l.name}`}
                  >
                    <TrashIcon width={14} height={14} />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
