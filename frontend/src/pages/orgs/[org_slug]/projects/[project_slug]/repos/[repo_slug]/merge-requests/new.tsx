import { Button, Card, Description, Input, Label, TextArea, TextField } from "@heroui/react";
import { useNavigate, useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { mergeRequests as mrApi, repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
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
      .catch((e) => setError(e as ApiError));
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

  return (
    <Card style={{ maxWidth: 720 }}>
      <Card.Header>
        <Card.Title>新建合并请求</Card.Title>
        <Card.Description>把 source 分支的提交合并到 target 分支。</Card.Description>
      </Card.Header>
      <Card.Content>
        <form onSubmit={onSubmit} style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "0.9rem" }}>
            <BranchField label="Source（合入源）" value={sourceRef} onChange={setSourceRef} refs={refs} />
            <BranchField label="Target（合入目标）" value={targetRef} onChange={setTargetRef} refs={refs} />
          </div>
          <TextField name="title" value={title} onChange={setTitle} isRequired>
            <Label>标题</Label>
            <Input />
          </TextField>
          <TextField name="body" value={body} onChange={setBody}>
            <Label>描述</Label>
            <TextArea rows={6} placeholder="简述这个 MR 想解决什么问题…" />
            <Description>支持简易 Markdown。</Description>
          </TextField>
          <ErrorBanner error={error} />
          <div>
            <Button type="submit" isDisabled={creating || !title || !sourceRef || !targetRef || sourceRef === targetRef}>
              {creating ? "提交中…" : "提交 MR"}
            </Button>
          </div>
        </form>
      </Card.Content>
    </Card>
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
    <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem" }}>
      <span style={{ fontSize: "0.85rem" }}>{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          padding: "0.4rem 0.5rem",
          background: "var(--field-background)",
          color: "var(--field-foreground)",
          border: "1px solid var(--border)",
          borderRadius: "var(--field-radius)",
        }}
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
