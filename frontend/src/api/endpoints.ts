/**
 * Endpoint functions — one per OpenAPI operation.
 *
 * Grouped by tag for navigability. Parameter and return types come from
 * src/api/types.ts which mirrors api/openapi.yaml.
 */

import { apiDelete, apiGet, apiPatch, apiPost, apiPut, type QueryMap } from "./client";
import type {
  AccessTokenView,
  ActivityDay,
  BlobResponse,
  Commit,
  ContributorStat,
  CreateIssueRequest,
  CreateLabelRequest,
  CreateMRReviewRequest,
  CreateMergeRequestRequest,
  CreateOrgRequest,
  CreatePatRequest,
  CreateProjectRequest,
  CreateRepoRequest,
  CreateSSHKeyRequest,
  GitRef,
  Issue,
  IssueComment,
  IssueListQuery,
  Label,
  LanguageStats,
  LoginRequest,
  MRComment,
  MRDiffResponse,
  MRListQuery,
  MRReview,
  MergeMRRequest,
  MergeRequest,
  Org,
  PatchIssueRequest,
  PatchMergeRequestRequest,
  Project,
  PutWikiPageRequest,
  RegisterRequest,
  Repo,
  SSHKey,
  TokenResponse,
  TreeResponse,
  User,
  WikiHistoryCommit,
  WikiPage,
  WikiPageContent,
} from "./types";

const enc = encodeURIComponent;

/**
 * Wiki pages live at a single path segment with the interior "/" percent-encoded
 * (e.g. "docs/usage.md" -> "docs%2Fusage.md"). Don't run encodeURIComponent on
 * the whole string twice — that double-encodes the "%" character.
 */
export function wikiPathSegment(path: string): string {
  return path.split("/").map(enc).join("%2F");
}

// ---------------- Health ----------------

export const health = {
  ping: () => apiGet<unknown>("/healthz"),
  version: () => apiGet<unknown>("/version"),
};

// ---------------- Auth ----------------

export const auth = {
  register: (body: RegisterRequest) =>
    apiPost<TokenResponse>("/api/v1/auth/register", body),
  login: (body: LoginRequest) =>
    apiPost<TokenResponse>("/api/v1/auth/login", body),
  me: (signal?: AbortSignal) =>
    apiGet<User>("/api/v1/auth/me", undefined, signal),
};

// ---------------- PATs ----------------

export const tokens = {
  list: () =>
    apiGet<{ tokens: AccessTokenView[] }>("/api/v1/auth/tokens").then((r) => r.tokens),
  create: (body: CreatePatRequest) =>
    apiPost<AccessTokenView>("/api/v1/auth/tokens", body),
  revoke: (id: string) =>
    apiDelete(`/api/v1/auth/tokens/${enc(id)}`),
};

// ---------------- SSH keys ----------------

export const sshKeys = {
  list: () =>
    apiGet<{ ssh_keys: SSHKey[] }>("/api/v1/auth/ssh-keys").then((r) => r.ssh_keys),
  create: (body: CreateSSHKeyRequest) =>
    apiPost<SSHKey>("/api/v1/auth/ssh-keys", body),
  revoke: (id: string) =>
    apiDelete(`/api/v1/auth/ssh-keys/${enc(id)}`),
};

// ---------------- Orgs ----------------

export const orgs = {
  list: () =>
    apiGet<{ orgs: Org[] }>("/api/v1/orgs").then((r) => r.orgs),
  create: (body: CreateOrgRequest) =>
    apiPost<Org>("/api/v1/orgs", body),
  get: (slug: string) =>
    apiGet<Org>(`/api/v1/orgs/${enc(slug)}`),
};

// ---------------- Projects ----------------

export const projects = {
  list: (orgSlug: string) =>
    apiGet<{ projects: Project[] }>(`/api/v1/orgs/${enc(orgSlug)}/projects`).then(
      (r) => r.projects,
    ),
  create: (orgSlug: string, body: CreateProjectRequest) =>
    apiPost<Project>(`/api/v1/orgs/${enc(orgSlug)}/projects`, body),
  get: (orgSlug: string, projectSlug: string) =>
    apiGet<Project>(`/api/v1/orgs/${enc(orgSlug)}/projects/${enc(projectSlug)}`),
};

// ---------------- Repos ----------------

const repoBase = (org: string, project: string, repo: string) =>
  `/api/v1/orgs/${enc(org)}/projects/${enc(project)}/repos/${enc(repo)}`;

export const repos = {
  list: (org: string, project: string) =>
    apiGet<{ repos: Repo[] }>(`/api/v1/orgs/${enc(org)}/projects/${enc(project)}/repos`).then(
      (r) => r.repos,
    ),
  create: (org: string, project: string, body: CreateRepoRequest) =>
    apiPost<Repo>(`/api/v1/orgs/${enc(org)}/projects/${enc(project)}/repos`, body),
  get: (org: string, project: string, repo: string) =>
    apiGet<Repo>(repoBase(org, project, repo)),

  refs: (org: string, project: string, repo: string) =>
    apiGet<{ refs: GitRef[] }>(`${repoBase(org, project, repo)}/refs`).then((r) => r.refs),

  commits: (
    org: string,
    project: string,
    repo: string,
    query: { ref?: string; limit?: number } = {},
  ) =>
    apiGet<{ commits: Commit[] }>(`${repoBase(org, project, repo)}/commits`, query).then(
      (r) => r.commits,
    ),

  tree: (
    org: string,
    project: string,
    repo: string,
    query: { ref?: string; path?: string } = {},
  ) => apiGet<TreeResponse>(`${repoBase(org, project, repo)}/tree`, query),

  blob: (
    org: string,
    project: string,
    repo: string,
    query: { oid?: string; ref?: string; path?: string },
  ) => apiGet<BlobResponse>(`${repoBase(org, project, repo)}/blob`, query),
};

// ---------------- Issues & Labels ----------------

const projectBase = (org: string, project: string) =>
  `/api/v1/orgs/${enc(org)}/projects/${enc(project)}`;

export const issues = {
  list: (org: string, project: string, query: IssueListQuery = {}) =>
    apiGet<{ issues: Issue[] }>(`${projectBase(org, project)}/issues`, query as QueryMap).then(
      (r) => r.issues,
    ),
  create: (org: string, project: string, body: CreateIssueRequest) =>
    apiPost<Issue>(`${projectBase(org, project)}/issues`, body),
  get: (org: string, project: string, number: number) =>
    apiGet<Issue>(`${projectBase(org, project)}/issues/${number}`),
  patch: (org: string, project: string, number: number, body: PatchIssueRequest) =>
    apiPatch<Issue>(`${projectBase(org, project)}/issues/${number}`, body),
  delete: (org: string, project: string, number: number) =>
    apiDelete(`${projectBase(org, project)}/issues/${number}`),

  comments: {
    list: (org: string, project: string, number: number) =>
      apiGet<{ comments: IssueComment[] }>(
        `${projectBase(org, project)}/issues/${number}/comments`,
      ).then((r) => r.comments),
    create: (org: string, project: string, number: number, body: { body: string }) =>
      apiPost<IssueComment>(`${projectBase(org, project)}/issues/${number}/comments`, body),
  },
};

export const labels = {
  list: (org: string, project: string) =>
    apiGet<{ labels: Label[] }>(`${projectBase(org, project)}/labels`).then((r) => r.labels),
  create: (org: string, project: string, body: CreateLabelRequest) =>
    apiPost<Label>(`${projectBase(org, project)}/labels`, body),
  delete: (org: string, project: string, id: string) =>
    apiDelete(`${projectBase(org, project)}/labels/${enc(id)}`),
};

// ---------------- Merge Requests ----------------

const mrBase = (org: string, project: string, repo: string) =>
  `${repoBase(org, project, repo)}/merge-requests`;

export const mergeRequests = {
  list: (org: string, project: string, repo: string, query: MRListQuery = {}) =>
    apiGet<{ merge_requests: MergeRequest[] }>(mrBase(org, project, repo), query as QueryMap).then(
      (r) => r.merge_requests,
    ),
  create: (org: string, project: string, repo: string, body: CreateMergeRequestRequest) =>
    apiPost<MergeRequest>(mrBase(org, project, repo), body),

  get: (org: string, project: string, repo: string, n: number) =>
    apiGet<MergeRequest>(`${mrBase(org, project, repo)}/${n}`),
  patch: (
    org: string,
    project: string,
    repo: string,
    n: number,
    body: PatchMergeRequestRequest,
  ) => apiPatch<MergeRequest>(`${mrBase(org, project, repo)}/${n}`, body),

  diff: (
    org: string,
    project: string,
    repo: string,
    n: number,
    opts: { includePatch?: boolean } = {},
  ) =>
    apiGet<MRDiffResponse>(`${mrBase(org, project, repo)}/${n}/diff`, {
      include: opts.includePatch ? "patch" : undefined,
    }),

  commits: (org: string, project: string, repo: string, n: number, limit?: number) =>
    apiGet<{ commits: Commit[] }>(`${mrBase(org, project, repo)}/${n}/commits`, {
      limit,
    }).then((r) => r.commits),

  merge: (org: string, project: string, repo: string, n: number, body: MergeMRRequest) =>
    apiPost<MergeRequest>(`${mrBase(org, project, repo)}/${n}/merge`, body),
  close: (org: string, project: string, repo: string, n: number) =>
    apiPost<MergeRequest>(`${mrBase(org, project, repo)}/${n}/close`),
  reopen: (org: string, project: string, repo: string, n: number) =>
    apiPost<MergeRequest>(`${mrBase(org, project, repo)}/${n}/reopen`),

  comments: {
    list: (org: string, project: string, repo: string, n: number) =>
      apiGet<{ comments: MRComment[] }>(
        `${mrBase(org, project, repo)}/${n}/comments`,
      ).then((r) => r.comments),
    create: (org: string, project: string, repo: string, n: number, body: { body: string }) =>
      apiPost<MRComment>(`${mrBase(org, project, repo)}/${n}/comments`, body),
  },
  reviews: {
    list: (org: string, project: string, repo: string, n: number) =>
      apiGet<{ reviews: MRReview[] }>(
        `${mrBase(org, project, repo)}/${n}/reviews`,
      ).then((r) => r.reviews),
    create: (
      org: string,
      project: string,
      repo: string,
      n: number,
      body: CreateMRReviewRequest,
    ) => apiPost<MRReview>(`${mrBase(org, project, repo)}/${n}/reviews`, body),
  },
};

// ---------------- Wiki ----------------

export const wiki = {
  pages: (org: string, project: string) =>
    apiGet<{ pages: WikiPage[] }>(`${projectBase(org, project)}/wiki/pages`).then(
      (r) => r.pages,
    ),
  get: (org: string, project: string, path: string) =>
    apiGet<WikiPageContent>(
      `${projectBase(org, project)}/wiki/pages/${wikiPathSegment(path)}`,
    ),
  put: (org: string, project: string, path: string, body: PutWikiPageRequest) =>
    apiPut<WikiPageContent>(
      `${projectBase(org, project)}/wiki/pages/${wikiPathSegment(path)}`,
      body,
    ),
  delete: (org: string, project: string, path: string) =>
    apiDelete(`${projectBase(org, project)}/wiki/pages/${wikiPathSegment(path)}`),
  history: (org: string, project: string, limit?: number) =>
    apiGet<{ commits: WikiHistoryCommit[] }>(
      `${projectBase(org, project)}/wiki/history`,
      { limit },
    ).then((r) => r.commits),
};

// ---------------- Insights ----------------

export const insights = {
  activity: (org: string, project: string, since?: string) =>
    apiGet<{ days: ActivityDay[] }>(`${projectBase(org, project)}/insights/activity`, {
      since,
    }).then((r) => r.days),
  contributors: (
    org: string,
    project: string,
    query: { repo: string; since?: string; limit?: number },
  ) =>
    apiGet<{ contributors: ContributorStat[] }>(
      `${projectBase(org, project)}/insights/contributors`,
      query,
    ).then((r) => r.contributors),
  languages: (org: string, project: string, query: { repo: string; ref?: string }) =>
    apiGet<LanguageStats>(`${projectBase(org, project)}/insights/languages`, query),
};

/**
 * Git clone URLs derived from server config. The HTTPS URL is rooted at the
 * current `window.location.origin`; the SSH URL needs a configured host
 * (defaults to the same host on port 2222 — see env in deploy/production).
 */
export function cloneUrls(
  org: string,
  project: string,
  repo: string,
): { https: string; ssh: string } {
  const origin =
    typeof window !== "undefined" ? window.location.origin : "http://localhost:8080";
  const host = typeof window !== "undefined" ? window.location.hostname : "localhost";
  return {
    https: `${origin}/${org}/${project}/${repo}.git`,
    ssh: `ssh://git@${host}:2222/${org}/${project}/${repo}.git`,
  };
}
