import { Button, Card, Input, Label, TextField } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { useEffect, useState } from "react";

import { labels as labelsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { LabelChip } from "@/components/label-chip";
import { Loading } from "@/components/loading";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Label as LabelType } from "@/api/types";

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
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <h1 style={{ margin: 0, fontSize: "1.5rem" }}>标签</h1>
      <Card>
        <Card.Header>
          <Card.Title>新建标签</Card.Title>
          <Card.Description>用于在 issue 上分类。仅组织所有者/管理员可创建。</Card.Description>
        </Card.Header>
        <Card.Content>
          <form onSubmit={onCreate} style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}>
            <TextField name="name" value={name} onChange={setName} isRequired>
              <Label>名字</Label>
              <Input placeholder="bug / enhancement / …" />
            </TextField>
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "0.9rem" }}>
              <TextField name="color" value={color} onChange={setColor}>
                <Label>颜色（6 位十六进制，无 #）</Label>
                <Input placeholder="888888" />
              </TextField>
              <TextField name="description" value={description} onChange={setDescription}>
                <Label>说明</Label>
                <Input />
              </TextField>
            </div>
            <ErrorBanner error={error} />
            <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
              <Button type="submit" isDisabled={creating || !name}>
                <PlusIcon width={16} height={16} /> {creating ? "创建中…" : "创建"}
              </Button>
              <span style={{ color: "var(--muted)", fontSize: "0.85rem" }}>预览：</span>
              <LabelChip label={{ id: "_", project_id: "_", name: name || "示例", color: color || "888888", description, created_at: "" }} />
            </div>
          </form>
        </Card.Content>
      </Card>

      {items === null ? (
        <Loading />
      ) : items.length === 0 ? (
        <EmptyState title="项目还没有标签" description="标签让 issue 分类与过滤变得更顺手。" />
      ) : (
        <Card>
          <Card.Content>
            <div style={{ display: "flex", gap: "0.6rem", flexWrap: "wrap" }}>
              {items.map((l) => (
                <div
                  key={l.id}
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: "0.5rem",
                    border: "1px solid var(--border)",
                    padding: "0.3rem 0.6rem",
                    borderRadius: "var(--field-radius)",
                  }}
                >
                  <LabelChip label={l} />
                  <span style={{ color: "var(--muted)", fontSize: "0.8rem", maxWidth: 240 }}>
                    {l.description || ""}
                  </span>
                  <button
                    onClick={() => onDelete(l.id)}
                    style={{
                      border: "none",
                      background: "transparent",
                      color: "var(--danger)",
                      cursor: "pointer",
                    }}
                    aria-label={`删除 ${l.name}`}
                  >
                    <TrashIcon width={14} height={14} />
                  </button>
                </div>
              ))}
            </div>
          </Card.Content>
        </Card>
      )}
    </div>
  );
}
