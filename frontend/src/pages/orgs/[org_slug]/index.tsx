import { Button, Description, Input, Label, TextField } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import FolderIcon from "@gravity-ui/icons/Folder";
import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { projects as projectsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { DataList, ListRow } from "@/components/page/data-list";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { VisibilityIcon } from "@/components/page/badges";
import { RelativeTime } from "@/components/relative-time";
import { useOrgCtx } from "@/auth/org-context";
import type { Project, Visibility } from "@/api/types";

const VIS: Array<{ id: Visibility; label: string; hint: string }> = [
  { id: "private", label: "私有", hint: "仅成员可见，默认选项" },
  { id: "internal", label: "内部", hint: "登录用户均可查看" },
  { id: "public", label: "公开", hint: "未登录访客也能查看" },
];

export default function OrgProjectsPage() {
  const org = useOrgCtx();
  const [items, setItems] = useState<Project[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [slug, setSlug] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [description, setDescription] = useState("");
  const [visibility, setVisibility] = useState<Visibility>("private");
  const [showForm, setShowForm] = useState(false);
  const [creating, setCreating] = useState(false);

  function load() {
    setError(null);
    projectsApi.list(org.slug).then(setItems).catch((e) => setError(e as ApiError));
  }

  useEffect(load, [org.slug]);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    try {
      await projectsApi.create(org.slug, {
        slug,
        display_name: displayName || undefined,
        description: description || undefined,
        visibility,
      });
      setSlug("");
      setDisplayName("");
      setDescription("");
      setVisibility("private");
      setShowForm(false);
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  return (
    <PageContainer>
      <PageHeader
        eyebrow={
          <span className="inline-flex items-center gap-1">
            <Link to="/orgs" className="hover:text-fg hover:underline">组织</Link>
            <span>·</span>
            <span className="font-mono text-fg">@{org.slug}</span>
          </span>
        }
        icon={
          <span
            aria-hidden
            className="grid h-full w-full place-items-center text-[15px] font-bold uppercase text-[var(--accent-foreground)]"
            style={{ background: "var(--accent)", margin: "-1px", borderRadius: "inherit" }}
          >
            {(org.display_name || org.slug).slice(0, 1)}
          </span>
        }
        title={org.display_name || org.slug}
        description={org.description || "（暂无简介）"}
        actions={
          <Button onPress={() => setShowForm((v) => !v)}>
            <PlusIcon width={14} height={14} /> {showForm ? "取消" : "新建项目"}
          </Button>
        }
      />

      {showForm ? (
        <Surface className="mb-4">
          <SurfaceHeader title="新建项目" description="项目是仓库、Issues、MR、Wiki 与 Insights 的承载单元。" />
          <SurfaceBody>
            <form onSubmit={onCreate} className="flex flex-col gap-3.5">
              <TextField name="slug" value={slug} onChange={setSlug} isRequired>
                <Label>Slug</Label>
                <Input placeholder="wuling-devops" />
                <Description>2–64 字符，作为 URL 路径段。</Description>
              </TextField>
              <TextField name="display_name" value={displayName} onChange={setDisplayName}>
                <Label>显示名</Label>
                <Input />
              </TextField>
              <TextField name="description" value={description} onChange={setDescription}>
                <Label>简介</Label>
                <Input />
              </TextField>
              <div>
                <div className="mb-1.5 text-[12.5px] font-medium text-fg">可见性</div>
                <div className="grid gap-2 sm:grid-cols-3">
                  {VIS.map((v) => (
                    <label
                      key={v.id}
                      className={[
                        "cursor-pointer rounded-md border px-3 py-2 transition-colors",
                        visibility === v.id
                          ? "border-[var(--accent)] bg-[var(--surface-secondary)]"
                          : "border-[var(--border)] bg-[var(--surface)] hover:bg-[var(--surface-secondary)]",
                      ].join(" ")}
                    >
                      <div className="flex items-center gap-2">
                        <input
                          type="radio"
                          name="visibility"
                          checked={visibility === v.id}
                          onChange={() => setVisibility(v.id)}
                          className="accent-[var(--accent)]"
                        />
                        <span className="inline-flex items-center gap-1 text-[13px] font-medium text-fg">
                          <VisibilityIcon v={v.id} size={12} />
                          {v.label}
                        </span>
                      </div>
                      <div className="mt-1 text-[11.5px] text-muted">{v.hint}</div>
                    </label>
                  ))}
                </div>
              </div>
              <ErrorBanner error={error} />
              <div className="flex justify-end">
                <Button type="submit" isDisabled={creating || !slug}>
                  {creating ? "创建中…" : "创建项目"}
                </Button>
              </div>
            </form>
          </SurfaceBody>
        </Surface>
      ) : (
        <ErrorBanner error={error} />
      )}

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            项目{items ? ` · ${items.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <SkeletonRows count={4} />
          ) : items.length === 0 ? (
            <EmptyState
              inset
              icon={<FolderIcon width={20} height={20} />}
              title="这个组织里还没有项目"
              description="项目是仓库、Issues、MR、Wiki 与 Insights 的承载单元。"
              action={
                <Button onPress={() => setShowForm(true)}>
                  <PlusIcon width={14} height={14} /> 新建项目
                </Button>
              }
            />
          ) : (
            <DataList>
              {items.map((p) => (
                <ListRow
                  key={p.id}
                  to={`/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(p.slug)}`}
                  icon={
                    <span
                      aria-hidden
                      className="grid h-8 w-8 place-items-center rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] text-fg/70"
                    >
                      <VisibilityIcon v={p.visibility} size={14} />
                    </span>
                  }
                  title={
                    <span className="inline-flex items-center gap-2">
                      <span>{p.display_name || p.slug}</span>
                    </span>
                  }
                  subtitle={
                    <span>
                      <code className="font-mono text-[11px] text-muted">{p.slug}</code>
                      {p.description ? <span className="ml-2 text-muted">· {p.description}</span> : null}
                    </span>
                  }
                  meta={
                    <span>
                      <RelativeTime iso={p.created_at} /> 创建
                    </span>
                  }
                />
              ))}
            </DataList>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
