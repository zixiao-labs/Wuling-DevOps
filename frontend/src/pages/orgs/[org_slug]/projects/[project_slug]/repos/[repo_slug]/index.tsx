import { Button } from "@heroui/react";
import { Link, useParams } from "chen-the-dawnstreak";
import { useEffect, useMemo, useRef, useState } from "react";

import CodeIcon from "@gravity-ui/icons/Code";
import CodePullRequest from "@gravity-ui/icons/CodePullRequest";
import Clock from "@gravity-ui/icons/Clock";
import BranchesRight from "@gravity-ui/icons/BranchesRight";

import { cloneUrls, repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { Markdown } from "@/components/markdown";
import { RelativeTime } from "@/components/relative-time";
import {
  PageContainer,
  PageHeader,
  PageTabs,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { Pill } from "@/components/page/badges";
import { useOrgCtx, useProjectCtx, RepoContext } from "@/auth/org-context";
import type { Commit, Repo } from "@/api/types";

export default function RepoHomePage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const repoSlug = params.repo_slug ?? "";

  const [repo, setRepo] = useState<Repo | null>(null);
  const [commits, setCommits] = useState<Commit[] | null>(null);
  const [readme, setReadme] = useState<string | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    if (!repoSlug) return;
    let cancelled = false;
    setRepo(null);
    setCommits(null);
    setReadme(null);
    setError(null);
    reposApi
      .get(org.slug, project.slug, repoSlug)
      .then(async (r) => {
        if (cancelled) return;
        setRepo(r);
        if (r.is_empty) return;
        const [commitsRes, readmeRes] = await Promise.allSettled([
          reposApi.commits(org.slug, project.slug, repoSlug, { limit: 5 }),
          reposApi.blob(org.slug, project.slug, repoSlug, {
            ref: r.default_branch,
            path: "README.md",
          }),
        ]);
        if (cancelled) return;
        setCommits(commitsRes.status === "fulfilled" ? commitsRes.value : []);
        if (readmeRes.status === "fulfilled" && readmeRes.value.encoding === "utf-8") {
          setReadme(readmeRes.value.content);
        }
      })
      .catch((e) => {
        if (!cancelled) setError(e as ApiError);
      });
    return () => {
      cancelled = true;
    };
  }, [org.slug, project.slug, repoSlug]);

  const urls = useMemo(
    () => cloneUrls(org.slug, project.slug, repoSlug),
    [org.slug, project.slug, repoSlug],
  );

  if (error) {
    return (
      <PageContainer>
        <ErrorBanner error={error} />
      </PageContainer>
    );
  }
  if (!repo) return <Loading />;

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repoSlug)}`;

  return (
    <RepoContext.Provider value={repo}>
      <PageContainer wide>
        <PageHeader
          eyebrow={
            <span className="inline-flex items-center gap-1.5">
              <Link
                to={`/orgs/${encodeURIComponent(org.slug)}`}
                className="hover:text-fg hover:underline"
              >
                @{org.slug}
              </Link>
              <span>/</span>
              <Link
                to={`/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}`}
                className="hover:text-fg hover:underline"
              >
                {project.slug}
              </Link>
              <span>/</span>
              <span className="text-fg">repos</span>
            </span>
          }
          icon={<CodeIcon width={20} height={20} />}
          title={
            <span className="inline-flex items-center gap-2 font-mono text-[18px]">
              <span>{repo.slug}</span>
              {repo.is_empty ? <Pill tone="warning">空仓库</Pill> : null}
            </span>
          }
          description={repo.description || `默认分支 ${repo.default_branch}`}
        />

        <PageTabs
          items={[
            { to: `${base}`, label: "Code", icon: CodeIcon, end: true },
            { to: `${base}/tree`, label: "文件" },
            { to: `${base}/commits`, label: "提交" },
            { to: `${base}/merge-requests`, label: "合并请求", icon: CodePullRequest },
          ]}
        />

        {/* Clone bar */}
        <Surface className="mb-4">
          <SurfaceHeader dense>
            <span className="inline-flex items-center gap-2">
              <BranchesRight width={13} height={13} className="opacity-70" />
              <span className="font-mono text-[12px] text-fg">{repo.default_branch}</span>
              <span className="text-muted">
                · {Math.max(1, Math.round(repo.size_bytes / 1024))} KB ·
              </span>
              <Clock width={11} height={11} className="opacity-60" />
              <RelativeTime iso={repo.created_at} />
            </span>
          </SurfaceHeader>
          <SurfaceBody>
            <div className="grid gap-2 md:grid-cols-2">
              <CloneRow label="HTTPS" url={urls.https} />
              <CloneRow label="SSH" url={urls.ssh} />
            </div>
            <p className="mt-3 text-[11.5px] text-muted">
              首次推送：<code className="font-mono">git push -u origin {repo.default_branch}</code>。
              HTTPS 用 PAT 填密码栏；SSH 需先在「设置 → SSH 公钥」注册公钥。
            </p>
          </SurfaceBody>
        </Surface>

        {repo.is_empty ? (
          <Surface>
            <SurfaceHeader title="空仓库 · 推送你的第一份代码" />
            <SurfaceBody>
              <pre className="m-0 overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] p-3 font-mono text-[12px] text-fg">{`# 已有本地仓库
git remote add origin ${urls.https}
git push -u origin ${repo.default_branch}

# 全新克隆
git clone ${urls.https}`}</pre>
            </SurfaceBody>
          </Surface>
        ) : (
          <div className="grid gap-4 lg:grid-cols-[1fr_360px]">
            <div className="flex flex-col gap-4">
              {readme !== null ? (
                <Surface>
                  <SurfaceHeader>
                    <span className="font-mono text-[12.5px] text-fg">README.md</span>
                  </SurfaceHeader>
                  <SurfaceBody>
                    <Markdown source={readme} />
                  </SurfaceBody>
                </Surface>
              ) : (
                <Surface>
                  <SurfaceBody>
                    <div className="text-[13px] text-muted">仓库根目录尚未发现 README.md。</div>
                  </SurfaceBody>
                </Surface>
              )}
            </div>
            <aside className="flex flex-col gap-4">
              <Surface>
                <SurfaceHeader title="最近提交" />
                <SurfaceBody noPad>
                  {commits === null ? (
                    <Loading />
                  ) : commits.length === 0 ? (
                    <div className="px-4 py-4 text-[12.5px] text-muted">（无提交）</div>
                  ) : (
                    <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
                      {commits.map((c) => (
                        <li key={c.oid} className="px-4 py-2">
                          <div className="flex items-baseline gap-2">
                            <code className="font-mono text-[11px] text-muted">
                              {c.oid.slice(0, 8)}
                            </code>
                            <span className="min-w-0 flex-1 truncate text-[12.5px] text-fg">
                              {firstLine(c.message)}
                            </span>
                          </div>
                          <div className="mt-0.5 text-[11px] text-muted">
                            {c.author.name} · <RelativeTime iso={c.author.when} />
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </SurfaceBody>
              </Surface>
            </aside>
          </div>
        )}
      </PageContainer>
    </RepoContext.Provider>
  );
}

function firstLine(s: string): string {
  const i = s.indexOf("\n");
  return i === -1 ? s : s.slice(0, i);
}

function CloneRow({ label, url }: { label: string; url: string }) {
  const [copied, setCopied] = useState(false);
  const resetTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    return () => {
      if (resetTimer.current !== null) clearTimeout(resetTimer.current);
    };
  }, []);
  return (
    <div className="flex items-center overflow-hidden rounded-md border border-[var(--border)] bg-[var(--field-background)]">
      <span className="shrink-0 border-r border-[var(--border)] bg-[var(--surface-secondary)] px-2.5 py-1 text-[10.5px] font-semibold uppercase tracking-wider text-muted">
        {label}
      </span>
      <code className="min-w-0 flex-1 truncate px-2.5 py-1 font-mono text-[11.5px] text-fg">
        {url}
      </code>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="!h-[26px] !rounded-none border-0 border-l border-[var(--border)]"
        onPress={async () => {
          if (!navigator.clipboard) {
            alert("当前浏览器不支持自动复制；请手动选中地址。");
            return;
          }
          try {
            await navigator.clipboard.writeText(url);
            setCopied(true);
            if (resetTimer.current !== null) clearTimeout(resetTimer.current);
            resetTimer.current = setTimeout(() => setCopied(false), 1500);
          } catch {
            alert("复制失败，请手动选中地址。");
          }
        }}
      >
        {copied ? "已复制" : "复制"}
      </Button>
    </div>
  );
}
