/**
 * Hand-curated TypeScript view of api/openapi.yaml schemas.
 *
 * Kept in sync manually with the OpenAPI spec; run `npm run api:types` to
 * additionally regenerate the full openapi-typescript file at schema.gen.ts
 * if you want a machine-checked counterpart. The types here are what the
 * UI imports day-to-day because they read better.
 */

// ---------------- Errors ----------------

export type ApiErrorCode =
  | "validation"
  | "unauthorized"
  | "forbidden"
  | "not_found"
  | "conflict"
  | "already_exists"
  | "rate_limited"
  | "unsupported"
  | "bad_request"
  | "internal"
  | "unavailable";

export interface ApiErrorBody {
  error: {
    code: ApiErrorCode;
    message: string;
    details?: Record<string, unknown>;
  };
}

// ---------------- Auth ----------------

export type UserApprovalStatus = "pending" | "approved" | "rejected";

export interface User {
  id: string;
  username: string;
  email: string;
  display_name: string;
  is_admin: boolean;
  is_active: boolean;
  approval_status: UserApprovalStatus;
  approval_note?: string;
  approved_at?: string | null;
  github_login?: string;
  created_at: string;
}

export interface RegisterRequest {
  username: string;
  email: string;
  password: string;
  display_name?: string;
}

export interface LoginRequest {
  login: string; // username or email
  password: string;
}

export interface TokenResponse {
  access_token: string;
  token_type: string;
  expires_at: string;
  user: User;
}

/** 202 response from /register when admin approval is required. */
export interface PendingAccountResponse {
  status: "pending" | "rejected";
  message: string;
  user: User;
}

/**
 * /oauth/github/confirm response.
 *
 * Mirrors /register: a TokenResponse-shaped body (with the linked-or-created
 * flag) when the account is approved and ready to use, or a
 * PendingAccountResponse-shaped body (HTTP 202) when an admin still has to
 * approve. Callers branch on the presence of `access_token` rather than HTTP
 * status since the fetch client doesn't expose status codes.
 */
export interface OAuthConfirmSuccessResponse extends TokenResponse {
  new_account: boolean;
}

export type OAuthConfirmResponse =
  | OAuthConfirmSuccessResponse
  | PendingAccountResponse;

export function isOAuthConfirmPending(
  r: OAuthConfirmResponse,
): r is PendingAccountResponse {
  return !("access_token" in r);
}

export type OAuthConfirmAction = "link" | "new";

export interface PatchUserRequest {
  approval_status?: UserApprovalStatus;
  approval_note?: string;
  is_admin?: boolean;
  is_active?: boolean;
}

export type PatScope = "repo:read" | "repo:write";

export interface AccessTokenView {
  id: string;
  name: string;
  scopes: PatScope[];
  expires_at: string | null;
  created_at: string;
  /** Raw token value, returned only on creation. */
  token?: string;
}

export interface CreatePatRequest {
  name: string;
  scopes?: PatScope[];
  expires_at?: string;
}

export interface SSHKey {
  id: string;
  title: string;
  fingerprint: string;
  public_key: string;
  created_at: string;
  last_used_at: string | null;
}

export interface CreateSSHKeyRequest {
  title: string;
  public_key: string;
}

// ---------------- Orgs & Projects ----------------

export interface Org {
  id: string;
  slug: string;
  display_name: string;
  description: string;
  is_personal: boolean;
  created_at: string;
}

export interface CreateOrgRequest {
  slug: string;
  display_name?: string;
  description?: string;
}

export type Visibility = "private" | "internal" | "public";

export interface Project {
  id: string;
  org_id: string;
  slug: string;
  display_name: string;
  description: string;
  visibility: Visibility;
  created_at: string;
}

export interface CreateProjectRequest {
  slug: string;
  display_name?: string;
  description?: string;
  visibility?: Visibility;
}

// ---------------- Repos ----------------

export interface Repo {
  id: string;
  project_id: string;
  slug: string;
  display_name: string;
  description: string;
  default_branch: string;
  visibility: Visibility;
  is_empty: boolean;
  size_bytes: number;
  created_at: string;
}

export interface CreateRepoRequest {
  slug: string;
  display_name?: string;
  description?: string;
  default_branch?: string;
  visibility?: Visibility;
}

export interface GitRef {
  name: string;
  oid: string;
  is_branch: boolean;
  is_tag: boolean;
}

export interface TreeEntry {
  name: string;
  oid: string;
  filemode: number;
  kind: "blob" | "tree" | "submodule" | "tag" | "other";
}

export interface TreeResponse {
  oid: string;
  entries: TreeEntry[];
}

export interface BlobResponse {
  oid: string;
  size: number;
  is_binary: boolean;
  encoding: "utf-8" | "base64";
  content: string;
}

export interface Signature {
  name: string;
  email: string;
  when: string;
}

export interface Commit {
  oid: string;
  tree_oid: string;
  author: Signature;
  committer: Signature;
  message: string;
  parents: string[];
}

// ---------------- Issues & Labels ----------------

export interface UserRef {
  id: string;
  username: string;
  display_name: string;
}

export interface Label {
  id: string;
  project_id: string;
  name: string;
  color: string;
  description: string;
  created_at: string;
}

export interface CreateLabelRequest {
  name: string;
  color?: string;
  description?: string;
}

export type IssueState = "open" | "closed";

export interface Issue {
  id: string;
  project_id: string;
  number: number;
  title: string;
  body: string;
  state: IssueState;
  author: UserRef;
  closed_at: string | null;
  closed_by: UserRef | null;
  created_at: string;
  updated_at: string;
  labels: Label[];
  assignees: UserRef[];
  comment_count: number;
}

export interface CreateIssueRequest {
  title: string;
  body?: string;
  labels?: string[]; // label uuids
  assignees?: string[]; // user uuids
}

export interface PatchIssueRequest {
  title?: string;
  body?: string;
  state?: IssueState;
  labels?: string[];
  assignees?: string[];
}

export interface IssueComment {
  id: string;
  issue_id: string;
  author: UserRef;
  body: string;
  created_at: string;
  updated_at: string;
}

export interface IssueListQuery {
  state?: IssueState;
  label?: string;
  assignee?: string;
  author?: string;
  search?: string;
  limit?: number;
}

// ---------------- Merge Requests ----------------

export type MRState = "open" | "merged" | "closed";
export type MergeStrategy = "ff" | "merge-commit" | "squash";
export type ReviewState = "approved" | "changes_requested" | "commented";

export interface MergeRequest {
  id: string;
  repo_id: string;
  project_id: string;
  number: number;
  title: string;
  body: string;
  state: MRState;
  source_ref: string;
  target_ref: string;
  source_oid_at_open: string;
  target_oid_at_open: string;
  merge_strategy: MergeStrategy | null;
  merge_commit_oid: string | null;
  author: UserRef;
  merged_by: UserRef | null;
  closed_by: UserRef | null;
  merged_at: string | null;
  closed_at: string | null;
  created_at: string;
  updated_at: string;
  comment_count: number;
  review_count: number;
}

export interface CreateMergeRequestRequest {
  title: string;
  body?: string;
  source_ref: string;
  target_ref: string;
}

export interface PatchMergeRequestRequest {
  title?: string;
  body?: string;
}

export interface MergeMRRequest {
  strategy: MergeStrategy;
  message?: string;
}

export interface MRComment {
  id: string;
  mr_id: string;
  author: UserRef;
  body: string;
  created_at: string;
  updated_at: string;
}

export interface MRReview {
  id: string;
  mr_id: string;
  author: UserRef;
  state: ReviewState;
  body: string;
  created_at: string;
}

export interface CreateMRReviewRequest {
  state: ReviewState;
  body?: string;
}

export interface MRDiffEntry {
  path: string;
  old_path: string;
  status: "added" | "modified" | "deleted" | "renamed" | "copied" | "typechange" | "other";
  additions: number;
  deletions: number;
  patch?: string;
}

export interface MRDiffResponse {
  base_oid: string;
  source_oid: string;
  target_oid: string;
  files: MRDiffEntry[];
}

export interface MRListQuery {
  state?: MRState;
  target_ref?: string;
  author?: string;
  limit?: number;
}

// ---------------- Wiki ----------------

export interface WikiPage {
  path: string;
  size: number;
  updated_at: string | null;
}

export interface WikiPageContent {
  path: string;
  raw: string;
  html: string;
  commit_oid: string;
}

export interface PutWikiPageRequest {
  content: string;
  message?: string;
}

export interface WikiHistoryCommit {
  oid: string;
  tree_oid: string;
  message: string;
  parents: string[];
  author: { name: string; email: string; when: string };
  committer: { name: string; email: string; when: string };
}

// ---------------- Insights ----------------

export interface ActivityDay {
  date: string; // YYYY-MM-DD UTC
  issues_opened: number;
  issues_closed: number;
  mrs_opened: number;
  mrs_merged: number;
  commits: number;
}

export interface ContributorStat {
  email: string;
  name: string;
  commits: number;
}

export interface LanguageStats {
  bytes: Record<string, number>;
  files: Record<string, number>;
  truncated?: boolean;
}
