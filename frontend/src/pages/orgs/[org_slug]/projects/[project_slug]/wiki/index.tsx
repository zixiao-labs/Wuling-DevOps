import { Button } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import HistoryIcon from "@gravity-ui/icons/Clock";
import FileIcon from "@gravity-ui/icons/File";
import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { wiki as wikiApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { WikiPage } from "@/api/types";

export default function WikiIndex() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [pages, setPages] = useState<WikiPage[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    wikiApi.pages(org.slug, project.slug).then(setPages).catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/wiki`;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <header style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <h1 style={{ margin: 0, fontSize: "1.5rem" }}>Wiki</h1>
        <div style={{ display: "flex", gap: "0.5rem" }}>
          <Link to={`${base}/history`}>
            <Button variant="secondary">
              <HistoryIcon width={16} height={16} /> 历史
            </Button>
          </Link>
          <Link to={`${base}/new`}>
            <Button>
              <PlusIcon width={16} height={16} /> 新页面
            </Button>
          </Link>
        </div>
      </header>

      <ErrorBanner error={error} />

      {pages === null ? (
        <Loading />
      ) : pages.length === 0 ? (
        <EmptyState
          title="还没有 Wiki 页面"
          description="Wiki 是一个独立 Git 仓库的 Markdown 集合。"
          action={
            <Link to={`${base}/new`}>
              <Button>
                <PlusIcon width={16} height={16} /> 创建首页
              </Button>
            </Link>
          }
        />
      ) : (
        <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
          {pages.map((p) => (
            <li
              key={p.path}
              style={{
                padding: "0.6rem 0.75rem",
                background: "var(--surface)",
                borderBottom: "1px solid var(--separator)",
                display: "flex",
                alignItems: "center",
                gap: "0.5rem",
              }}
            >
              <FileIcon width={16} height={16} />
              <Link
                to={`${base}/${encodeURI(p.path)}`}
                style={{ color: "var(--accent)", textDecoration: "none", flex: 1 }}
              >
                {p.path}
              </Link>
              <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                {p.size} 字节 · <RelativeTime iso={p.updated_at} />
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
