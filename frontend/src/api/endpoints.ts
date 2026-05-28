/**
 * Endpoint functions — one per OpenAPI operation.
 *
 * Grouped by tag for navigability. Parameter and return types come from
 * src/api/types.ts which mirrors api/openapi.yaml.
 */

import { apiDelete, apiGet, apiPatch, apiPost, apiPut, apiFetch, type QueryMap } from "./client";
import type {
  AccessTokenView,
  ActivityDay,
  AuthorizationView,
  AuthorizePreview,
  AvatarUploadResponse,
  BlobResponse,
  Commit,
  ContributorStat,
  CreateInvitationRequest,
  CreateInvitationResponse,
  CreateIssueRequest,
  CreateLabelRequest,
  CreateMRReviewRequest,
  CreateMergeRequestRequest,
  CreateOAuthAppRequest,
  CreateOAuthAppResponse,
  CreateOrgRequest,
  CreatePatRequest,
  CreateProjectRequest,
  CreateRepoRequest,
  CreateSSHKeyRequest,
  GitRef,
  InvitationStatus,
  Issue,
  IssueComment,
  IssueListQuery,
  Label,
  LanguageStats,
  ListMembersResponse,
  LoginRequest,
  MRComment,
  MRDiffResponse,
  MRListQuery,
  MRReview,
  MergeMRRequest,
  MergeRequest,
  OAuthAppView,
  OAuthClientPublic,
  OAuthConfirmAction,
  OAuthConfirmResponse,
  Org,
  OrgInvitation,
  PatchIssueRequest,
  PatchMemberRequest,
  PatchMergeRequestRequest,
  PatchUserRequest,
  PendingAccountResponse,
  Project,
  PutWikiPageRequest,
  RegisterRequest,
  Repo,
  SSHKey,
  TokenResponse,
  TreeResponse,
  UpdateOAuthAppRequest,
  User,
  UserApprovalStatus,
  WellKnownDoc,
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

/**
 * Account registration returns one of two shapes depending on the server's
 * approval policy: a `TokenResponse` (immediate session) when approval isn't
 * required, or a `PendingAccountResponse` (202) when an admin still has to
 * approve. Callers branch on the `access_token` field rather than HTTP status
 * since the fetch client doesn't expose status codes directly.
 */
export type RegisterResponse = TokenResponse | PendingAccountResponse;

export function isPendingResponse(r: RegisterResponse): r is PendingAccountResponse {
  return (r as PendingAccountResponse).status !== undefined && !("access_token" in r);
}

export const auth = {
  register: (body: RegisterRequest) =>
    apiPost<RegisterResponse>("/api/v1/auth/register", body),
  login: (body: LoginRequest) =>
    apiPost<TokenResponse>("/api/v1/auth/login", body),
  me: (signal?: AbortSignal) =>
    apiGet<User>("/api/v1/auth/me", undefined, signal),
};

// ---------------- Avatar ----------------

export const avatars = {
  /** Build the public avatar URL for a username. Optional `v` is a cache-buster. */
  url: (username: string, v?: string | number): string => {
    const q = v ? `?v=${enc(String(v))}` : "";
    return `/api/v1/users/${enc(username)}/avatar${q}`;
  },
  upload: (file: File) => {
    const fd = new FormData();
    fd.append("avatar", file);
    return apiFetch<AvatarUploadResponse>("/api/v1/auth/avatar", {
      method: "PUT",
      body: fd,
    });
  },
  remove: () => apiDelete("/api/v1/auth/avatar"),
};

/**
 * Computes the absolute URL used by the "Sign in with GitHub" button. We
 * navigate the *top-level* document there (window.location.assign) rather
 * than calling fetch, since OAuth is a multi-step browser redirect dance.
 */
export const githubOAuth = {
  startURL: (returnTo?: string): string => {
    const q = returnTo ? `?return_to=${enc(returnTo)}` : "";
    return `/api/v1/auth/oauth/github/start${q}`;
  },
  confirm: (action: OAuthConfirmAction) =>
    apiFetch<OAuthConfirmResponse>("/api/v1/auth/oauth/github/confirm", {
      method: "POST",
      body: { action },
      anonymous: true,
    }),
};

// ---------------- Admin ----------------

export const admin = {
  users: {
    list: (status?: UserApprovalStatus) =>
      apiGet<{ users: User[] }>("/api/v1/admin/users", status ? { status } : undefined).then(
        (r) => r.users,
      ),
    patch: (userID: string, body: PatchUserRequest) =>
      apiPatch<User>(`/api/v1/admin/users/${enc(userID)}`, body),
  },
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

// ---------------- Org members ----------------

export const orgMembers = {
  list: (orgSlug: string) =>
    apiGet<ListMembersResponse>(`/api/v1/orgs/${enc(orgSlug)}/members`),
  setRole: (orgSlug: string, userID: string, body: PatchMemberRequest) =>
    apiPatch<void>(`/api/v1/orgs/${enc(orgSlug)}/members/${enc(userID)}`, body),
  remove: (orgSlug: string, userID: string) =>
    apiDelete(`/api/v1/orgs/${enc(orgSlug)}/members/${enc(userID)}`),
};

// ---------------- Org invitations ----------------

export const orgInvitations = {
  list: (orgSlug: string, status?: InvitationStatus) =>
    apiGet<{ invitations: OrgInvitation[] }>(
      `/api/v1/orgs/${enc(orgSlug)}/invitations`,
      status ? { status } : undefined,
    ).then((r) => r.invitations),
  create: (orgSlug: string, body: CreateInvitationRequest) =>
    apiPost<CreateInvitationResponse>(
      `/api/v1/orgs/${enc(orgSlug)}/invitations`,
      body,
    ),
  revoke: (orgSlug: string, invitationID: string) =>
    apiDelete(`/api/v1/orgs/${enc(orgSlug)}/invitations/${enc(invitationID)}`),

  /** Recipient-facing preview by raw token. */
  preview: (token: string) =>
    apiGet<OrgInvitation>(`/api/v1/invitations/${enc(token)}`),
  accept: (token: string) =>
    apiPost<OrgInvitation>(`/api/v1/invitations/${enc(token)}/accept`),
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

// ---------------- OAuth Provider ----------------

/**
 * Endpoints for the OAuth provider role — both the consent flow that other
 * apps drag the user into, and the user's own "Authorized Apps" / "OAuth
 * Apps" settings pages.
 *
 * `authorizeStartURL` is intentionally a URL builder, not a fetch helper:
 * the consent flow is a top-level navigation, and the SPA hands the user
 * agent to the backend via window.location.assign.
 */
export const oauthProvider = {
  wellKnown: () => apiGet<WellKnownDoc>("/.well-known/wuling-clients"),

  /** GET /api/v1/oauth/clients/{client_id} */
  publicClient: (clientId: string) =>
    apiGet<OAuthClientPublic>(`/api/v1/oauth/clients/${enc(clientId)}`),

  /** GET /api/v1/oauth/authorize/preview?req=... */
  authorizePreview: (req: string) =>
    apiGet<AuthorizePreview>("/api/v1/oauth/authorize/preview", { req }),

  /** POST /api/v1/oauth/authorize/decision */
  authorizeDecision: (req: string, decision: "allow" | "deny") =>
    apiPost<{ redirect_url: string }>("/api/v1/oauth/authorize/decision", { req, decision }),

  /** POST /api/v1/oauth/device/approve */
  deviceApprove: (userCode: string) =>
    apiPost<{ status: "approved" }>("/api/v1/oauth/device/approve", { user_code: userCode }),

  /** POST /api/v1/oauth/device/deny */
  deviceDeny: (userCode: string) =>
    apiPost("/api/v1/oauth/device/deny", { user_code: userCode }),

  /**
   * Build the top-level URL that begins a third-party Authorization Code
   * flow. Used by anything that needs to test the flow end-to-end from the
   * frontend (e.g. a "Try this app" button on the OAuth Apps settings page).
   */
  authorizeStartURL: (params: {
    clientId: string;
    redirectUri: string;
    scope: string;
    state: string;
    codeChallenge: string;
  }): string => {
    const q = new URLSearchParams({
      response_type: "code",
      client_id: params.clientId,
      redirect_uri: params.redirectUri,
      scope: params.scope,
      state: params.state,
      code_challenge: params.codeChallenge,
      code_challenge_method: "S256",
    });
    return `/api/v1/oauth/authorize?${q.toString()}`;
  },

  authorizations: {
    list: () =>
      apiGet<AuthorizationView[]>("/api/v1/oauth/authorizations"),
    revoke: (id: string) =>
      apiDelete(`/api/v1/oauth/authorizations/${enc(id)}`),
  },

  apps: {
    list: () =>
      apiGet<OAuthAppView[]>("/api/v1/oauth/apps"),
    create: (body: CreateOAuthAppRequest) =>
      apiPost<CreateOAuthAppResponse>("/api/v1/oauth/apps", body),
    update: (id: string, body: UpdateOAuthAppRequest) =>
      apiPatch<OAuthAppView>(`/api/v1/oauth/apps/${enc(id)}`, body),
    delete: (id: string) =>
      apiDelete(`/api/v1/oauth/apps/${enc(id)}`),
    resetSecret: (id: string) =>
      apiPost<{ client_secret: string }>(`/api/v1/oauth/apps/${enc(id)}/reset-secret`),
  },

  admin: {
    listApps: () =>
      apiGet<OAuthAppView[]>("/api/v1/admin/oauth/apps"),
    updateApp: (id: string, body: { is_first_party?: boolean }) =>
      apiPatch<OAuthAppView>(`/api/v1/admin/oauth/apps/${enc(id)}`, body),
    deleteApp: (id: string) =>
      apiDelete(`/api/v1/admin/oauth/apps/${enc(id)}`),
  },
};
