import { Button, Card, Description, Input, Label, TextArea, TextField } from "@heroui/react";
import { useNavigate, useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { wiki as wikiApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RawMarkdownHtml } from "@/components/markdown";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { WikiPageContent } from "@/api/types";

export default function WikiPagePage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const navigate = useNavigate();
  const path = (params["*"] ?? "") as string;

  const [page, setPage] = useState<WikiPageContent | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [editing, setEditing] = useState(false);
  const [content, setContent] = useState("");
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setPage(null);
    setError(null);
    setEditing(false);
    if (!path) return;
    wikiApi
      .get(org.slug, project.slug, path)
      .then((p) => {
        setPage(p);
        setContent(p.raw);
      })
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, path]);

  async function save(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setSaving(true);
    try {
      const updated = await wikiApi.put(org.slug, project.slug, path, {
        content,
        message: message || undefined,
      });
      setPage(updated);
      setEditing(false);
      setMessage("");
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setSaving(false);
    }
  }

  async function del() {
    if (!confirm(`删除页面 ${path}？将创建一次删除提交。`)) return;
    try {
      await wikiApi.delete(org.slug, project.slug, path);
      navigate(
        `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/wiki`,
      );
    } catch (err) {
      setError(err as ApiError);
    }
  }

  if (error) return <ErrorBanner error={error} />;
  if (!page) return <Loading />;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <header style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
        <h1 style={{ margin: 0, fontSize: "1.5rem" }}>{page.path}</h1>
        <span style={{ color: "var(--muted)", fontSize: "0.85rem" }}>
          commit <code>{page.commit_oid.slice(0, 8)}</code>{" "}
          {/* Wiki list returns updated_at but single-page does not — fall back to "—" */}
        </span>
        <span style={{ flex: 1 }} />
        {editing ? null : (
          <>
            <Button variant="secondary" onPress={() => setEditing(true)}>
              编辑
            </Button>
            <Button variant="danger-soft" onPress={del}>
              删除
            </Button>
          </>
        )}
      </header>

      {editing ? (
        <Card>
          <Card.Content>
            <form onSubmit={save} style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}>
              <TextField name="content" value={content} onChange={setContent}>
                <Label>正文（Markdown）</Label>
                <TextArea
                  rows={20}
                  style={{ fontFamily: "ui-monospace, monospace", fontSize: "0.85rem" }}
                />
              </TextField>
              <TextField name="message" value={message} onChange={setMessage}>
                <Label>提交信息</Label>
                <Input placeholder={`Update ${path}`} />
                <Description>留空使用默认值。</Description>
              </TextField>
              <div style={{ display: "flex", gap: "0.5rem" }}>
                <Button type="submit" isDisabled={saving}>
                  {saving ? "保存中…" : "保存"}
                </Button>
                <Button
                  variant="tertiary"
                  type="button"
                  onPress={() => {
                    setEditing(false);
                    setContent(page!.raw);
                  }}
                >
                  取消
                </Button>
              </div>
            </form>
          </Card.Content>
        </Card>
      ) : (
        <Card>
          <Card.Content>
            <RawMarkdownHtml html={page.html} />
          </Card.Content>
        </Card>
      )}

      <PageMetaFooter />
    </div>
  );
}

function PageMetaFooter() {
  return (
    <div style={{ fontSize: "0.75rem", color: "var(--muted)" }}>
      Wiki HTML 由服务端通过 goldmark + bluemonday 净化，可直接渲染。
    </div>
  );
}
