import { Button, Description, Input, Label, TextField } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import { Link, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { issues as issuesApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { LabelChip } from "@/components/label-chip";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Issue, IssueState } from "@/api/types";

const STATES: Array<{ id: IssueState | "all"; label: string }> = [
  { id: "open", label: "Open" },
  { id: "closed", label: "Closed" },
  { id: "all", label: "全部" },
];

export default function IssuesIndex() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [search, setSearch] = useSearchParams();

  const state = (search.get("state") ?? "open") as IssueState | "all";
  const label = search.get("label") ?? "";
  const assignee = search.get("assignee") ?? "";
  const author = search.get("author") ?? "";
  const q = search.get("search") ?? "";

  const [items, setItems] = useState<Issue[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setItems(null);
    setError(null);
    issuesApi
      .list(org.slug, project.slug, {
        state: state === "all" ? undefined : state,
        label: label || undefined,
        assignee: assignee || undefined,
        author: author || undefined,
        search: q || undefined,
        limit: 100,
      })
      .then(setItems)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, state, label, assignee, author, q]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/issues`;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <header style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <h1 style={{ margin: 0, fontSize: "1.5rem" }}>Issues</h1>
        <Link to={`${base}/new`}>
          <Button>
            <PlusIcon width={16} height={16} /> 新建
          </Button>
        </Link>
      </header>

      <div style={{ display: "flex", gap: "1rem", alignItems: "end", flexWrap: "wrap" }}>
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
        <FilterField
          label="搜索"
          value={q}
          onChange={(v) => updateParam(search, setSearch, "search", v)}
          placeholder="标题/正文…"
        />
        <FilterField
          label="标签"
          value={label}
          onChange={(v) => updateParam(search, setSearch, "label", v)}
        />
        <FilterField
          label="作者"
          value={author}
          onChange={(v) => updateParam(search, setSearch, "author", v)}
        />
        <FilterField
          label="指派"
          value={assignee}
          onChange={(v) => updateParam(search, setSearch, "assignee", v)}
        />
      </div>

      <ErrorBanner error={error} />

      {items === null ? (
        <Loading />
      ) : items.length === 0 ? (
        <EmptyState title="没有匹配的 issue" description="试着换个过滤条件，或新建一个。" />
      ) : (
        <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
          {items.map((it) => (
            <li
              key={it.id}
              style={{
                padding: "0.75rem 1rem",
                borderBottom: "1px solid var(--separator)",
                background: "var(--surface)",
              }}
            >
              <div style={{ display: "flex", alignItems: "baseline", gap: "0.75rem" }}>
                <StateBadge state={it.state} />
                <Link
                  to={`${base}/${it.number}`}
                  style={{ color: "var(--foreground)", textDecoration: "none", fontWeight: 600, flex: 1 }}
                >
                  #{it.number} · {it.title}
                </Link>
                <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                  {it.comment_count} 评论
                </span>
              </div>
              <div style={{ marginTop: "0.35rem", display: "flex", alignItems: "center", gap: "0.5rem", color: "var(--muted)", fontSize: "0.8rem", flexWrap: "wrap" }}>
                <UserAvatar user={it.author} size={18} />
                <span>{it.author.username}</span>
                <span>
                  <RelativeTime iso={it.updated_at} /> 更新
                </span>
                {it.labels.map((l) => (
                  <LabelChip key={l.id} label={l} />
                ))}
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function FilterField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <TextField name={label} value={value} onChange={onChange}>
      <Label>{label}</Label>
      <Input placeholder={placeholder} style={{ minWidth: "10rem" }} />
      <Description>留空忽略此过滤。</Description>
    </TextField>
  );
}

function updateParam(
  cur: URLSearchParams,
  setter: (p: URLSearchParams) => void,
  key: string,
  value: string,
) {
  const sp = new URLSearchParams(cur);
  if (value) sp.set(key, value);
  else sp.delete(key);
  setter(sp);
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

function StateBadge({ state }: { state: IssueState }) {
  const map: Record<IssueState, { bg: string; fg: string; label: string }> = {
    open: { bg: "var(--success)", fg: "var(--success-foreground)", label: "Open" },
    closed: { bg: "var(--accent)", fg: "var(--accent-foreground)", label: "Closed" },
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
      }}
    >
      {c.label}
    </span>
  );
}
