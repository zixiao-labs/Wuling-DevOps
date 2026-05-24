import { Button, Label, TextArea, TextField } from "@heroui/react";
import { useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import CircleQuestion from "@gravity-ui/icons/CircleQuestion";

import { issues as issuesApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { LabelChip } from "@/components/label-chip";
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
import { StateBadge } from "@/components/page/badges";
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
      <PageContainer>
        <ErrorBanner
          error={new ApiError(400, "unknown", `无效的 issue 编号：${params.number ?? ""}`)}
        />
      </PageContainer>
    );
  }
  if (error) {
    return (
      <PageContainer>
        <ErrorBanner error={error} />
      </PageContainer>
    );
  }
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
    <PageContainer wide>
      <PageHeader
        eyebrow={
          <span className="font-mono">
            #{issue.number} · 由 {issue.author.username} 创建
          </span>
        }
        title={
          <span className="inline-flex items-center gap-3">
            <span>{issue.title}</span>
            <StateBadge state={issue.state} />
          </span>
        }
        description={
          <span>
            <RelativeTime iso={issue.created_at} /> 创建
            {issue.closed_at ? (
              <> · <RelativeTime iso={issue.closed_at} /> 关闭</>
            ) : null}
          </span>
        }
        actions={
          <Button
            variant={issue.state === "open" ? "outline" : "primary"}
            onPress={toggleState}
            isDisabled={toggling}
          >
            {issue.state === "open" ? "关闭 Issue" : "重新打开"}
          </Button>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[1fr_280px]">
        {/* Conversation column */}
        <div className="flex flex-col gap-3">
          <CommentCard
            author={issue.author}
            createdAt={issue.created_at}
            body={issue.body}
            tone="opener"
          />
          {comments.map((c) => (
            <CommentCard
              key={c.id}
              author={c.author}
              createdAt={c.created_at}
              body={c.body}
            />
          ))}

          <Surface>
            <SurfaceHeader title={`回复 · ${comments.length} 条评论`} />
            <SurfaceBody>
              <form onSubmit={postComment} className="flex flex-col gap-2">
                <TextField name="comment" value={commentBody} onChange={setCommentBody} isRequired>
                  <Label className="sr-only">新评论</Label>
                  <TextArea rows={3} placeholder="留下你的回复…" />
                </TextField>
                <div className="flex justify-end">
                  <Button type="submit" isDisabled={!commentBody.trim() || posting}>
                    {posting ? "发表中…" : "发表评论"}
                  </Button>
                </div>
              </form>
            </SurfaceBody>
          </Surface>
        </div>

        {/* Side panel */}
        <aside className="flex flex-col gap-3">
          <Surface>
            <SurfaceHeader title="元数据" />
            <SurfaceBody>
              <Meta label="状态">
                <StateBadge state={issue.state} />
              </Meta>
              <Meta label="作者">
                <span className="inline-flex items-center gap-1.5">
                  <UserAvatar user={issue.author} size={18} />
                  <span className="text-fg">{issue.author.username}</span>
                </span>
              </Meta>
              <Meta label="指派">
                {issue.assignees.length > 0 ? (
                  <div className="flex flex-wrap items-center gap-1.5">
                    {issue.assignees.map((a) => (
                      <span key={a.id} className="inline-flex items-center gap-1.5 text-[12px]">
                        <UserAvatar user={a} size={16} />
                        {a.username}
                      </span>
                    ))}
                  </div>
                ) : (
                  <span className="text-muted">—</span>
                )}
              </Meta>
              <Meta label="标签">
                {issue.labels.length > 0 ? (
                  <div className="flex flex-wrap gap-1">
                    {issue.labels.map((l) => (
                      <LabelChip key={l.id} label={l} size="sm" />
                    ))}
                  </div>
                ) : (
                  <span className="text-muted">—</span>
                )}
              </Meta>
              <Meta label="评论">
                <span className="inline-flex items-center gap-1.5 font-mono text-[12px]">
                  <CircleQuestion width={11} height={11} />
                  {issue.comment_count}
                </span>
              </Meta>
            </SurfaceBody>
          </Surface>
        </aside>
      </div>
    </PageContainer>
  );
}

function CommentCard({
  author,
  createdAt,
  body,
  tone,
}: {
  author: Issue["author"];
  createdAt: string;
  body: string;
  tone?: "opener";
}) {
  return (
    <article className="overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)]">
      <header
        className={[
          "flex items-center gap-2.5 border-b border-[var(--separator)] px-4 py-2",
          tone === "opener" ? "bg-[var(--surface-secondary)]" : "",
        ].join(" ")}
      >
        <UserAvatar user={author} size={22} />
        <span className="text-[13px] font-medium text-fg">{author.username}</span>
        <span className="text-[11.5px] text-muted">
          {tone === "opener" ? "创建于" : "评论于"} <RelativeTime iso={createdAt} />
        </span>
      </header>
      <div className="px-4 py-3">
        {body ? (
          <Markdown source={body} />
        ) : (
          <div className="text-[12.5px] text-muted">（无正文）</div>
        )}
      </div>
    </article>
  );
}

function Meta({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-3 border-b border-[var(--separator)] py-2 last:border-b-0">
      <span className="text-[11.5px] uppercase tracking-wider text-muted">{label}</span>
      <div className="min-w-0 text-right text-[12.5px]">{children}</div>
    </div>
  );
}
