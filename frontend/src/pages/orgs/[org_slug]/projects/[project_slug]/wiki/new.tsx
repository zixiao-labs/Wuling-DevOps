import { Button, Description, Input, Label, TextArea, TextField } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useState } from "react";

import { wiki as wikiApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";

export default function NewWikiPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const navigate = useNavigate();

  const [path, setPath] = useState("");
  const [content, setContent] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState<ApiError | null>(null);
  const [saving, setSaving] = useState(false);

  const normalizedPath = path.endsWith(".md") ? path : path ? `${path}.md` : "";
  const pathSegments = normalizedPath ? normalizedPath.split("/") : [];
  const pathDepth = pathSegments.filter(Boolean).length;
  const hasBadSegment = pathSegments.some(
    (seg) => seg === "." || seg === ".." || seg.includes("#"),
  );
  const isInvalidPath = Boolean(
    normalizedPath &&
      (!/^[^<>:"|?*\x00-\x1f]+\.md$/.test(normalizedPath) ||
        normalizedPath.includes("//") ||
        normalizedPath.startsWith("/") ||
        hasBadSegment ||
        pathDepth > 8),
  );

  async function onSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (saving || !content || !normalizedPath || isInvalidPath) return;
    setSaving(true);
    setError(null);
    try {
      await wikiApi.put(org.slug, project.slug, normalizedPath, {
        content,
        message: message || undefined,
      });
      const encodedPath = normalizedPath.split("/").map(encodeURIComponent).join("/");
      navigate(
        `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/wiki/${encodedPath}`,
      );
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setSaving(false);
    }
  }

  return (
    <PageContainer>
      <PageHeader
        title="新建 Wiki 页面"
        description={
          <>使用斜杠分层，例如 <code className="font-mono">docs/usage.md</code>。最多 8 层。</>
        }
      />
      <div className="max-w-[900px]">
        <Surface>
          <SurfaceBody>
            <form onSubmit={onSubmit} className="flex flex-col gap-4">
              <TextField name="path" value={path} onChange={setPath} isRequired isInvalid={Boolean(isInvalidPath)}>
                <Label>路径</Label>
                <Input placeholder="Home  或  docs/usage" />
                <Description>不写 <code className="font-mono">.md</code> 会自动补上。</Description>
              </TextField>
              <TextField name="content" value={content} onChange={setContent} isRequired>
                <Label>正文</Label>
                <TextArea rows={22} placeholder="# 标题" className="font-mono text-[12.5px]" />
              </TextField>
              <TextField name="message" value={message} onChange={setMessage}>
                <Label>提交信息</Label>
                <Input placeholder={`Create ${normalizedPath || "page"}`} />
              </TextField>
              <ErrorBanner error={error} />
              <div className="flex justify-end">
                <Button type="submit" isDisabled={saving || !content || !normalizedPath || Boolean(isInvalidPath)}>
                  {saving ? "保存中…" : "保存并查看"}
                </Button>
              </div>
            </form>
          </SurfaceBody>
        </Surface>
      </div>
    </PageContainer>
  );
}
