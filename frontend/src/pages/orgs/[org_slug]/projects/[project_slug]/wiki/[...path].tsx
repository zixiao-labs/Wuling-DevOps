import { Button, Description, Input, Label, TextArea, TextField } from "@heroui/react";
import { useNavigate, useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { wiki as wikiApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RawMarkdownHtml } from "@/components/markdown";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
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
  const [inlineError, setInlineError] = useState<ApiError | null>(null);

  const [editing, setEditing] = useState(false);
  const [content, setContent] = useState("");
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setPage(null);
    setError(null);
    setInlineError(null);
    setEditing(false);
    if (!path) return;
    let cancelled = false;
    wikiApi
      .get(org.slug, project.slug, path)
      .then((p) => {
        if (cancelled) return;
        setPage(p);
        setContent(p.raw);
      })
      .catch((e) => {
        if (!cancelled) setError(e as ApiError);
      });
    return () => {
      cancelled = true;
    };
  }, [org.slug, project.slug, path]);

  async function save(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setSaving(true);
    setInlineError(null);
    try {
      const updated = await wikiApi.put(org.slug, project.slug, path, {
        content,
        message: message || undefined,
      });
      setPage(updated);
      setEditing(false);
      setMessage("");
    } catch (err) {
      setInlineError(err as ApiError);
    } finally {
      setSaving(false);
    }
  }

  async function del() {
    if (!confirm(`删除页面 ${path}？将创建一次删除提交。`)) return;
    setInlineError(null);
    try {
      await wikiApi.delete(org.slug, project.slug, path);
      navigate(
        `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/wiki`,
      );
    } catch (err) {
      setInlineError(err as ApiError);
    }
  }

  if (error && !page) {
    return (
      <PageContainer>
        <ErrorBanner error={error} />
      </PageContainer>
    );
  }
  if (!page) return <Loading />;

  return (
    <PageContainer>
      <PageHeader
        eyebrow={
          <span>
            commit <code className="font-mono">{page.commit_oid.slice(0, 8)}</code>
          </span>
        }
        title={<span className="font-mono">{page.path}</span>}
        actions={
          editing ? null : (
            <div className="flex items-center gap-2">
              <Button variant="outline" onPress={() => setEditing(true)}>
                编辑
              </Button>
              <Button variant="danger-soft" onPress={del}>
                删除
              </Button>
            </div>
          )
        }
      />

      {inlineError ? <ErrorBanner error={inlineError} /> : null}

      <Surface>
        {editing ? (
          <>
            <SurfaceHeader title="编辑页面" />
            <SurfaceBody>
              <form onSubmit={save} className="flex flex-col gap-4">
                <TextField name="content" value={content} onChange={setContent}>
                  <Label>正文（Markdown）</Label>
                  <TextArea rows={22} className="font-mono text-[12.5px]" />
                </TextField>
                <TextField name="message" value={message} onChange={setMessage}>
                  <Label>提交信息</Label>
                  <Input placeholder={`Update ${path}`} />
                  <Description>留空使用默认值。</Description>
                </TextField>
                <div className="flex justify-end gap-2">
                  <Button
                    variant="outline"
                    type="button"
                    onPress={() => {
                      setEditing(false);
                      setContent(page!.raw);
                    }}
                  >
                    取消
                  </Button>
                  <Button type="submit" isDisabled={saving}>
                    {saving ? "保存中…" : "保存"}
                  </Button>
                </div>
              </form>
            </SurfaceBody>
          </>
        ) : (
          <SurfaceBody>
            <RawMarkdownHtml html={page.html} />
          </SurfaceBody>
        )}
      </Surface>

      <p className="mt-3 text-[11px] text-muted">
        Wiki HTML 由服务端通过 goldmark + bluemonday 净化，可直接渲染。
      </p>
    </PageContainer>
  );
}
