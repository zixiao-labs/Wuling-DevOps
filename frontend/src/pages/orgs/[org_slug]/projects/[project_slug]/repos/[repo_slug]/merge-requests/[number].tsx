import { Button, Card, Input, Label, Tabs, TextArea, TextField } from "@heroui/react";
import { useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { mergeRequests as mrApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { DiffView } from "@/components/diff-view";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { Markdown } from "@/components/markdown";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
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

  if (error) return <ErrorBanner error={error} />;
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

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <header>
        <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
          <h1 style={{ margin: 0, fontSize: "1.5rem" }}>
            {mr.title} <span style={{ color: "var(--muted)" }}>#{mr.number}</span>
          </h1>
          <StateBadge state={mr.state} />
        </div>
        <p style={{ color: "var(--muted)", marginTop: "0.25rem", fontSize: "0.9rem" }}>
          <UserAvatar user={mr.author} size={18} /> {mr.author.username} 想把{" "}
          <code>{shortRef(mr.source_ref)}</code> 合入 <code>{shortRef(mr.target_ref)}</code> · 创建{" "}
          <RelativeTime iso={mr.created_at} />
        </p>
      </header>

      <Card>
        <Card.Content>
          {mr.body ? <Markdown source={mr.body} /> : <span style={{ color: "var(--muted)" }}>（无描述）</span>}
        </Card.Content>
      </Card>

      {mr.state === "open" ? (
        <Card>
          <Card.Header>
            <Card.Title>合并</Card.Title>
            <Card.Description>挑一种策略。FF 要求 target 是 source 的祖先。</Card.Description>
          </Card.Header>
          <Card.Content>
            <ErrorBanner error={mergeError} />
            <TextField name="merge_message" value={mergeMessage} onChange={setMergeMessage}>
              <Label>提交信息（仅 merge-commit / squash）</Label>
              <Input placeholder={`Merge #${mr.number}: ${mr.title}`} />
            </TextField>
            <div style={{ display: "flex", gap: "0.5rem", marginTop: "0.75rem", flexWrap: "wrap" }}>
              <Button onPress={() => doMerge("ff")} isDisabled={busy}>FF</Button>
              <Button variant="secondary" onPress={() => doMerge("merge-commit")} isDisabled={busy}>
                Merge commit
              </Button>
              <Button variant="secondary" onPress={() => doMerge("squash")} isDisabled={busy}>
                Squash
              </Button>
              <span style={{ flex: 1 }} />
              <Button variant="danger-soft" onPress={doClose} isDisabled={busy}>
                关闭
              </Button>
            </div>
          </Card.Content>
        </Card>
      ) : mr.state === "closed" ? (
        <Card>
          <Card.Content>
            <Button variant="secondary" onPress={doReopen} isDisabled={busy}>
              重新打开
            </Button>
          </Card.Content>
        </Card>
      ) : (
        <Card>
          <Card.Content>
            <strong>已合并。</strong>提交 <code>{mr.merge_commit_oid?.slice(0, 8) ?? "—"}</code> ·{" "}
            策略 {mr.merge_strategy} · <RelativeTime iso={mr.merged_at} />
          </Card.Content>
        </Card>
      )}

      <Tabs defaultSelectedKey="commits">
        <Tabs.ListContainer>
          <Tabs.List aria-label="MR 详情">
            <Tabs.Tab id="commits">提交 ({commits.length}) <Tabs.Indicator /></Tabs.Tab>
            <Tabs.Tab id="files">文件 ({diff.files.length}) <Tabs.Indicator /></Tabs.Tab>
            <Tabs.Tab id="comments">评论 ({comments.length}) <Tabs.Indicator /></Tabs.Tab>
            <Tabs.Tab id="reviews">评审 ({reviews.length}) <Tabs.Indicator /></Tabs.Tab>
          </Tabs.List>
        </Tabs.ListContainer>

        <Tabs.Panel id="commits">
          <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {commits.map((c) => (
              <li
                key={c.oid}
                style={{
                  display: "flex",
                  gap: "0.5rem",
                  padding: "0.4rem 0",
                  borderBottom: "1px solid var(--separator)",
                  fontSize: "0.9rem",
                }}
              >
                <code style={{ color: "var(--muted)" }}>{c.oid.slice(0, 8)}</code>
                <span style={{ flex: 1 }}>{c.message.split("\n")[0]}</span>
                <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                  {c.author.name} · <RelativeTime iso={c.author.when} />
                </span>
              </li>
            ))}
          </ul>
        </Tabs.Panel>

        <Tabs.Panel id="files">
          {diff.files.length === 0 ? (
            <div style={{ color: "var(--muted)" }}>（无差异）</div>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: "0.75rem" }}>
              {diff.files.map((f) => (
                <details key={f.path + f.status} open style={{ border: "1px solid var(--border)", borderRadius: "var(--radius)" }}>
                  <summary
                    style={{
                      padding: "0.5rem 0.75rem",
                      background: "var(--surface-secondary)",
                      cursor: "pointer",
                      display: "flex",
                      alignItems: "center",
                      gap: "0.5rem",
                    }}
                  >
                    <code style={{ fontSize: "0.85rem" }}>{f.path}</code>
                    <span style={{ color: "var(--muted)", fontSize: "0.75rem" }}>
                      {f.status} · <span style={{ color: "var(--success)" }}>+{f.additions}</span>{" "}
                      <span style={{ color: "var(--danger)" }}>−{f.deletions}</span>
                    </span>
                  </summary>
                  <div style={{ padding: "0.5rem" }}>
                    {f.patch ? <DiffView patch={f.patch} /> : <em>未包含 patch</em>}
                  </div>
                </details>
              ))}
            </div>
          )}
        </Tabs.Panel>

        <Tabs.Panel id="comments">
          <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {comments.map((c) => (
              <li
                key={c.id}
                style={{
                  padding: "0.75rem",
                  background: "var(--surface)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--radius)",
                  marginBottom: "0.5rem",
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: "0.5rem", marginBottom: "0.5rem" }}>
                  <UserAvatar user={c.author} size={20} />
                  <strong>{c.author.username}</strong>
                  <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                    <RelativeTime iso={c.created_at} />
                  </span>
                </div>
                <Markdown source={c.body} />
              </li>
            ))}
          </ul>
          <form onSubmit={postComment} style={{ marginTop: "1rem" }}>
            <TextField name="comment" value={commentBody} onChange={setCommentBody} isRequired>
              <Label>新评论</Label>
              <TextArea rows={3} />
            </TextField>
            <div style={{ marginTop: "0.5rem" }}>
              <Button type="submit" isDisabled={!commentBody.trim()}>
                发表
              </Button>
            </div>
          </form>
        </Tabs.Panel>

        <Tabs.Panel id="reviews">
          <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {reviews.map((r) => (
              <li
                key={r.id}
                style={{
                  padding: "0.75rem",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--radius)",
                  marginBottom: "0.5rem",
                  background: "var(--surface)",
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: "0.5rem", marginBottom: "0.5rem" }}>
                  <UserAvatar user={r.author} size={20} />
                  <strong>{r.author.username}</strong>
                  <ReviewBadge state={r.state} />
                  <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                    <RelativeTime iso={r.created_at} />
                  </span>
                </div>
                {r.body ? <Markdown source={r.body} /> : null}
              </li>
            ))}
          </ul>
          <form onSubmit={postReview} style={{ marginTop: "1rem" }}>
            <div style={{ display: "flex", gap: "0.5rem", marginBottom: "0.5rem" }}>
              {(["approved", "changes_requested", "commented"] as const).map((s) => (
                <label
                  key={s}
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: "0.3rem",
                    padding: "0.25rem 0.6rem",
                    border: "1px solid var(--border)",
                    borderRadius: "var(--field-radius)",
                    cursor: "pointer",
                    background: reviewState === s ? "var(--surface-secondary)" : "var(--surface)",
                  }}
                >
                  <input
                    type="radio"
                    name="review-state"
                    checked={reviewState === s}
                    onChange={() => setReviewState(s)}
                  />
                  {s.replace("_", " ")}
                </label>
              ))}
            </div>
            <TextField name="review_body" value={reviewBody} onChange={setReviewBody}>
              <Label>评审说明</Label>
              <TextArea rows={3} />
            </TextField>
            <div style={{ marginTop: "0.5rem" }}>
              <Button type="submit">提交评审</Button>
            </div>
          </form>
        </Tabs.Panel>
      </Tabs>
    </div>
  );
}

function shortRef(r: string): string {
  return r.replace(/^refs\/(heads|tags)\//, "");
}

function StateBadge({ state }: { state: MergeRequest["state"] }) {
  const map = {
    open: { bg: "var(--success)", fg: "var(--success-foreground)", label: "Open" },
    merged: { bg: "var(--accent)", fg: "var(--accent-foreground)", label: "Merged" },
    closed: { bg: "var(--default)", fg: "var(--default-foreground)", label: "Closed" },
  } as const;
  const c = map[state];
  return (
    <span
      style={{
        background: c.bg,
        color: c.fg,
        padding: "0.1rem 0.6rem",
        borderRadius: "999px",
        fontSize: "0.75rem",
        textTransform: "uppercase",
      }}
    >
      {c.label}
    </span>
  );
}

function ReviewBadge({ state }: { state: ReviewState }) {
  const map: Record<ReviewState, { bg: string; fg: string; label: string }> = {
    approved: { bg: "var(--success)", fg: "var(--success-foreground)", label: "Approved" },
    changes_requested: { bg: "var(--warning)", fg: "var(--warning-foreground)", label: "Changes Requested" },
    commented: { bg: "var(--surface-secondary)", fg: "var(--foreground)", label: "Commented" },
  };
  const c = map[state];
  return (
    <span
      style={{
        background: c.bg,
        color: c.fg,
        padding: "0.05rem 0.5rem",
        borderRadius: "999px",
        fontSize: "0.7rem",
      }}
    >
      {c.label}
    </span>
  );
}
