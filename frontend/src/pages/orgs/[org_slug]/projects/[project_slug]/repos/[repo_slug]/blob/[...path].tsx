import { Link, useParams, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { Markdown } from "@/components/markdown";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { BlobResponse } from "@/api/types";

export default function BlobPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const [search] = useSearchParams();

  const repoSlug = params.repo_slug ?? "";
  const path = (params["*"] ?? "") as string;
  const ref = search.get("ref") ?? "";

  const [blob, setBlob] = useState<BlobResponse | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setBlob(null);
    setError(null);
    if (!path) return;
    reposApi
      .blob(org.slug, project.slug, repoSlug, { ref: ref || undefined, path })
      .then(setBlob)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, repoSlug, ref, path]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repoSlug)}`;
  const parentPath = path.includes("/") ? path.replace(/\/[^/]+$/, "") : "";
  const refSuffix = ref ? `?ref=${encodeURIComponent(ref)}` : "";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <nav style={{ display: "flex", gap: "0.5rem", alignItems: "center" }}>
        <Link to={`${base}/tree${refSuffix}`} style={{ color: "var(--accent)" }}>
          根
        </Link>
        <span style={{ color: "var(--muted)" }}>/</span>
        {parentPath ? (
          <>
            <Link to={`${base}/tree/${parentPath}${refSuffix}`} style={{ color: "var(--accent)" }}>
              {parentPath}
            </Link>
            <span style={{ color: "var(--muted)" }}>/</span>
          </>
        ) : null}
        <strong>{path.split("/").pop()}</strong>
      </nav>

      <ErrorBanner error={error} />

      {!blob ? (
        <Loading />
      ) : (
        <BlobContents path={path} blob={blob} />
      )}
    </div>
  );
}

function BlobContents({ path, blob }: { path: string; blob: BlobResponse }) {
  if (blob.is_binary || blob.encoding === "base64") {
    return (
      <div
        style={{
          padding: "1rem",
          background: "var(--surface-secondary)",
          borderRadius: "var(--radius)",
          color: "var(--muted)",
        }}
      >
        二进制文件，{blob.size} 字节。暂不支持在线预览。
      </div>
    );
  }
  const isMd = /\.md$/i.test(path);
  if (isMd) {
    return <Markdown source={blob.content} />;
  }
  return (
    <pre
      style={{
        background: "var(--surface-secondary)",
        padding: "0.75rem",
        borderRadius: "var(--radius)",
        overflow: "auto",
        fontSize: "0.85rem",
        margin: 0,
        whiteSpace: "pre",
      }}
    >
      {blob.content}
    </pre>
  );
}
