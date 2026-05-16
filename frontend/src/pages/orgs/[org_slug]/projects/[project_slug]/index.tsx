import { Card } from "@heroui/react";
import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
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
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <Card>
        <Card.Header>
          <Card.Title>项目概览</Card.Title>
          <Card.Description>
            {project.description || "（暂无简介）"} · 可见性 {project.visibility}
          </Card.Description>
        </Card.Header>
        <Card.Content>
          <div style={{ display: "flex", gap: "1.5rem", color: "var(--muted)", fontSize: "0.85rem" }}>
            <span>
              ID <code>{project.id}</code>
            </span>
            <span>
              创建 <RelativeTime iso={project.created_at} />
            </span>
          </div>
        </Card.Content>
      </Card>

      <Card>
        <Card.Header>
          <Card.Title>仓库</Card.Title>
          <Card.Description>Issues / MR / Wiki / Insights 都按仓库或项目维度组织。</Card.Description>
        </Card.Header>
        <Card.Content>
          <ErrorBanner error={error} />
          {repos === null ? (
            <Loading />
          ) : repos.length === 0 ? (
            <div style={{ color: "var(--muted)" }}>
              这个项目还没有仓库。前往 <Link to={`${basePath}/repos`}>仓库标签</Link> 新建一个。
            </div>
          ) : (
            <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
              {repos.map((r) => (
                <li
                  key={r.id}
                  style={{
                    display: "flex",
                    alignItems: "baseline",
                    justifyContent: "space-between",
                    padding: "0.5rem 0",
                    borderBottom: "1px solid var(--separator)",
                  }}
                >
                  <Link
                    to={`${basePath}/repos/${encodeURIComponent(r.slug)}`}
                    style={{ color: "var(--accent)", fontWeight: 600 }}
                  >
                    {r.slug}
                  </Link>
                  <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                    {r.is_empty ? "空仓库" : `${Math.round(r.size_bytes / 1024)} KB`}
                    {" · "}
                    <RelativeTime iso={r.created_at} />
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}
