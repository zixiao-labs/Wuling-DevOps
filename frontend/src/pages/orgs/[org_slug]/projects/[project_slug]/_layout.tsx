import { NavLink, Outlet, useParams } from "chen-the-dawnstreak";
import CodeIcon from "@gravity-ui/icons/Code";
import CircleQuestionIcon from "@gravity-ui/icons/CircleQuestion";
import CodePullRequestIcon from "@gravity-ui/icons/CodePullRequest";
import BookOpenIcon from "@gravity-ui/icons/BookOpen";
import ChartLineIcon from "@gravity-ui/icons/ChartLine";
import TagIcon from "@gravity-ui/icons/Tag";
import { useEffect, useState } from "react";

import { projects as projectsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { useOrgCtx, ProjectContext } from "@/auth/org-context";
import type { Project } from "@/api/types";

const navItems = [
  { suffix: "", label: "概览", icon: BookOpenIcon, end: true },
  { suffix: "/repos", label: "仓库", icon: CodeIcon },
  { suffix: "/issues", label: "Issues", icon: CircleQuestionIcon },
  { suffix: "/labels", label: "标签", icon: TagIcon },
  { suffix: "/wiki", label: "Wiki", icon: BookOpenIcon },
  { suffix: "/insights", label: "Insights", icon: ChartLineIcon },
];

export default function ProjectLayout() {
  const org = useOrgCtx();
  const params = useParams();
  const projectSlug = params.project_slug ?? "";
  const [project, setProject] = useState<Project | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setProject(null);
    setError(null);
    if (!projectSlug) return;
    projectsApi.get(org.slug, projectSlug).then(setProject).catch((e) => setError(e as ApiError));
  }, [org.slug, projectSlug]);

  if (error) return <ErrorBanner error={error} />;
  if (!project) return <Loading />;

  const basePath = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}`;

  return (
    <ProjectContext.Provider value={project}>
      <div style={{ display: "grid", gridTemplateColumns: "200px 1fr", gap: "1.5rem" }}>
        <aside>
          <div style={{ marginBottom: "1rem" }}>
            <div style={{ fontSize: "0.7rem", color: "var(--muted)" }}>@{org.slug}</div>
            <h2 style={{ margin: 0, fontSize: "1.1rem", wordBreak: "break-word" }}>
              {project.display_name || project.slug}
            </h2>
            {project.description ? (
              <p style={{ color: "var(--muted)", fontSize: "0.8rem", margin: "0.25rem 0 0" }}>
                {project.description}
              </p>
            ) : null}
          </div>

          {/* Custom nav using NavLink — exact "end" semantics for /repos vs /repos/...  */}
          <nav style={{ display: "flex", flexDirection: "column", gap: "0.15rem" }}>
            {navItems.map((it) => {
              const Icon = it.icon;
              return (
                <NavLink
                  key={it.suffix}
                  to={`${basePath}${it.suffix}`}
                  end={it.end ?? false}
                  style={({ isActive }) => ({
                    padding: "0.4rem 0.6rem",
                    borderRadius: "var(--radius)",
                    textDecoration: "none",
                    color: isActive ? "var(--accent-foreground)" : "var(--foreground)",
                    background: isActive ? "var(--accent)" : "transparent",
                    fontSize: "0.9rem",
                    display: "inline-flex",
                    alignItems: "center",
                    gap: "0.5rem",
                  })}
                >
                  <Icon width={16} height={16} />
                  {it.label}
                </NavLink>
              );
            })}
            <NavLink
              to={`${basePath}/repos`}
              end
              style={{ display: "none" }}
            >
              {/* The merge-requests sub-route currently lives under a repo, so we surface
                  a deep link here instead of a top-level nav item. */}
              <CodePullRequestIcon />
            </NavLink>
          </nav>
        </aside>

        <section>
          <Outlet />
        </section>
      </div>
    </ProjectContext.Provider>
  );
}
