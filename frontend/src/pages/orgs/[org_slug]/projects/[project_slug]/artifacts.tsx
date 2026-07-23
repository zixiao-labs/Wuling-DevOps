import { Button, Input, Label, ListBox, Select, Tabs, TextArea, TextField } from "@heroui/react";
import Plus from "@gravity-ui/icons/Plus";
import { useEffect, useState } from "react";

import { artifactRegistry } from "@/api/endpoints";
import type { ArtifactPackage, PackageKind, ProjectRelease } from "@/api/types";
import { ApiError } from "@/api/errors";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { Pill } from "@/components/page/badges";
import { DataList, ListRow } from "@/components/page/data-list";
import { PageContainer, PageHeader, Surface, SurfaceBody, SurfaceHeader } from "@/components/page/primitives";
import { RelativeTime } from "@/components/relative-time";

const PACKAGE_KINDS: Array<{ id: PackageKind; label: string }> = [
  { id: "npm", label: "npm" }, { id: "pypi", label: "PyPI" }, { id: "cargo", label: "Cargo" },
  { id: "docker", label: "Docker" }, { id: "logos", label: "Logos Extension" },
];

export default function ArtifactsPage() {
  const org = useOrgCtx(); const project = useProjectCtx();
  const [packages, setPackages] = useState<ArtifactPackage[] | null>(null);
  const [releases, setReleases] = useState<ProjectRelease[] | null>(null);
  const [createMode, setCreateMode] = useState<"package" | "release" | null>(null);
  const [name, setName] = useState(""); const [kind, setKind] = useState<PackageKind>("npm");
  const [tag, setTag] = useState(""); const [notes, setNotes] = useState("");
  const [error, setError] = useState<ApiError | null>(null);
  function load() { Promise.all([artifactRegistry.listPackages(org.slug, project.slug), artifactRegistry.listReleases(org.slug, project.slug)]).then(([p, r]) => { setPackages(p); setReleases(r); }).catch((err) => setError(err as ApiError)); }
  useEffect(load, [org.slug, project.slug]);
  async function create(e: React.SyntheticEvent<HTMLFormElement>) { e.preventDefault(); try { if (createMode === "package") await artifactRegistry.createPackage(org.slug, project.slug, { kind, name, description: notes }); else await artifactRegistry.createRelease(org.slug, project.slug, { tag_name: tag, name, notes, publish: true }); setName(""); setTag(""); setNotes(""); setCreateMode(null); load(); } catch (err) { setError(err as ApiError); } }
  return <PageContainer>
    <PageHeader eyebrow="DEPLOY · ARTIFACTS" title="Artifacts" description="统一管理 Package Registry、容器镜像、Logos 扩展与 Release；二进制内容由独立 Artifact Service 写入 Blob Storage。" actions={<><Button variant="secondary" onPress={() => setCreateMode("release")}><Plus width={14} height={14} /> Release</Button><Button onPress={() => setCreateMode("package")}><Plus width={14} height={14} /> Package</Button></>} />
    <ErrorBanner error={error} />
    {createMode ? <Surface className="mb-4"><SurfaceHeader title={createMode === "package" ? "注册 Package" : "发布 Release"} actions={<Button size="sm" variant="ghost" onPress={() => setCreateMode(null)}>取消</Button>} /><SurfaceBody><form onSubmit={create} className="grid gap-3 md:grid-cols-2">
      {createMode === "package" ? <Select value={kind} onChange={(value) => value && setKind(String(value) as PackageKind)}><Label>Registry 类型</Label><Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger><Select.Popover><ListBox>{PACKAGE_KINDS.map((item) => <ListBox.Item key={item.id} id={item.id} textValue={item.label}>{item.label}<ListBox.ItemIndicator /></ListBox.Item>)}</ListBox></Select.Popover></Select> : <TextField value={tag} onChange={setTag} isRequired><Label>Tag</Label><Input placeholder="v2.0.0" /></TextField>}
      <TextField value={name} onChange={setName} isRequired><Label>{createMode === "package" ? "Package 名称" : "Release 标题"}</Label><Input /></TextField>
      <TextField className="md:col-span-2" value={notes} onChange={setNotes}><Label>{createMode === "package" ? "说明" : "Release Notes"}</Label><TextArea rows={4} /></TextField>
      <div className="md:col-span-2 flex justify-end"><Button type="submit" isDisabled={!name || (createMode === "release" && !tag)}>创建</Button></div>
    </form></SurfaceBody></Surface> : null}
    <Tabs variant="secondary" defaultSelectedKey="packages">
      <Tabs.ListContainer><Tabs.List aria-label="Artifact 类型"><Tabs.Tab id="packages">Packages<Tabs.Indicator /></Tabs.Tab><Tabs.Tab id="releases">Releases<Tabs.Indicator /></Tabs.Tab></Tabs.List></Tabs.ListContainer>
      <Tabs.Panel id="packages" className="pt-4"><Surface><SurfaceHeader dense><span className="text-[12px] font-medium">Package Registry · {packages?.length ?? 0}</span></SurfaceHeader>{packages === null ? <SkeletonRows count={5} /> : packages.length === 0 ? <EmptyState inset title="还没有 Package" description="支持 npm、PyPI、Cargo、Docker 与 Logos Extension Registry。" /> : <DataList>{packages.map((item) => <ListRow key={item.id} title={<span className="inline-flex items-center gap-2"><span className="font-mono">{item.name}</span><Pill>{item.kind}</Pill></span>} subtitle={item.description || "暂无说明"} meta={<span>{item.versions} versions · <RelativeTime iso={item.updated_at} /></span>} />)}</DataList>}</Surface></Tabs.Panel>
      <Tabs.Panel id="releases" className="pt-4"><Surface><SurfaceHeader dense><span className="text-[12px] font-medium">Releases · {releases?.length ?? 0}</span></SurfaceHeader>{releases === null ? <SkeletonRows count={5} /> : releases.length === 0 ? <EmptyState inset title="还没有 Release" description="用 Tag 和 Release Notes 记录每次可交付版本。" /> : <DataList>{releases.map((item) => <ListRow key={item.id} title={<span className="inline-flex items-center gap-2">{item.name}<Pill tone={item.prerelease ? "warning" : "success"}>{item.tag_name}</Pill></span>} subtitle={item.notes || "暂无 Release Notes"} meta={<RelativeTime iso={item.created_at} />} />)}</DataList>}</Surface></Tabs.Panel>
    </Tabs>
  </PageContainer>;
}
