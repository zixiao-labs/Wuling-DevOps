import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import CodeIcon from "@gravity-ui/icons/Code";
import CircleQuestionIcon from "@gravity-ui/icons/CircleQuestion";
import BookOpenIcon from "@gravity-ui/icons/BookOpen";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { DataList, ListRow } from "@/components/page/data-list";
import {
  PageContainer,
  PageHeader,
  SectionHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
import { VisibilityBadge, VisibilityIcon, Pill } from "@/components/page/badges";
import { RelativeTime } from "@/components/relative-time";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Repo } from "@/api/types";

export default function ProjectOverview() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [repos, setRepos] = useState<Repo[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    reposApi.list(org.slug, project.slug).then(setRepos).catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug]);

  const basePath = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}`;

  return (
    <PageContainer>
      <PageHeader
        eyebrow={
          <span className="inline-flex items-center gap-1.5">
            <Link to="/orgs" className="hover:text-fg hover:underline">组织</Link>
            <span>·</span>
            <Link
              to={`/orgs/${encodeURIComponent(org.slug)}`}
              className="font-mono hover:text-fg hover:underline"
            >
              @{org.slug}
            </Link>
          </span>
        }
        icon={
          <span
            aria-hidden
            className="grid h-full w-full place-items-center rounded-[inherit] text-fg/70"
            style={{ margin: "-1px" }}
          >
            <VisibilityIcon v={project.visibility} size={20} />
          </span>
        }
        title={
          <span className="inline-flex items-center gap-2">
            <span>{project.display_name || project.slug}</span>
            <VisibilityBadge v={project.visibility} />
          </span>
        }
        description={project.description || "（暂无简介）"}
      />

      <ErrorBanner error={error} />

      {/* Quick links — issue, MR shortcuts */}
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <QuickLink
          to={`${basePath}/repos`}
          icon={<CodeIcon width={16} height={16} />}
          title="仓库"
          hint={repos === null ? "加载中…" : `${repos.length} 个仓库`}
        />
        <QuickLink
          to={`${basePath}/issues`}
          icon={<CircleQuestionIcon width={16} height={16} />}
          title="Issues"
          hint="任务与缺陷跟踪"
        />
        <QuickLink
          to={`${basePath}/wiki`}
          icon={<BookOpenIcon width={16} height={16} />}
          title="Wiki"
          hint="文档与说明"
        />
      </div>

      <SectionHeader
        title="仓库"
        description="Issues / MR / Wiki / Insights 都按仓库或项目维度组织。"
        actions={
          <Link
            to={`${basePath}/repos`}
            className="text-[12px] text-[var(--accent)] hover:underline"
          >
            查看全部 →
          </Link>
        }
      />
      <Surface>
        {repos === null ? (
          <SkeletonRows count={3} />
        ) : repos.length === 0 ? (
          <SurfaceBody>
            <div className="text-[13px] text-muted">
              这个项目还没有仓库。前往
              <Link to={`${basePath}/repos`} className="ml-1 text-[var(--accent)] hover:underline">
                仓库标签
              </Link>
              新建一个。
            </div>
          </SurfaceBody>
        ) : (
          <DataList>
            {repos.slice(0, 8).map((r) => (
              <ListRow
                key={r.id}
                to={`${basePath}/repos/${encodeURIComponent(r.slug)}`}
                icon={
                  <span className="grid h-7 w-7 place-items-center rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] text-fg/70">
                    <CodeIcon width={13} height={13} />
                  </span>
                }
                title={
                  <span className="inline-flex items-center gap-2">
                    <span className="font-mono text-[13px]">{r.slug}</span>
                    {r.is_empty ? <Pill tone="warning">空仓库</Pill> : null}
                  </span>
                }
                subtitle={
                  r.description ? (
                    <span className="text-muted">{r.description}</span>
                  ) : (
                    <span className="text-muted">
                      默认分支 <code className="font-mono text-[11px]">{r.default_branch}</code>
                    </span>
                  )
                }
                meta={
                  <span>
                    {Math.max(1, Math.round(r.size_bytes / 1024))} KB ·{" "}
                    <RelativeTime iso={r.created_at} />
                  </span>
                }
              />
            ))}
          </DataList>
        )}
      </Surface>
    </PageContainer>
  );
}

function QuickLink({
  to,
  icon,
  title,
  hint,
}: {
  to: string;
  icon: React.ReactNode;
  title: string;
  hint: string;
}) {
  return (
    <Link
      to={to}
      className="group flex items-center gap-3 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2.5 transition-colors hover:border-[var(--accent)] hover:bg-[var(--surface-secondary)]"
    >
      <span className="grid h-8 w-8 place-items-center rounded-md bg-[var(--surface-secondary)] text-fg/70 group-hover:bg-[var(--surface)] group-hover:text-[var(--accent)]">
        {icon}
      </span>
      <span className="min-w-0">
        <div className="text-[13px] font-semibold text-fg">{title}</div>
        <div className="truncate text-[11.5px] text-muted">{hint}</div>
      </span>
    </Link>
  );
}
