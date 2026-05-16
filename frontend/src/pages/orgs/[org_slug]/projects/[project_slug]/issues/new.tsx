import { Button, Card, Description, Input, Label, TextArea, TextField } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { issues as issuesApi, labels as labelsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { LabelChip } from "@/components/label-chip";
import { Loading } from "@/components/loading";
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
    <Card style={{ maxWidth: 720 }}>
      <Card.Header>
        <Card.Title>新建 Issue</Card.Title>
      </Card.Header>
      <Card.Content>
        <form onSubmit={onSubmit} style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}>
          <TextField name="title" value={title} onChange={setTitle} isRequired>
            <Label>标题</Label>
            <Input />
          </TextField>
          <TextField name="body" value={body} onChange={setBody}>
            <Label>正文</Label>
            <TextArea rows={8} placeholder="复现步骤、期望、实际…" />
            <Description>支持简易 Markdown。</Description>
          </TextField>
          {allLabels.length > 0 ? (
            <div>
              <div style={{ fontSize: "0.85rem", marginBottom: "0.4rem" }}>标签</div>
              <div style={{ display: "flex", gap: "0.4rem", flexWrap: "wrap" }}>
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
                      style={{
                        border: selected ? "2px solid var(--accent)" : "1px solid var(--border)",
                        background: "transparent",
                        padding: 0,
                        borderRadius: "999px",
                        cursor: "pointer",
                      }}
                    >
                      <LabelChip label={l} />
                    </button>
                  );
                })}
              </div>
            </div>
          ) : null}
          <ErrorBanner error={error} />
          <div>
            <Button type="submit" isDisabled={creating || !title}>
              {creating ? "创建中…" : "创建 Issue"}
            </Button>
          </div>
        </form>
      </Card.Content>
    </Card>
  );
}
