import { Button, Description, Input, Label, TextArea, TextField } from "@heroui/react";
import { useNavigate, useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import BranchesRight from "@gravity-ui/icons/BranchesRight";

import { mergeRequests as mrApi, repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { GitRef } from "@/api/types";

export default function NewMRPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const navigate = useNavigate();
  const repoSlug = params.repo_slug ?? "";

  const [refs, setRefs] = useState<GitRef[] | null>(null);
  const [sourceRef, setSourceRef] = useState("");
  const [targetRef, setTargetRef] = useState("");
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [error, setError] = useState<ApiError | null>(null);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    reposApi
      .refs(org.slug, project.slug, repoSlug)
      .then((r) => {
        const branches = r.filter((x) => x.is_branch);
        setRefs(branches);
        if (branches.length > 0) {
          const def = branches[0]!;
          setTargetRef(strip(def.name));
          setSourceRef(branches.length > 1 ? strip(branches[1]!.name) : strip(def.name));
        }
      })
      .catch((e) => {
        setRefs([]);
        setError(e as ApiError);
      });
  }, [org.slug, project.slug, repoSlug]);

  async function onSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    setError(null);
    try {
      const mr = await mrApi.create(org.slug, project.slug, repoSlug, {
        title,
        body: body || undefined,
        source_ref: sourceRef,
        target_ref: targetRef,
      });
      navigate(
        `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repoSlug)}/merge-requests/${mr.number}`,
      );
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  if (refs === null) return <Loading />;
  const sameBranch = !!sourceRef && sourceRef === targetRef;

  return (
    <PageContainer>
      <PageHeader title="新建合并请求" description="把 source 分支的提交合并到 target 分支。" />
      <div className="max-w-[760px]">
        <Surface>
          <SurfaceBody>
            <form onSubmit={onSubmit} className="flex flex-col gap-4">
              <div className="grid items-end gap-4 sm:grid-cols-[1fr_auto_1fr]">
                <BranchField label="Source · 合入源" value={sourceRef} onChange={setSourceRef} refs={refs} />
                <div className="grid h-8 w-8 place-items-center self-end justify-self-center text-muted">
                  <BranchesRight width={16} height={16} />
                </div>
                <BranchField label="Target · 合入目标" value={targetRef} onChange={setTargetRef} refs={refs} />
              </div>
              {sameBranch ? (
                <div className="rounded-md border border-[var(--warning)]/40 bg-[color-mix(in_oklch,var(--warning)_10%,transparent)] px-3 py-2 text-[12.5px] text-[var(--warning)]">
                  ⚠ Source 和 Target 不能是同一个分支。
                </div>
              ) : null}
              <TextField name="title" value={title} onChange={setTitle} isRequired>
                <Label>标题</Label>
                <Input placeholder={sourceRef && targetRef ? `Merge ${sourceRef} into ${targetRef}` : "MR 标题"} />
              </TextField>
              <TextField name="body" value={body} onChange={setBody}>
                <Label>描述</Label>
                <TextArea rows={8} placeholder="简述这个 MR 想解决什么问题…" />
                <Description>支持简易 Markdown。</Description>
              </TextField>
              <ErrorBanner error={error} />
              <div className="flex justify-end">
                <Button
                  type="submit"
                  isDisabled={creating || !title || !sourceRef || !targetRef || sameBranch}
                >
                  {creating ? "提交中…" : "提交 MR"}
                </Button>
              </div>
            </form>
          </SurfaceBody>
        </Surface>
      </div>
    </PageContainer>
  );
}

function BranchField({
  label,
  value,
  onChange,
  refs,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  refs: GitRef[];
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-[12.5px] font-medium text-fg">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-9 rounded-md border border-[var(--border)] bg-[var(--field-background)] px-2.5 font-mono text-[13px] text-[var(--field-foreground)] focus:outline-none focus:ring-2 focus:ring-[var(--focus)]"
      >
        {refs.map((r) => (
          <option key={r.name} value={strip(r.name)}>
            {strip(r.name)}
          </option>
        ))}
      </select>
    </label>
  );
}

function strip(r: string): string {
  return r.replace(/^refs\/(heads|tags)\//, "");
}
