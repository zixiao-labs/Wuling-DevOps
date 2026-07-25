import { Button, Input, Label, ListBox, Select, Tabs, TextArea, TextField } from "@heroui/react";
import FileArrowUp from "@gravity-ui/icons/FileArrowUp";
import Plus from "@gravity-ui/icons/Plus";
import { useCallback, useEffect, useState } from "react";

import { artifactRegistry } from "@/api/endpoints";
import type {
  ArtifactPackage,
  PackageKind,
  PackageVersion,
  ProjectRelease,
} from "@/api/types";
import { ApiError } from "@/api/errors";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { Pill } from "@/components/page/badges";
import { DataList, ListRow } from "@/components/page/data-list";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { RelativeTime } from "@/components/relative-time";

const PACKAGE_KINDS: Array<{ id: PackageKind; label: string }> = [
  { id: "npm", label: "npm" },
  { id: "pypi", label: "PyPI" },
  { id: "cargo", label: "Cargo" },
  { id: "docker", label: "Docker" },
  { id: "logos", label: "Logos Extension" },
];

type PanelMode = "package" | "release" | "upload" | null;

function formatBytes(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`;
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(1)} MiB`;
  return `${(size / 1024 / 1024 / 1024).toFixed(1)} GiB`;
}

export default function ArtifactsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [packages, setPackages] = useState<ArtifactPackage[] | null>(null);
  const [releases, setReleases] = useState<ProjectRelease[] | null>(null);
  const [panelMode, setPanelMode] = useState<PanelMode>(null);
  const [name, setName] = useState("");
  const [kind, setKind] = useState<PackageKind>("npm");
  const [tag, setTag] = useState("");
  const [notes, setNotes] = useState("");
  const [uploadPackageID, setUploadPackageID] = useState("");
  const [uploadVersion, setUploadVersion] = useState("");
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [uploadInputKey, setUploadInputKey] = useState(0);
  const [uploading, setUploading] = useState(false);
  const [uploaded, setUploaded] = useState<PackageVersion | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const load = useCallback(() => {
    return Promise.all([
      artifactRegistry.listPackages(org.slug, project.slug),
      artifactRegistry.listReleases(org.slug, project.slug),
    ])
      .then(([nextPackages, nextReleases]) => {
        setPackages(nextPackages);
        setReleases(nextReleases);
        setUploadPackageID((current) =>
          nextPackages.some((item) => item.id === current)
            ? current
            : nextPackages[0]?.id || "",
        );
      })
      .catch((err) => setError(err as ApiError));
  }, [org.slug, project.slug]);

  useEffect(() => {
    void load();
  }, [load]);

  function resetPanel(nextMode: PanelMode) {
    setName("");
    setKind("npm");
    setTag("");
    setNotes("");
    setUploaded(null);
    setUploadVersion("");
    setUploadFile(null);
    setUploadInputKey((value) => value + 1);
    setPanelMode(nextMode);
  }

  function closePanel() {
    resetPanel(null);
  }

  async function create(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    try {
      if (panelMode === "package") {
        await artifactRegistry.createPackage(org.slug, project.slug, {
          kind,
          name,
          description: notes,
        });
      } else {
        await artifactRegistry.createRelease(org.slug, project.slug, {
          tag_name: tag,
          name,
          notes,
          publish: true,
        });
      }
      closePanel();
      await load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  async function upload(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!uploadFile || !uploadPackageID || !uploadVersion.trim()) return;
    setError(null);
    setUploaded(null);
    setUploading(true);
    try {
      const version = await artifactRegistry.uploadVersion(
        org.slug,
        project.slug,
        uploadPackageID,
        uploadVersion.trim(),
        uploadFile,
      );
      setUploaded(version);
      setUploadVersion("");
      setUploadFile(null);
      setUploadInputKey((value) => value + 1);
      await load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setUploading(false);
    }
  }

  const panelTitle =
    panelMode === "package"
      ? "注册 Package"
      : panelMode === "release"
        ? "发布 Release"
        : "手动上传 Artifact";

  return (
    <PageContainer>
      <PageHeader
        eyebrow="DEPLOY · ARTIFACTS"
        title="Artifacts"
        description="统一管理 Package Registry、容器镜像、Logos 扩展与 Release；二进制内容由独立 Artifact Service 写入 Blob Storage。"
        actions={
          <>
            <Button
              variant="secondary"
              onPress={() => resetPanel("release")}
            >
              <Plus width={14} height={14} /> Release
            </Button>
            <Button
              variant="secondary"
              onPress={() => resetPanel("package")}
            >
              <Plus width={14} height={14} /> Package
            </Button>
            <Button
              onPress={() => resetPanel("upload")}
              isDisabled={packages?.length === 0}
            >
              <FileArrowUp width={14} height={14} /> 上传 Artifact
            </Button>
          </>
        }
      />
      <ErrorBanner error={error} />

      {panelMode ? (
        <Surface className="mb-4">
          <SurfaceHeader
            title={panelTitle}
            actions={
              <Button size="sm" variant="ghost" onPress={closePanel} isDisabled={uploading}>
                取消
              </Button>
            }
          />
          <SurfaceBody>
            {panelMode === "upload" ? (
              <form onSubmit={upload} className="grid gap-3 md:grid-cols-2">
                <Select
                  value={uploadPackageID}
                  onChange={(value) => value && setUploadPackageID(String(value))}
                  isRequired
                >
                  <Label>Package</Label>
                  <Select.Trigger>
                    <Select.Value />
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox>
                      {(packages ?? []).map((item) => (
                        <ListBox.Item
                          key={item.id}
                          id={item.id}
                          textValue={`${item.name} (${item.kind})`}
                        >
                          <span className="font-mono">{item.name}</span>
                          <span className="ml-2 text-muted">{item.kind}</span>
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                      ))}
                    </ListBox>
                  </Select.Popover>
                </Select>
                <TextField
                  value={uploadVersion}
                  onChange={setUploadVersion}
                  isRequired
                  maxLength={128}
                >
                  <Label>版本</Label>
                  <Input placeholder="1.0.0" />
                </TextField>
                <label className="md:col-span-2 grid gap-1.5 text-[13px] text-fg">
                  <span>Artifact 文件</span>
                  <input
                    key={uploadInputKey}
                    type="file"
                    required
                    disabled={uploading}
                    onChange={(event) => setUploadFile(event.target.files?.[0] ?? null)}
                    className="block w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-[12.5px] file:mr-3 file:rounded file:border-0 file:bg-[var(--surface-secondary)] file:px-2.5 file:py-1 file:text-[12px] file:font-medium file:text-fg"
                  />
                  <span className="text-[11.5px] text-muted">
                    文件将经项目权限校验后写入不可变 Blob Storage，并自动记录大小、SHA-256
                    与 Content-Type。
                  </span>
                </label>
                {uploaded ? (
                  <div className="md:col-span-2 rounded-md border border-[color:var(--success,#2f855a)]/30 bg-[color:var(--success,#2f855a)]/10 px-3 py-2 text-[12.5px] text-fg">
                    已上传版本 <span className="font-mono">{uploaded.version}</span> ·{" "}
                    {formatBytes(uploaded.size_bytes)} · SHA-256{" "}
                    <span className="font-mono">{uploaded.sha256.slice(0, 12)}…</span>
                  </div>
                ) : null}
                <div className="md:col-span-2 flex justify-end">
                  <Button
                    type="submit"
                    isPending={uploading}
                    isDisabled={
                      uploading ||
                      !uploadPackageID ||
                      !uploadVersion.trim() ||
                      uploadFile === null
                    }
                  >
                    {uploading ? "上传中…" : "上传"}
                  </Button>
                </div>
              </form>
            ) : (
              <form onSubmit={create} className="grid gap-3 md:grid-cols-2">
                {panelMode === "package" ? (
                  <Select
                    value={kind}
                    onChange={(value) => value && setKind(String(value) as PackageKind)}
                  >
                    <Label>Registry 类型</Label>
                    <Select.Trigger>
                      <Select.Value />
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox>
                        {PACKAGE_KINDS.map((item) => (
                          <ListBox.Item key={item.id} id={item.id} textValue={item.label}>
                            {item.label}
                            <ListBox.ItemIndicator />
                          </ListBox.Item>
                        ))}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                ) : (
                  <TextField value={tag} onChange={setTag} isRequired>
                    <Label>Tag</Label>
                    <Input placeholder="v2.0.0" />
                  </TextField>
                )}
                <TextField value={name} onChange={setName} isRequired>
                  <Label>{panelMode === "package" ? "Package 名称" : "Release 标题"}</Label>
                  <Input />
                </TextField>
                <TextField className="md:col-span-2" value={notes} onChange={setNotes}>
                  <Label>{panelMode === "package" ? "说明" : "Release Notes"}</Label>
                  <TextArea rows={4} />
                </TextField>
                <div className="md:col-span-2 flex justify-end">
                  <Button
                    type="submit"
                    isDisabled={!name || (panelMode === "release" && !tag)}
                  >
                    创建
                  </Button>
                </div>
              </form>
            )}
          </SurfaceBody>
        </Surface>
      ) : null}

      <Tabs variant="secondary" defaultSelectedKey="packages">
        <Tabs.ListContainer>
          <Tabs.List aria-label="Artifact 类型">
            <Tabs.Tab id="packages">
              Packages
              <Tabs.Indicator />
            </Tabs.Tab>
            <Tabs.Tab id="releases">
              Releases
              <Tabs.Indicator />
            </Tabs.Tab>
          </Tabs.List>
        </Tabs.ListContainer>
        <Tabs.Panel id="packages" className="pt-4">
          <Surface>
            <SurfaceHeader dense>
              <span className="text-[12px] font-medium">
                Package Registry · {packages?.length ?? 0}
              </span>
            </SurfaceHeader>
            {packages === null ? (
              <SkeletonRows count={5} />
            ) : packages.length === 0 ? (
              <EmptyState
                inset
                title="还没有 Package"
                description="先注册 npm、PyPI、Cargo、Docker 或 Logos Extension Package，再手动上传版本文件。"
              />
            ) : (
              <DataList>
                {packages.map((item) => (
                  <ListRow
                    key={item.id}
                    title={
                      <span className="inline-flex items-center gap-2">
                        <span className="font-mono">{item.name}</span>
                        <Pill>{item.kind}</Pill>
                      </span>
                    }
                    subtitle={item.description || "暂无说明"}
                    meta={
                      <span>
                        {item.versions} versions · <RelativeTime iso={item.updated_at} />
                      </span>
                    }
                  />
                ))}
              </DataList>
            )}
          </Surface>
        </Tabs.Panel>
        <Tabs.Panel id="releases" className="pt-4">
          <Surface>
            <SurfaceHeader dense>
              <span className="text-[12px] font-medium">
                Releases · {releases?.length ?? 0}
              </span>
            </SurfaceHeader>
            {releases === null ? (
              <SkeletonRows count={5} />
            ) : releases.length === 0 ? (
              <EmptyState
                inset
                title="还没有 Release"
                description="用 Tag 和 Release Notes 记录每次可交付版本。"
              />
            ) : (
              <DataList>
                {releases.map((item) => (
                  <ListRow
                    key={item.id}
                    title={
                      <span className="inline-flex items-center gap-2">
                        {item.name}
                        <Pill tone={item.prerelease ? "warning" : "success"}>
                          {item.tag_name}
                        </Pill>
                      </span>
                    }
                    subtitle={item.notes || "暂无 Release Notes"}
                    meta={<RelativeTime iso={item.created_at} />}
                  />
                ))}
              </DataList>
            )}
          </Surface>
        </Tabs.Panel>
      </Tabs>
    </PageContainer>
  );
}
