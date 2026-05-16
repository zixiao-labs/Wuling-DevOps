import { Button, Description, Input, Label, TextField } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import { Link, useParams, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { mergeRequests as mrApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { MRState, MergeRequest } from "@/api/types";

const STATES: Array<{ id: MRState | "all"; label: string }> = [
  { id: "open", label: "Open" },
  { id: "merged", label: "Merged" },
  { id: "closed", label: "Closed" },
  { id: "all", label: "全部" },
];

export default function MRListPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const [search, setSearch] = useSearchParams();
  const repoSlug = params.repo_slug ?? "";

  const state = (search.get("state") ?? "open") as MRState | "all";
  const author = search.get("author") ?? "";

  const [items, setItems] = useState<MergeRequest[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setItems(null);
    setError(null);
    mrApi
      .list(org.slug, project.slug, repoSlug, {
        state: state === "all" ? undefined : state,
        author: author || undefined,
        limit: 100,
      })
      .then(setItems)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, repoSlug, state, author]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repoSlug)}/merge-requests`;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <header style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <h1 style={{ margin: 0, fontSize: "1.5rem" }}>合并请求</h1>
        <Link to={`${base}/new`}>
          <Button>
            <PlusIcon width={16} height={16} /> 新建 MR
          </Button>
        </Link>
      </header>

      <div style={{ display: "flex", gap: "1rem", alignItems: "center", flexWrap: "wrap" }}>
        <div style={{ display: "inline-flex", gap: "0.25rem" }}>
          {STATES.map((s) => (
            <button
              key={s.id}
              onClick={() => {
                const sp = new URLSearchParams(search);
                if (s.id === "open") sp.delete("state");
                else sp.set("state", s.id);
                setSearch(sp);
              }}
              style={pillStyle(s.id === state)}
            >
              {s.label}
            </button>
          ))}
        </div>
        <TextField
          name="author"
          value={author}
          onChange={(v) => {
            const sp = new URLSearchParams(search);
            if (v) sp.set("author", v);
            else sp.delete("author");
            setSearch(sp);
          }}
        >
          <Label>作者</Label>
          <Input placeholder="username 或 UUID" />
          <Description>留空显示所有作者。</Description>
        </TextField>
      </div>

      <ErrorBanner error={error} />

      {items === null ? (
        <Loading />
      ) : items.length === 0 ? (
        <EmptyState
          title="没有匹配的 MR"
          description="尝试切换状态过滤，或新建一个 MR。"
        />
      ) : (
        <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
          {items.map((mr) => (
            <li
              key={mr.id}
              style={{
                padding: "0.75rem 1rem",
                borderBottom: "1px solid var(--separator)",
                background: "var(--surface)",
              }}
            >
              <div style={{ display: "flex", alignItems: "baseline", gap: "0.75rem" }}>
                <StateBadge state={mr.state} />
                <Link
                  to={`${base}/${mr.number}`}
                  style={{ color: "var(--foreground)", textDecoration: "none", fontWeight: 600, flex: 1 }}
                >
                  #{mr.number} · {mr.title}
                </Link>
                <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                  {mr.comment_count} 评论 · {mr.review_count} 评审
                </span>
              </div>
              <div style={{ marginTop: "0.35rem", display: "flex", alignItems: "center", gap: "0.5rem", color: "var(--muted)", fontSize: "0.8rem" }}>
                <UserAvatar user={mr.author} size={18} />
                <span>{mr.author.username}</span>
                <span>
                  <code>{shortRef(mr.source_ref)}</code> → <code>{shortRef(mr.target_ref)}</code>
                </span>
                <span>
                  <RelativeTime iso={mr.updated_at} /> 更新
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function shortRef(r: string): string {
  return r.replace(/^refs\/(heads|tags)\//, "");
}

function pillStyle(active: boolean) {
  return {
    border: "1px solid var(--border)",
    background: active ? "var(--accent)" : "var(--surface)",
    color: active ? "var(--accent-foreground)" : "var(--foreground)",
    padding: "0.25rem 0.75rem",
    borderRadius: "999px",
    cursor: "pointer",
    fontSize: "0.85rem",
  } as const;
}

function StateBadge({ state }: { state: MRState }) {
  const map: Record<MRState, { bg: string; fg: string; label: string }> = {
    open: { bg: "var(--success)", fg: "var(--success-foreground)", label: "Open" },
    merged: { bg: "var(--accent)", fg: "var(--accent-foreground)", label: "Merged" },
    closed: { bg: "var(--default)", fg: "var(--default-foreground)", label: "Closed" },
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
        textTransform: "uppercase",
        letterSpacing: "0.05em",
      }}
    >
      {c.label}
    </span>
  );
}
