import { Button, Description, Input, Label, TextArea, TextField } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { issues as issuesApi, labels as labelsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { LabelChip } from "@/components/label-chip";
import { Loading } from "@/components/loading";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Label as LabelType } from "@/api/types";

export default function NewIssuePage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const navigate = useNavigate();

  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [allLabels, setAllLabels] = useState<LabelType[] | null>(null);
  const [selectedLabels, setSelectedLabels] = useState<string[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    labelsApi.list(org.slug, project.slug).then(setAllLabels).catch(() => setAllLabels([]));
  }, [org.slug, project.slug]);

  async function onSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    try {
      const issue = await issuesApi.create(org.slug, project.slug, {
        title,
        body: body || undefined,
        labels: selectedLabels.length > 0 ? selectedLabels : undefined,
      });
      navigate(
        `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/issues/${issue.number}`,
      );
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  if (allLabels === null) return <Loading />;

  return (
    <PageContainer>
      <PageHeader title="新建 Issue" description="使用清晰的标题描述问题，正文里附上复现步骤或上下文。" />
      <div className="max-w-[760px]">
        <Surface>
          <SurfaceBody>
            <form onSubmit={onSubmit} className="flex flex-col gap-4">
              <TextField name="title" value={title} onChange={setTitle} isRequired>
                <Label>标题</Label>
                <Input placeholder="一句话概括这个 Issue" />
              </TextField>
              <TextField name="body" value={body} onChange={setBody}>
                <Label>正文</Label>
                <TextArea rows={10} placeholder="复现步骤、期望、实际…" />
                <Description>支持简易 Markdown。</Description>
              </TextField>
              {allLabels.length > 0 ? (
                <div>
                  <div className="mb-1.5 text-[12.5px] font-medium text-fg">标签</div>
                  <div className="flex flex-wrap gap-1.5">
                    {allLabels.map((l) => {
                      const selected = selectedLabels.includes(l.id);
                      return (
                        <button
                          type="button"
                          key={l.id}
                          onClick={() =>
                            setSelectedLabels((cur) =>
                              selected ? cur.filter((x) => x !== l.id) : [...cur, l.id],
                            )
                          }
                          className={[
                            "rounded-full p-px transition-all",
                            selected
                              ? "ring-2 ring-[var(--accent)]"
                              : "opacity-70 hover:opacity-100",
                          ].join(" ")}
                        >
                          <LabelChip label={l} />
                        </button>
                      );
                    })}
                  </div>
                </div>
              ) : null}
              <ErrorBanner error={error} />
              <div className="flex justify-end">
                <Button type="submit" isDisabled={creating || !title}>
                  {creating ? "创建中…" : "创建 Issue"}
                </Button>
              </div>
            </form>
          </SurfaceBody>
        </Surface>
      </div>
    </PageContainer>
  );
}
