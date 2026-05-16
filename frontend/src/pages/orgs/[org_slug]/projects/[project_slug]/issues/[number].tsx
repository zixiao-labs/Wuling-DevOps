import { Button, Card, Label, TextArea, TextField } from "@heroui/react";
import { useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { issues as issuesApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { LabelChip } from "@/components/label-chip";
import { Loading } from "@/components/loading";
import { Markdown } from "@/components/markdown";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Issue, IssueComment } from "@/api/types";

export default function IssueDetailPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const number = Number(params.number);

  const [issue, setIssue] = useState<Issue | null>(null);
  const [comments, setComments] = useState<IssueComment[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [commentBody, setCommentBody] = useState("");
  const [posting, setPosting] = useState(false);
  const [toggling, setToggling] = useState(false);

  const numberIsValid = Number.isInteger(number) && number > 0;

  function refresh() {
    if (!numberIsValid) return;
    Promise.all([
      issuesApi.get(org.slug, project.slug, number),
      issuesApi.comments.list(org.slug, project.slug, number),
    ])
      .then(([i, c]) => {
        setIssue(i);
        setComments(c);
      })
      .catch((e) => setError(e as ApiError));
  }
  useEffect(refresh, [org.slug, project.slug, number]);

  if (!numberIsValid) {
    return (
      <ErrorBanner
        error={
          new ApiError(400, "unknown", `无效的 issue 编号：${params.number ?? ""}`)
        }
      />
    );
  }
  if (error) return <ErrorBanner error={error} />;
  if (!issue || !comments) return <Loading />;

  async function postComment(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setPosting(true);
    try {
      await issuesApi.comments.create(org.slug, project.slug, number, { body: commentBody });
      setCommentBody("");
      refresh();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setPosting(false);
    }
  }

  async function toggleState() {
    setToggling(true);
    try {
      await issuesApi.patch(org.slug, project.slug, number, {
        state: issue!.state === "open" ? "closed" : "open",
      });
      refresh();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setToggling(false);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <header>
        <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
          <h1 style={{ margin: 0, fontSize: "1.5rem" }}>
            {issue.title} <span style={{ color: "var(--muted)" }}>#{issue.number}</span>
          </h1>
          <StateBadge state={issue.state} />
          <span style={{ flex: 1 }} />
          <Button variant={issue.state === "open" ? "secondary" : "primary"} onPress={toggleState} isDisabled={toggling}>
            {issue.state === "open" ? "关闭" : "重新打开"}
          </Button>
        </div>
        <p style={{ color: "var(--muted)", marginTop: "0.25rem", fontSize: "0.9rem" }}>
          <UserAvatar user={issue.author} size={18} /> {issue.author.username} 创建于{" "}
          <RelativeTime iso={issue.created_at} />
          {issue.closed_at ? (
            <>
              {" · "}关闭于 <RelativeTime iso={issue.closed_at} />
            </>
          ) : null}
        </p>
        {issue.labels.length > 0 ? (
          <div style={{ display: "flex", gap: "0.4rem", marginTop: "0.5rem", flexWrap: "wrap" }}>
            {issue.labels.map((l) => (
              <LabelChip key={l.id} label={l} />
            ))}
          </div>
        ) : null}
      </header>

      <Card>
        <Card.Content>
          {issue.body ? <Markdown source={issue.body} /> : <span style={{ color: "var(--muted)" }}>（无正文）</span>}
        </Card.Content>
      </Card>

      <h2 style={{ fontSize: "1rem", margin: 0 }}>评论（{comments.length}）</h2>
      <ul style={{ listStyle: "none", padding: 0, margin: 0, display: "flex", flexDirection: "column", gap: "0.5rem" }}>
        {comments.map((c) => (
          <li
            key={c.id}
            style={{
              padding: "0.75rem",
              background: "var(--surface)",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius)",
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

      <form onSubmit={postComment} style={{ marginTop: "0.5rem" }}>
        <TextField name="comment" value={commentBody} onChange={setCommentBody} isRequired>
          <Label>新评论</Label>
          <TextArea rows={3} />
        </TextField>
        <div style={{ marginTop: "0.5rem" }}>
          <Button type="submit" isDisabled={!commentBody.trim() || posting}>
            {posting ? "发表中…" : "发表"}
          </Button>
        </div>
      </form>
    </div>
  );
}

function StateBadge({ state }: { state: Issue["state"] }) {
  const map = {
    open: { bg: "var(--success)", fg: "var(--success-foreground)", label: "Open" },
    closed: { bg: "var(--accent)", fg: "var(--accent-foreground)", label: "Closed" },
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
      }}
    >
      {c.label}
    </span>
  );
}
