import { Card } from "@heroui/react";
import { Link, useParams } from "chen-the-dawnstreak";
import { useEffect, useMemo, useState } from "react";

import { cloneUrls, repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { Markdown } from "@/components/markdown";
import { RelativeTime } from "@/components/relative-time";
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
    setRepo(null);
    setCommits(null);
    setReadme(null);
    setError(null);
    reposApi
      .get(org.slug, project.slug, repoSlug)
      .then(async (r) => {
        setRepo(r);
        if (r.is_empty) return;
        // Fire commits + README in parallel; non-fatal if either fails
        const [commitsRes, readmeRes] = await Promise.allSettled([
          reposApi.commits(org.slug, project.slug, repoSlug, { limit: 5 }),
          reposApi.blob(org.slug, project.slug, repoSlug, {
            ref: r.default_branch,
            path: "README.md",
          }),
        ]);
        if (commitsRes.status === "fulfilled") setCommits(commitsRes.value);
        if (readmeRes.status === "fulfilled" && readmeRes.value.encoding === "utf-8") {
          setReadme(readmeRes.value.content);
        }
      })
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, repoSlug]);

  const urls = useMemo(() => cloneUrls(org.slug, project.slug, repoSlug), [
    org.slug,
    project.slug,
    repoSlug,
  ]);

  if (error) return <ErrorBanner error={error} />;
  if (!repo) return <Loading />;

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repoSlug)}`;

  return (
    <RepoContext.Provider value={repo}>
      <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
        <Card>
          <Card.Header>
            <Card.Title>{repo.slug}</Card.Title>
            <Card.Description>
              {repo.description || "（暂无简介）"} · 默认分支 <code>{repo.default_branch}</code>
            </Card.Description>
          </Card.Header>
          <Card.Content>
            <div style={{ display: "flex", gap: "1rem", flexWrap: "wrap" }}>
              <CloneRow label="HTTPS" url={urls.https} />
              <CloneRow label="SSH" url={urls.ssh} />
            </div>
            <p style={{ marginTop: "0.5rem", fontSize: "0.8rem", color: "var(--muted)" }}>
              首次推送：<code>git push -u origin {repo.default_branch}</code>。HTTPS 用 PAT
              填密码栏；SSH 需先在「设置 → SSH 公钥」注册公钥。
            </p>
            <nav style={{ display: "flex", gap: "0.75rem", marginTop: "1rem" }}>
              <Link to={`${base}/tree`} style={navStyle}>浏览代码</Link>
              <Link to={`${base}/commits`} style={navStyle}>提交</Link>
              <Link to={`${base}/merge-requests`} style={navStyle}>合并请求</Link>
            </nav>
          </Card.Content>
        </Card>

        {repo.is_empty ? (
          <Card>
            <Card.Content>
              <h3>空仓库 · 推送你的第一份代码</h3>
              <pre
                style={{
                  background: "var(--surface-secondary)",
                  padding: "0.75rem",
                  borderRadius: "var(--radius)",
                  overflowX: "auto",
                  fontSize: "0.85rem",
                }}
              >{`# 已有本地仓库
git remote add origin ${urls.https}
git push -u origin ${repo.default_branch}

# 全新克隆
git clone ${urls.https}`}</pre>
            </Card.Content>
          </Card>
        ) : (
          <>
            {readme !== null ? (
              <Card>
                <Card.Header>
                  <Card.Title>README.md</Card.Title>
                </Card.Header>
                <Card.Content>
                  <Markdown source={readme} />
                </Card.Content>
              </Card>
            ) : null}
            <Card>
              <Card.Header>
                <Card.Title>最近提交</Card.Title>
              </Card.Header>
              <Card.Content>
                {commits === null ? (
                  <Loading />
                ) : commits.length === 0 ? (
                  <div style={{ color: "var(--muted)" }}>（无提交）</div>
                ) : (
                  <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
                    {commits.map((c) => (
                      <li
                        key={c.oid}
                        style={{
                          display: "flex",
                          alignItems: "baseline",
                          gap: "0.5rem",
                          padding: "0.4rem 0",
                          borderBottom: "1px solid var(--separator)",
                          fontSize: "0.9rem",
                        }}
                      >
                        <code style={{ color: "var(--muted)" }}>{c.oid.slice(0, 8)}</code>
                        <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis" }}>
                          {firstLine(c.message)}
                        </span>
                        <span style={{ color: "var(--muted)", fontSize: "0.75rem" }}>
                          {c.author.name} · <RelativeTime iso={c.author.when} />
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </Card.Content>
            </Card>
          </>
        )}
      </div>
    </RepoContext.Provider>
  );
}

function firstLine(s: string): string {
  const i = s.indexOf("\n");
  return i === -1 ? s : s.slice(0, i);
}

const navStyle = {
  color: "var(--accent)",
  textDecoration: "none",
  fontWeight: 500,
} as const;

function CloneRow({ label, url }: { label: string; url: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div style={{ display: "flex", alignItems: "center", gap: "0.5rem", minWidth: 280 }}>
      <span
        style={{
          fontSize: "0.7rem",
          color: "var(--muted)",
          padding: "0.1rem 0.4rem",
          background: "var(--surface-secondary)",
          borderRadius: "0.25rem",
        }}
      >
        {label}
      </span>
      <code
        style={{
          fontSize: "0.8rem",
          flex: 1,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {url}
      </code>
      <button
        type="button"
        onClick={async () => {
          if (!navigator.clipboard) {
            alert("当前浏览器不支持自动复制；请手动选中地址。");
            return;
          }
          try {
            await navigator.clipboard.writeText(url);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          } catch {
            alert("复制失败，请手动选中地址。");
          }
        }}
        style={{
          border: "1px solid var(--border)",
          background: "var(--surface)",
          color: "var(--foreground)",
          padding: "0.15rem 0.5rem",
          borderRadius: "var(--field-radius)",
          fontSize: "0.75rem",
          cursor: "pointer",
        }}
      >
        {copied ? "已复制" : "复制"}
      </button>
    </div>
  );
}
