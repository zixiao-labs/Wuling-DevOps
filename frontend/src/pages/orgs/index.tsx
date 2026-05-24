import { Button, Description, Input, Label, TextField } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import LayersIcon from "@gravity-ui/icons/Layers";
import { useEffect, useState } from "react";

import { orgs as orgsApi } from "@/api/endpoints";
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
import { Pill } from "@/components/page/badges";
import { RelativeTime } from "@/components/relative-time";
import { RequireAuth } from "@/auth/guards";
import type { Org } from "@/api/types";

export default function OrgsIndex() {
  return (
    <RequireAuth>
      <OrgsList />
    </RequireAuth>
  );
}

function OrgsList() {
  const [items, setItems] = useState<Org[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [slug, setSlug] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [description, setDescription] = useState("");
  const [showForm, setShowForm] = useState(false);
  const [creating, setCreating] = useState(false);

  function load() {
    setError(null);
    orgsApi.list().then(setItems).catch((e) => setError(e as ApiError));
  }

  useEffect(load, []);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    try {
      await orgsApi.create({
        slug,
        display_name: displayName || undefined,
        description: description || undefined,
      });
      setSlug("");
      setDisplayName("");
      setDescription("");
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
        title="组织"
        description="组织是项目和成员的承载单元。每个用户都自带一个同名个人组织。"
        actions={
          <Button onPress={() => setShowForm((v) => !v)}>
            <PlusIcon width={14} height={14} /> {showForm ? "取消" : "新建组织"}
          </Button>
        }
      />

      {showForm ? (
        <Surface className="mb-4">
          <SurfaceHeader title="新建组织" description="Slug 决定 URL 路径段；显示名可以稍后修改。" />
          <SurfaceBody>
            <form onSubmit={onCreate} className="flex flex-col gap-3.5">
              <TextField name="slug" value={slug} onChange={setSlug} isRequired>
                <Label>Slug</Label>
                <Input placeholder="zixiao-labs" />
                <Description>2–64 字符，作为 URL 路径段。</Description>
              </TextField>
              <TextField name="display_name" value={displayName} onChange={setDisplayName}>
                <Label>显示名</Label>
                <Input placeholder="紫霄实验室" />
              </TextField>
              <TextField name="description" value={description} onChange={setDescription}>
                <Label>简介</Label>
                <Input />
              </TextField>
              <ErrorBanner error={error} />
              <div className="flex justify-end">
                <Button type="submit" isDisabled={creating || !slug}>
                  {creating ? "创建中…" : "创建组织"}
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
            全部组织{items ? ` · ${items.length}` : ""}
          </span>
          {items && items.length > 0 ? (
            <span className="text-[11.5px] text-muted">点击进入查看项目</span>
          ) : null}
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <SkeletonRows count={4} />
          ) : items.length === 0 ? (
            <EmptyState
              inset
              icon={<LayersIcon width={20} height={20} />}
              title="还没有任何组织"
              description="新建你的第一个组织来托管项目。"
              action={
                <Button onPress={() => setShowForm(true)}>
                  <PlusIcon width={14} height={14} /> 新建组织
                </Button>
              }
            />
          ) : (
            <DataList>
              {items.map((o) => (
                <ListRow
                  key={o.id}
                  to={`/orgs/${encodeURIComponent(o.slug)}`}
                  icon={
                    <span
                      aria-hidden
                      className="grid h-8 w-8 place-items-center rounded-md text-[12px] font-semibold uppercase text-[var(--accent-foreground)]"
                      style={{ background: "var(--accent)" }}
                    >
                      {(o.display_name || o.slug).slice(0, 1)}
                    </span>
                  }
                  title={
                    <span className="inline-flex items-center gap-2">
                      <span>{o.display_name || o.slug}</span>
                      {o.is_personal ? <Pill tone="neutral">个人</Pill> : null}
                    </span>
                  }
                  subtitle={
                    <span>
                      <code className="font-mono text-[11px] text-muted">@{o.slug}</code>
                      {o.description ? <span className="ml-2 text-muted">· {o.description}</span> : null}
                    </span>
                  }
                  meta={
                    <span>
                      <RelativeTime iso={o.created_at} /> 创建
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
