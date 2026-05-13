import { Button, Card, Description, Input, Label, TextField } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Repo, Visibility } from "@/api/types";

const VIS: Array<{ id: Visibility; label: string }> = [
  { id: "private", label: "私有" },
  { id: "internal", label: "内部" },
  { id: "public", label: "公开" },
];

export default function ReposIndex() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [items, setItems] = useState<Repo[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [slug, setSlug] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [defaultBranch, setDefaultBranch] = useState("main");
  const [visibility, setVisibility] = useState<Visibility>("private");
  const [showForm, setShowForm] = useState(false);
  const [creating, setCreating] = useState(false);

  function load() {
    setError(null);
    reposApi.list(org.slug, project.slug).then(setItems).catch((e) => setError(e as ApiError));
  }

  useEffect(load, [org.slug, project.slug]);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    try {
      await reposApi.create(org.slug, project.slug, {
        slug,
        display_name: displayName || undefined,
        default_branch: defaultBranch || "main",
        visibility,
      });
      setSlug("");
      setDisplayName("");
      setDefaultBranch("main");
      setVisibility("private");
      setShowForm(false);
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos`;

  return (
    <div>
      <header
        style={{
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          marginBottom: "1rem",
        }}
      >
        <h1 style={{ margin: 0, fontSize: "1.5rem" }}>仓库</h1>
        <Button onPress={() => setShowForm((v) => !v)}>
          <PlusIcon width={16} height={16} /> {showForm ? "取消" : "新建仓库"}
        </Button>
      </header>

      {showForm ? (
        <Card style={{ marginBottom: "1rem" }}>
          <Card.Content>
            <form
              onSubmit={onCreate}
              style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}
            >
              <TextField name="slug" value={slug} onChange={setSlug} isRequired>
                <Label>Slug</Label>
                <Input placeholder="my-repo" />
                <Description>2–64 字符，作为 URL 路径段。</Description>
              </TextField>
              <TextField name="display_name" value={displayName} onChange={setDisplayName}>
                <Label>显示名</Label>
                <Input />
              </TextField>
              <TextField name="default_branch" value={defaultBranch} onChange={setDefaultBranch}>
                <Label>默认分支</Label>
                <Input placeholder="main" />
              </TextField>
              <div>
                <div style={{ fontSize: "0.85rem", marginBottom: "0.4rem" }}>可见性</div>
                <div style={{ display: "flex", gap: "0.5rem" }}>
                  {VIS.map((v) => (
                    <label
                      key={v.id}
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: "0.3rem",
                        padding: "0.25rem 0.6rem",
                        border: "1px solid var(--border)",
                        borderRadius: "var(--field-radius)",
                        cursor: "pointer",
                        background: visibility === v.id ? "var(--surface-secondary)" : "var(--surface)",
                      }}
                    >
                      <input
                        type="radio"
                        name="visibility"
                        checked={visibility === v.id}
                        onChange={() => setVisibility(v.id)}
                      />
                      {v.label}
                    </label>
                  ))}
                </div>
              </div>
              <ErrorBanner error={error} />
              <div>
                <Button type="submit" isDisabled={creating || !slug}>
                  {creating ? "创建中…" : "创建"}
                </Button>
              </div>
            </form>
          </Card.Content>
        </Card>
      ) : (
        <ErrorBanner error={error} />
      )}

      {items === null ? (
        <Loading />
      ) : items.length === 0 ? (
        <EmptyState
          title="项目里还没有仓库"
          description="新建一个 bare 仓库，立刻就能 git push。"
          action={
            <Button onPress={() => setShowForm(true)}>
              <PlusIcon width={16} height={16} /> 新建仓库
            </Button>
          }
        />
      ) : (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))",
            gap: "1rem",
          }}
        >
          {items.map((r) => (
            <Link
              key={r.id}
              to={`${base}/${encodeURIComponent(r.slug)}`}
              style={{ color: "var(--foreground)", textDecoration: "none" }}
            >
              <Card>
                <Card.Header>
                  <Card.Title>{r.slug}</Card.Title>
                  <Card.Description>
                    {r.is_empty ? "空仓库" : `默认分支 ${r.default_branch}`}
                  </Card.Description>
                </Card.Header>
                <Card.Content>
                  <div style={{ color: "var(--muted)", minHeight: "1.2em" }}>
                    {r.description || "—"}
                  </div>
                  <div style={{ fontSize: "0.75rem", color: "var(--muted)", marginTop: "0.5rem" }}>
                    {Math.round(r.size_bytes / 1024)} KB · <RelativeTime iso={r.created_at} />
                  </div>
                </Card.Content>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
