import { Button, Card, Description, Input, Label, TextArea, TextField } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useState } from "react";

import { wiki as wikiApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
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
  // Reject filesystem-reserved chars (so Windows checkouts and our backend
  // both stay happy) and cap nesting at 8 segments to match the UI hint.
  // Unicode page names are allowed.
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
    // Mirror the submit button's disable guard so keyboard / programmatic
    // submits can't bypass it.
    if (saving || !content || !normalizedPath || isInvalidPath) return;
    setSaving(true);
    setError(null);
    try {
      await wikiApi.put(org.slug, project.slug, normalizedPath, {
        content,
        message: message || undefined,
      });
      // encodeURI leaves "#" unescaped, which truncates the path at the
      // fragment boundary. Encode each segment with encodeURIComponent and
      // rejoin so "#"/"?" etc. survive the redirect.
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
    <Card style={{ maxWidth: 900 }}>
      <Card.Header>
        <Card.Title>新建 Wiki 页面</Card.Title>
        <Card.Description>使用斜杠分层，例如 <code>docs/usage.md</code>。最多 8 层。</Card.Description>
      </Card.Header>
      <Card.Content>
        <form onSubmit={onSubmit} style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}>
          <TextField name="path" value={path} onChange={setPath} isRequired isInvalid={Boolean(isInvalidPath)}>
            <Label>路径</Label>
            <Input placeholder="Home  或  docs/usage" />
            <Description>不写 <code>.md</code> 会自动补上。</Description>
          </TextField>
          <TextField name="content" value={content} onChange={setContent} isRequired>
            <Label>正文</Label>
            <TextArea
              rows={20}
              placeholder="# 标题"
              style={{ fontFamily: "ui-monospace, monospace", fontSize: "0.85rem" }}
            />
          </TextField>
          <TextField name="message" value={message} onChange={setMessage}>
            <Label>提交信息</Label>
            <Input placeholder={`Create ${normalizedPath || "page"}`} />
          </TextField>
          <ErrorBanner error={error} />
          <div>
            <Button type="submit" isDisabled={saving || !content || !normalizedPath || Boolean(isInvalidPath)}>
              {saving ? "保存中…" : "保存并查看"}
            </Button>
          </div>
        </form>
      </Card.Content>
    </Card>
  );
}
