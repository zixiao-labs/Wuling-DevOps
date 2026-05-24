import { Link, useParams, useSearchParams } from "chen-the-dawnstreak";
import ArrowChevronRight from "@gravity-ui/icons/ArrowChevronRight";
import { useEffect, useState } from "react";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { Markdown } from "@/components/markdown";
import {
  PageContainer,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
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
  const filename = path.split("/").pop() || "";

  return (
    <PageContainer wide>
      <nav className="mb-3 inline-flex flex-wrap items-center gap-0.5 text-[12px]">
        <Link to={`${base}/tree${refSuffix}`} className="font-mono text-[var(--accent)] hover:underline">
          {repoSlug}
        </Link>
        <ArrowChevronRight width={11} height={11} className="opacity-50" />
        <Link to={`${base}/tree${refSuffix}`} className="text-[var(--accent)] hover:underline">
          /
        </Link>
        {parentPath ? (
          <>
            <ArrowChevronRight width={11} height={11} className="opacity-50" />
            <Link
              to={`${base}/tree/${encodeURI(parentPath)}${refSuffix}`}
              className="font-mono text-[var(--accent)] hover:underline"
            >
              {parentPath}
            </Link>
          </>
        ) : null}
        <ArrowChevronRight width={11} height={11} className="opacity-50" />
        <span className="font-mono font-medium text-fg">{filename}</span>
      </nav>

      <ErrorBanner error={error} />

      {!blob ? (
        <Loading />
      ) : (
        <Surface>
          <SurfaceHeader dense>
            <span className="inline-flex items-center gap-3 text-[11.5px] text-muted">
              <span className="font-mono text-fg">{filename}</span>
              <span>{blob.size.toLocaleString()} 字节</span>
              <span className="rounded-sm border border-[var(--border)] bg-[var(--surface-secondary)] px-1.5 text-[10.5px] uppercase tracking-wider">
                {blob.is_binary ? "binary" : blob.encoding}
              </span>
            </span>
          </SurfaceHeader>
          <SurfaceBody noPad={!isMarkdown(path)}>
            <BlobContents path={path} blob={blob} />
          </SurfaceBody>
        </Surface>
      )}
    </PageContainer>
  );
}

function isMarkdown(p: string): boolean {
  return /\.md$/i.test(p);
}

function BlobContents({ path, blob }: { path: string; blob: BlobResponse }) {
  if (blob.is_binary || blob.encoding === "base64") {
    return (
      <div className="px-4 py-4 text-[12.5px] text-muted">
        二进制文件，{blob.size} 字节。暂不支持在线预览。
      </div>
    );
  }
  if (isMarkdown(path)) {
    return <Markdown source={blob.content} />;
  }
  const lines = blob.content.split("\n");
  return (
    <div className="overflow-x-auto font-mono text-[12px] leading-[1.6]">
      <table className="m-0 border-collapse">
        <tbody>
          {lines.map((line, i) => (
            <tr key={i} className="hover:bg-[var(--surface-secondary)]/40">
              <td className="select-none px-3 text-right text-[11px] tabular-nums text-muted/60">
                {i + 1}
              </td>
              <td className="whitespace-pre pr-4">{line || " "}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
