import { createContext, useContext } from "react";

import type { Org, Project, Repo } from "@/api/types";

export const OrgContext = createContext<Org | null>(null);
export const ProjectContext = createContext<Project | null>(null);
export const RepoContext = createContext<Repo | null>(null);

export function useOrgCtx(): Org {
  const o = useContext(OrgContext);
  if (!o) throw new Error("OrgContext is missing — wrap in /orgs/[org_slug]/_layout.tsx");
  return o;
}

export function useProjectCtx(): Project {
  const p = useContext(ProjectContext);
  if (!p)
    throw new Error("ProjectContext is missing — wrap in /orgs/[org_slug]/projects/[project_slug]/_layout.tsx");
  return p;
}

export function useRepoCtx(): Repo {
  const r = useContext(RepoContext);
  if (!r) throw new Error("RepoContext is missing");
  return r;
}
