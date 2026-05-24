import { Button } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import HistoryIcon from "@gravity-ui/icons/Clock";
import FileIcon from "@gravity-ui/icons/File";
import BookOpen from "@gravity-ui/icons/BookOpen";
import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { wiki as wikiApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { DataList, ListRow } from "@/components/page/data-list";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
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
    <PageContainer>
      <PageHeader
        title="Wiki"
        description="Wiki 是一个独立 Git 仓库的 Markdown 集合，可以协同编辑、按提交追溯。"
        actions={
          <div className="flex items-center gap-2">
            <Link to={`${base}/history`}>
              <Button variant="outline">
                <HistoryIcon width={14} height={14} /> 历史
              </Button>
            </Link>
            <Link to={`${base}/new`}>
              <Button>
                <PlusIcon width={14} height={14} /> 新页面
              </Button>
            </Link>
          </div>
        }
      />

      <ErrorBanner error={error} />

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            页面{pages ? ` · ${pages.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {pages === null ? (
            <SkeletonRows count={4} />
          ) : pages.length === 0 ? (
            <EmptyState
              inset
              icon={<BookOpen width={20} height={20} />}
              title="还没有 Wiki 页面"
              description="Wiki 是一个独立 Git 仓库的 Markdown 集合。"
              action={
                <Link to={`${base}/new`}>
                  <Button>
                    <PlusIcon width={14} height={14} /> 创建首页
                  </Button>
                </Link>
              }
            />
          ) : (
            <DataList>
              {pages.map((p) => (
                <ListRow
                  key={p.path}
                  to={`${base}/${encodeURI(p.path)}`}
                  icon={<FileIcon width={14} height={14} />}
                  title={<span className="font-mono">{p.path}</span>}
                  meta={
                    <span>
                      {p.size} 字节 · <RelativeTime iso={p.updated_at} />
                    </span>
                  }
                />
              ))}
            </DataList>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
