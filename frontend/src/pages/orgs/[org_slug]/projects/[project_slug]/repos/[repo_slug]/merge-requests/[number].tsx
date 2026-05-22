import { Button, Input, Label, TextArea, TextField } from "@heroui/react";
import { useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import BranchesRight from "@gravity-ui/icons/BranchesRight";

import { mergeRequests as mrApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { DiffView } from "@/components/diff-view";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { Markdown } from "@/components/markdown";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { Pill, StateBadge } from "@/components/page/badges";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type {
  Commit,
  MRComment,
  MRDiffResponse,
  MRReview,
  MergeRequest,
  MergeStrategy,
  ReviewState,
} from "@/api/types";

type Tab = "commits" | "files" | "comments" | "reviews";

export default function MRDetailPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const repoSlug = params.repo_slug ?? "";
  const number = Number(params.number);

  const [mr, setMR] = useState<MergeRequest | null>(null);
  const [commits, setCommits] = useState<Commit[] | null>(null);
  const [diff, setDiff] = useState<MRDiffResponse | null>(null);
  const [comments, setComments] = useState<MRComment[] | null>(null);
  const [reviews, setReviews] = useState<MRReview[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [commentBody, setCommentBody] = useState("");
  const [reviewState, setReviewState] = useState<ReviewState>("commented");
  const [reviewBody, setReviewBody] = useState("");
  const [mergeMessage, setMergeMessage] = useState("");
  const [mergeError, setMergeError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState<Tab>("commits");

  function refresh() {
    if (!Number.isFinite(number)) return;
    setError(null);
    Promise.all([
      mrApi.get(org.slug, project.slug, repoSlug, number),
      mrApi.commits(org.slug, project.slug, repoSlug, number),
      mrApi.diff(org.slug, project.slug, repoSlug, number, { includePatch: true }),
      mrApi.comments.list(org.slug, project.slug, repoSlug, number),
      mrApi.reviews.list(org.slug, project.slug, repoSlug, number),
    ])
      .then(([m, c, d, cm, rv]) => {
        setMR(m);
        setCommits(c);
        setDiff(d);
        setComments(cm);
        setReviews(rv);
      })
      .catch((e) => setError(e as ApiError));
  }

  useEffect(refresh, [org.slug, project.slug, repoSlug, number]);

  if (error) return <PageContainer><ErrorBanner error={error} /></PageContainer>;
  if (!mr || !commits || !diff || !comments || !reviews) return <Loading />;

  async function doMerge(strategy: MergeStrategy) {
    setBusy(true);
    setMergeError(null);
    try {
      await mrApi.merge(org.slug, project.slug, repoSlug, number, {
        strategy,
        message: strategy === "ff" ? undefined : mergeMessage || undefined,
      });
      refresh();
    } catch (err) {
      setMergeError(err as ApiError);
    } finally {
      setBusy(false);
    }
  }

  async function doClose() {
    if (!confirm("关闭这个 MR？可以稍后再次打开。")) return;
    setBusy(true);
    try {
      await mrApi.close(org.slug, project.slug, repoSlug, number);
      refresh();
    } catch (err) {
      setMergeError(err as ApiError);
    } finally {
      setBusy(false);
    }
  }

  async function doReopen() {
    setBusy(true);
    try {
      await mrApi.reopen(org.slug, project.slug, repoSlug, number);
      refresh();
    } catch (err) {
      setMergeError(err as ApiError);
    } finally {
      setBusy(false);
    }
  }

  async function postComment(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!commentBody.trim()) return;
    try {
      await mrApi.comments.create(org.slug, project.slug, repoSlug, number, { body: commentBody });
      setCommentBody("");
      refresh();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  async function postReview(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    try {
      await mrApi.reviews.create(org.slug, project.slug, repoSlug, number, {
        state: reviewState,
        body: reviewBody || undefined,
      });
      setReviewBody("");
      refresh();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  const totals = diff.files.reduce(
    (acc, f) => ({ adds: acc.adds + f.additions, dels: acc.dels + f.deletions }),
    { adds: 0, dels: 0 },
  );

  return (
    <PageContainer wide>
      <PageHeader
        eyebrow={
          <span className="font-mono">
            #{mr.number} · {mr.author.username} 想合并
          </span>
        }
        title={
          <span className="inline-flex items-center gap-3">
            <span>{mr.title}</span>
            <StateBadge state={mr.state} />
          </span>
        }
        description={
          <span className="inline-flex flex-wrap items-center gap-2 font-mono text-[12px]">
            <code className="rounded-sm bg-[var(--surface-secondary)] px-1.5 py-px text-fg">
              {shortRef(mr.source_ref)}
            </code>
            <BranchesRight width={11} height={11} className="opacity-60" />
            <code className="rounded-sm bg-[var(--surface-secondary)] px-1.5 py-px text-fg">
              {shortRef(mr.target_ref)}
            </code>
            <span className="text-muted">
              · 创建 <RelativeTime iso={mr.created_at} />
            </span>
          </span>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <div className="flex flex-col gap-4">
          <Surface>
            <SurfaceHeader title="描述" />
            <SurfaceBody>
              {mr.body ? <Markdown source={mr.body} /> : <span className="text-[13px] text-muted">（无描述）</span>}
            </SurfaceBody>
          </Surface>

          {/* Tabs */}
          <nav className="-mb-3 flex items-center gap-0 overflow-x-auto border-b border-[var(--separator)]">
            {[
              { id: "commits" as const, label: `提交`, count: commits.length },
              { id: "files" as const, label: `文件`, count: diff.files.length },
              { id: "comments" as const, label: `评论`, count: comments.length },
              { id: "reviews" as const, label: `评审`, count: reviews.length },
            ].map((t) => {
              const active = tab === t.id;
              return (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => setTab(t.id)}
                  className={[
                    "relative inline-flex items-center gap-1.5 whitespace-nowrap px-3 py-2 text-[13px]",
                    active ? "text-fg" : "text-fg/65 hover:text-fg",
                  ].join(" ")}
                >
                  {t.label}
                  <span className="rounded-full bg-[var(--surface-tertiary)] px-1.5 py-px text-[10px] tabular-nums text-muted">
                    {t.count}
                  </span>
                  {active ? (
                    <span aria-hidden className="absolute inset-x-2 -bottom-px h-[2px] rounded-full bg-accent" />
                  ) : null}
                </button>
              );
            })}
          </nav>

          {tab === "commits" ? (
            <Surface>
              <SurfaceBody noPad>
                <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
                  {commits.map((c) => (
                    <li key={c.oid} className="flex items-center gap-3 px-4 py-2 text-[12.5px]">
                      <code className="font-mono text-[11px] text-muted">{c.oid.slice(0, 8)}</code>
                      <span className="min-w-0 flex-1 truncate text-fg">{c.message.split("\n")[0]}</span>
                      <span className="shrink-0 text-[11.5px] text-muted">
                        {c.author.name} · <RelativeTime iso={c.author.when} />
                      </span>
                    </li>
                  ))}
                </ul>
              </SurfaceBody>
            </Surface>
          ) : null}

          {tab === "files" ? (
            diff.files.length === 0 ? (
              <Surface>
                <SurfaceBody>
                  <div className="text-[13px] text-muted">（无差异）</div>
                </SurfaceBody>
              </Surface>
            ) : (
              <div className="flex flex-col gap-3">
                <div className="text-[11.5px] text-muted">
                  共 {diff.files.length} 个文件变更 ·{" "}
                  <span className="text-[var(--success)]">+{totals.adds}</span>{" "}
                  <span className="text-[var(--danger)]">−{totals.dels}</span>
                </div>
                {diff.files.map((f) => (
                  <details key={f.path + f.status} open className="overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)]">
                    <summary className="flex cursor-pointer items-center gap-3 border-b border-[var(--separator)] bg-[var(--surface-secondary)]/60 px-3 py-2 text-[12.5px]">
                      <code className="font-mono text-fg">{f.path}</code>
                      <span className="text-[11px] uppercase tracking-wider text-muted">{f.status}</span>
                      <span className="ml-auto text-[11px] font-mono">
                        <span className="text-[var(--success)]">+{f.additions}</span>{" "}
                        <span className="text-[var(--danger)]">−{f.deletions}</span>
                      </span>
                    </summary>
                    <div className="p-2">
                      {f.patch ? <DiffView patch={f.patch} /> : <em className="text-muted">未包含 patch</em>}
                    </div>
                  </details>
                ))}
              </div>
            )
          ) : null}

          {tab === "comments" ? (
            <div className="flex flex-col gap-3">
              {comments.map((c) => (
                <article key={c.id} className="overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)]">
                  <header className="flex items-center gap-2.5 border-b border-[var(--separator)] px-4 py-2">
                    <UserAvatar user={c.author} size={20} />
                    <span className="text-[13px] font-medium text-fg">{c.author.username}</span>
                    <span className="text-[11.5px] text-muted">
                      <RelativeTime iso={c.created_at} />
                    </span>
                  </header>
                  <div className="px-4 py-3"><Markdown source={c.body} /></div>
                </article>
              ))}
              <Surface>
                <SurfaceHeader title="新评论" />
                <SurfaceBody>
                  <form onSubmit={postComment} className="flex flex-col gap-2">
                    <TextField name="comment" value={commentBody} onChange={setCommentBody} isRequired>
                      <Label className="sr-only">新评论</Label>
                      <TextArea rows={3} placeholder="留下你的反馈…" />
                    </TextField>
                    <div className="flex justify-end">
                      <Button type="submit" isDisabled={!commentBody.trim()}>
                        发表评论
                      </Button>
                    </div>
                  </form>
                </SurfaceBody>
              </Surface>
            </div>
          ) : null}

          {tab === "reviews" ? (
            <div className="flex flex-col gap-3">
              {reviews.map((r) => (
                <article key={r.id} className="overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)]">
                  <header className="flex items-center gap-2.5 border-b border-[var(--separator)] px-4 py-2">
                    <UserAvatar user={r.author} size={20} />
                    <span className="text-[13px] font-medium text-fg">{r.author.username}</span>
                    <ReviewBadge state={r.state} />
                    <span className="ml-auto text-[11.5px] text-muted">
                      <RelativeTime iso={r.created_at} />
                    </span>
                  </header>
                  {r.body ? <div className="px-4 py-3"><Markdown source={r.body} /></div> : null}
                </article>
              ))}
              <Surface>
                <SurfaceHeader title="提交评审" />
                <SurfaceBody>
                  <form onSubmit={postReview} className="flex flex-col gap-3">
                    <div className="flex flex-wrap gap-2">
                      {(["approved", "changes_requested", "commented"] as const).map((s) => {
                        const selected = reviewState === s;
                        return (
                          <label
                            key={s}
                            className={[
                              "inline-flex cursor-pointer items-center gap-2 rounded-md border px-3 py-1.5 text-[12.5px]",
                              selected
                                ? "border-[var(--accent)] bg-[var(--surface-secondary)] text-fg"
                                : "border-[var(--border)] bg-[var(--surface)] text-fg/85 hover:bg-[var(--surface-secondary)]",
                            ].join(" ")}
                          >
                            <input
                              type="radio"
                              name="review-state"
                              checked={selected}
                              onChange={() => setReviewState(s)}
                              className="accent-[var(--accent)]"
                            />
                            {reviewLabel(s)}
                          </label>
                        );
                      })}
                    </div>
                    <TextField name="review_body" value={reviewBody} onChange={setReviewBody}>
                      <Label className="sr-only">评审说明</Label>
                      <TextArea rows={3} placeholder="评审说明（可选）" />
                    </TextField>
                    <div className="flex justify-end">
                      <Button type="submit">提交评审</Button>
                    </div>
                  </form>
                </SurfaceBody>
              </Surface>
            </div>
          ) : null}
        </div>

        {/* Sidebar — merge controls + metadata */}
        <aside className="flex flex-col gap-3">
          {mr.state === "open" ? (
            <Surface>
              <SurfaceHeader title="合并" description="FF 要求 target 是 source 的祖先。" />
              <SurfaceBody>
                <ErrorBanner error={mergeError} />
                <TextField name="merge_message" value={mergeMessage} onChange={setMergeMessage}>
                  <Label>提交信息（仅 merge-commit / squash）</Label>
                  <Input placeholder={`Merge #${mr.number}: ${mr.title}`} />
                </TextField>
                <div className="mt-3 flex flex-col gap-1.5">
                  <Button onPress={() => doMerge("ff")} isDisabled={busy}>
                    FF 合并
                  </Button>
                  <Button variant="outline" onPress={() => doMerge("merge-commit")} isDisabled={busy}>
                    Merge commit
                  </Button>
                  <Button variant="outline" onPress={() => doMerge("squash")} isDisabled={busy}>
                    Squash
                  </Button>
                  <div className="mt-1 border-t border-[var(--separator)] pt-2">
                    <Button variant="danger-soft" onPress={doClose} isDisabled={busy} className="w-full">
                      关闭 MR
                    </Button>
                  </div>
                </div>
              </SurfaceBody>
            </Surface>
          ) : mr.state === "closed" ? (
            <Surface>
              <SurfaceHeader title="已关闭" />
              <SurfaceBody>
                <Button variant="outline" onPress={doReopen} isDisabled={busy} className="w-full">
                  重新打开
                </Button>
              </SurfaceBody>
            </Surface>
          ) : (
            <Surface>
              <SurfaceHeader title="已合并" />
              <SurfaceBody>
                <div className="text-[12.5px] text-fg">
                  策略：<Pill tone="info">{mr.merge_strategy ?? "—"}</Pill>
                </div>
                <div className="mt-2 text-[12px] text-muted">
                  提交：<code className="font-mono text-fg">{mr.merge_commit_oid?.slice(0, 8) ?? "—"}</code>
                </div>
                <div className="mt-1 text-[12px] text-muted">
                  时间：<RelativeTime iso={mr.merged_at} />
                </div>
              </SurfaceBody>
            </Surface>
          )}

          <Surface>
            <SurfaceHeader title="元数据" />
            <SurfaceBody>
              <Meta label="作者">
                <span className="inline-flex items-center gap-1.5">
                  <UserAvatar user={mr.author} size={18} />
                  <span className="text-fg">{mr.author.username}</span>
                </span>
              </Meta>
              <Meta label="状态">
                <StateBadge state={mr.state} />
              </Meta>
              <Meta label="评审"><span className="font-mono">{mr.review_count}</span></Meta>
              <Meta label="评论"><span className="font-mono">{mr.comment_count}</span></Meta>
              <Meta label="Source">
                <code className="font-mono text-fg">{shortRef(mr.source_ref)}</code>
              </Meta>
              <Meta label="Target">
                <code className="font-mono text-fg">{shortRef(mr.target_ref)}</code>
              </Meta>
            </SurfaceBody>
          </Surface>
        </aside>
      </div>
    </PageContainer>
  );
}

function reviewLabel(s: ReviewState): string {
  if (s === "approved") return "Approved";
  if (s === "changes_requested") return "Changes Requested";
  return "Commented";
}

function shortRef(r: string): string {
  return r.replace(/^refs\/(heads|tags)\//, "");
}

function ReviewBadge({ state }: { state: ReviewState }) {
  if (state === "approved") return <Pill tone="success">Approved</Pill>;
  if (state === "changes_requested") return <Pill tone="warning">Changes Requested</Pill>;
  return <Pill tone="neutral">Commented</Pill>;
}

function Meta({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-3 border-b border-[var(--separator)] py-2 last:border-b-0">
      <span className="text-[11.5px] uppercase tracking-wider text-muted">{label}</span>
      <span className="min-w-0 text-right text-[12.5px]">{children}</span>
    </div>
  );
}
