import { Outlet, useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { projects as projectsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { PageContainer } from "@/components/page/primitives";
import { setShellContext } from "@/components/shell/sidebar-store";
import { useOrgCtx, ProjectContext } from "@/auth/org-context";
import type { Project } from "@/api/types";

/**
 * Project _layout — fetches the project record, feeds the AppShell sidebar
 * decoration store with the display name & visibility, and hands the project
 * down to children through the existing ProjectContext.
 *
 * The contextual sidebar (project sub-nav) is rendered by the AppShell —
 * this layout no longer carries its own aside.
 */
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
    let cancelled = false;
    projectsApi
      .get(org.slug, projectSlug)
      .then((p) => {
        if (!cancelled) setProject(p);
      })
      .catch((e) => {
        if (!cancelled) setError(e as ApiError);
      });
    return () => {
      cancelled = true;
    };
  }, [org.slug, projectSlug]);

  // Push display data into the sidebar store as soon as the project is known;
  // clear it on unmount so the next route doesn't inherit stale chrome.
  useEffect(() => {
    if (project) {
      setShellContext({
        projectDisplayName: project.display_name || project.slug,
        projectVisibility: project.visibility,
      });
    } else {
      setShellContext({ projectDisplayName: null, projectVisibility: null });
    }
  }, [project]);

  useEffect(() => {
    return () => {
      setShellContext({ projectDisplayName: null, projectVisibility: null });
    };
  }, []);

  if (error) {
    return (
      <PageContainer>
        <ErrorBanner error={error} />
      </PageContainer>
    );
  }
  if (!project) return <Loading />;

  return (
    <ProjectContext.Provider value={project}>
      <Outlet />
    </ProjectContext.Provider>
  );
}
