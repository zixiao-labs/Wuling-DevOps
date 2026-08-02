import {
  Alert,
  Button,
  Description,
  Input,
  Label,
  TextArea,
  TextField,
} from "@heroui/react";
import Gear from "@gravity-ui/icons/Gear";
import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { orgMembers, runnerConfig as runnerConfigApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { OrgRole, RunnerConfig } from "@/api/types";
import { useOrgCtx } from "@/auth/org-context";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";

/** Seed shown when the org has not committed runner-config.yaml yet. */
const EMPTY_SEED = `# runner-config.yaml — 组织级 Runner / Autoscaler 配置（GitOps）
# 保存后写入 {org}/config/config 仓库根目录。云凭证用 credentials_secret 引用
# 「机密」里的名称，不要写明文。完整示例见 runners/config/runner-config.example.yaml。

version: 1
default_tier: medium
idle_timeout: 5m

tiers:
  low:
    cpu: 2
    memory: 4Gi
    storage: 40Gi
  medium:
    cpu: 4
    memory: 8Gi
    storage: 80Gi
  high:
    cpu: 8
    memory: 16Gi
    storage: 160Gi

# 按需添加 pools（aliyun / aws）。保存前请在「机密」里配置 credentials_secret。
pools: []
`;

const ROLE_RANK: Record<OrgRole, number> = {
  owner: 50,
  maintainer: 40,
  developer: 30,
  reporter: 20,
  guest: 10,
};

function canEditRunnerConfig(role: OrgRole | null): boolean {
  return role !== null && ROLE_RANK[role] >= ROLE_RANK.maintainer;
}

function detailString(details: Record<string, unknown> | undefined, key: string): string | undefined {
  const v = details?.[key];
  return typeof v === "string" && v.length > 0 ? v : undefined;
}

export default function RunnerConfigPage() {
  const org = useOrgCtx();
  const secretsHref = `/orgs/${encodeURIComponent(org.slug)}/secrets`;

  const [meta, setMeta] = useState<RunnerConfig | null>(null);
  const [content, setContent] = useState("");
  const [message, setMessage] = useState("");
  const [blobSha, setBlobSha] = useState("");
  const [myRole, setMyRole] = useState<OrgRole | null>(null);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [saveError, setSaveError] = useState<ApiError | null>(null);
  const [conflict, setConflict] = useState(false);
  const [successNote, setSuccessNote] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);

  const canEdit = canEditRunnerConfig(myRole);

  function applyConfig(cfg: RunnerConfig) {
    setMeta(cfg);
    setBlobSha(cfg.blob_sha);
    const next = cfg.exists ? cfg.content : EMPTY_SEED;
    setContent(next);
    // Seeded template is not yet committed — allow Save without requiring an edit.
    setDirty(!cfg.exists);
    setConflict(false);
    setSaveError(null);
  }

  function load() {
    setMeta(null);
    setLoadError(null);
    setSaveError(null);
    setConflict(false);
    setSuccessNote(null);
    Promise.all([
      runnerConfigApi.get(org.slug),
      orgMembers.list(org.slug).catch(() => null),
    ])
      .then(([cfg, members]) => {
        applyConfig(cfg);
        if (members) setMyRole(members.role);
      })
      .catch((e) => setLoadError(e as ApiError));
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [org.slug]);

  async function onSave(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canEdit || !meta) return;
    setSaving(true);
    setSaveError(null);
    setConflict(false);
    setSuccessNote(null);
    try {
      const res = await runnerConfigApi.put(org.slug, {
        content,
        message: message.trim() || undefined,
        base_blob_sha: blobSha,
      });
      applyConfig(res);
      setMessage("");
      const notes: string[] = [];
      if (res.created_project || res.created_repo) {
        notes.push(
          `已自动创建 ${res.project_slug}/${res.repo_slug} 仓库`,
        );
      }
      if (res.unchanged) {
        notes.push("内容未变化，未产生新提交");
      } else {
        notes.push("已提交到 config 仓库");
      }
      setSuccessNote(notes.join(" · "));
    } catch (err) {
      const ae = err as ApiError;
      if (ae.status === 409) {
        setConflict(true);
        setSaveError(ae);
      } else {
        setSaveError(ae);
      }
    } finally {
      setSaving(false);
    }
  }

  if (loadError) {
    return (
      <PageContainer>
        <PageHeader title="自动扩缩容" description="编辑组织的 runner-config.yaml。" />
        <ErrorBanner error={loadError} />
      </PageContainer>
    );
  }

  if (!meta) return <Loading />;

  const parseErrorFromSave = detailString(saveError?.details, "parse_error");
  const configRepoLabel = `${meta.project_slug}/${meta.repo_slug}`;

  return (
    <PageContainer>
      <PageHeader
        icon={<Gear width={18} height={18} />}
        title="自动扩缩容"
        description={
          <>
            编辑 GitOps 管理的{" "}
            <code className="rounded bg-[var(--surface-secondary)] px-1 py-0.5 font-mono text-[12px]">
              runner-config.yaml
            </code>
            。保存即提交到{" "}
            <code className="rounded bg-[var(--surface-secondary)] px-1 py-0.5 font-mono text-[12px]">
              {configRepoLabel}
            </code>
            。云凭证请在{" "}
            <Link to={secretsHref} className="text-[var(--accent)] underline-offset-2 hover:underline">
              机密
            </Link>{" "}
            里按 <code className="font-mono text-[12px]">credentials_secret</code> 名称配置。
            需要维护者及以上权限才能保存。
          </>
        }
        actions={
          <Button variant="outline" size="sm" onPress={load} isDisabled={saving}>
            重新加载
          </Button>
        }
      />

      {!canEdit ? (
        <Alert status="warning" className="mb-4">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>只读</Alert.Title>
            <Alert.Description>
              当前角色无法编辑 runner-config.yaml。需要维护者或所有者权限才能保存。
            </Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      {!meta.exists ? (
        <Alert status="accent" className="mb-4">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>尚未配置自动扩缩容</Alert.Title>
            <Alert.Description>
              下方是示例模板。首次保存会自动创建 {configRepoLabel} 仓库并写入该文件。
            </Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      {meta.exists && !meta.valid ? (
        <Alert status="danger" className="mb-4">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>已提交的配置无法解析</Alert.Title>
            <Alert.Description>
              自动扩缩容会忽略此文件，直到修复并重新保存。
              {meta.parse_error ? (
                <pre className="mt-2 overflow-x-auto whitespace-pre-wrap font-mono text-[12px]">
                  {meta.parse_error}
                </pre>
              ) : null}
            </Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      {meta.warnings.length > 0 ? (
        <Alert status="warning" className="mb-4">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>配置警告</Alert.Title>
            <Alert.Description>
              <ul className="mt-1 list-inside list-disc space-y-1">
                {meta.warnings.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            </Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      {conflict ? (
        <Alert status="danger" className="mb-4">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>配置已被他人更新</Alert.Title>
            <Alert.Description>
              <p>
                {saveError?.message ??
                  "runner-config.yaml 自你加载后已变更。请重新加载后再合并你的修改。"}
              </p>
              {detailString(saveError?.details, "current_blob_sha") ? (
                <p className="mt-1 font-mono text-[11.5px] text-muted">
                  current_blob_sha: {detailString(saveError?.details, "current_blob_sha")}
                </p>
              ) : null}
              <div className="mt-3">
                <Button size="sm" variant="outline" onPress={load}>
                  重新加载最新版本
                </Button>
              </div>
            </Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      {saveError && !conflict ? (
        <Alert status="danger" className="mb-4">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>
              {saveError.code === "validation" ? "配置无效" : "保存失败"}
            </Alert.Title>
            <Alert.Description>
              <span>{saveError.message}</span>
              {parseErrorFromSave ? (
                <pre className="mt-2 overflow-x-auto whitespace-pre-wrap font-mono text-[12px]">
                  {parseErrorFromSave}
                </pre>
              ) : null}
            </Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      {successNote ? (
        <Alert status="success" className="mb-4">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>已保存</Alert.Title>
            <Alert.Description>{successNote}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      <Surface>
        <SurfaceHeader
          dense
          title={
            <span className="font-mono text-[12px]">
              {meta.path}
              {meta.exists ? "" : " · 未提交"}
              {dirty ? " · 未保存" : ""}
            </span>
          }
          description={
            meta.exists && meta.updated_at ? (
              <span className="text-[11.5px] text-muted">
                最近由 {meta.updated_by || "—"} · <RelativeTime iso={meta.updated_at} />
                {meta.branch ? ` · ${meta.branch}` : ""}
                {meta.commit_sha ? (
                  <span className="ml-1 font-mono">{meta.commit_sha.slice(0, 8)}</span>
                ) : null}
              </span>
            ) : (
              <span className="text-[11.5px] text-muted">尚未写入 config 仓库</span>
            )
          }
        />
        <SurfaceBody>
          <form onSubmit={onSave} className="flex flex-col gap-3.5">
            <TextField
              name="content"
              value={content}
              onChange={(v) => {
                setContent(v);
                setDirty(true);
                setSuccessNote(null);
              }}
              isDisabled={!canEdit || saving}
              className="w-full"
            >
              <Label>YAML</Label>
              <TextArea
                aria-label="runner-config.yaml"
                className="min-h-[28rem] w-full font-mono text-[12.5px] leading-relaxed"
                rows={24}
                spellCheck={false}
                style={{ resize: "vertical" }}
                variant="secondary"
              />
              <Description>
                原始 YAML，注释会原样保留。乐观并发令牌为 blob SHA
                {blobSha ? (
                  <>
                    ：<code className="font-mono text-[11px]">{blobSha.slice(0, 12)}…</code>
                  </>
                ) : (
                  "（空 = 首次创建）"
                )}
                。
              </Description>
            </TextField>

            <TextField
              name="message"
              value={message}
              onChange={setMessage}
              isDisabled={!canEdit || saving}
            >
              <Label>提交说明（可选）</Label>
              <Input placeholder="Update runner-config.yaml" variant="secondary" />
              <Description>留空则使用默认说明。</Description>
            </TextField>

            <div className="flex items-center justify-between gap-3">
              <p className="text-[12px] text-muted">
                保存后约一个 reconcile 周期（≤20s）生效。
              </p>
              <Button type="submit" isDisabled={!canEdit || saving || !dirty}>
                {saving ? "保存中…" : "保存并提交"}
              </Button>
            </div>
          </form>
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
