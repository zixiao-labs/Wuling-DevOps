import { useParams, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RefPicker } from "@/components/ref-picker";
import { RelativeTime } from "@/components/relative-time";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { Commit } from "@/api/types";

export default function CommitsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const [search, setSearch] = useSearchParams();

  const repoSlug = params.repo_slug ?? "";
  const ref = search.get("ref") ?? "";

  const [commits, setCommits] = useState<Commit[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setCommits(null);
    setError(null);
    reposApi
      .commits(org.slug, project.slug, repoSlug, { ref: ref || undefined, limit: 100 })
      .then(setCommits)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, repoSlug, ref]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <h1 style={{ margin: 0, fontSize: "1.5rem" }}>提交</h1>
        <RefPicker
          org={org.slug}
          project={project.slug}
          repo={repoSlug}
          value={ref}
          onChange={(next) => {
            const sp = new URLSearchParams(search);
            if (next) sp.set("ref", next);
            else sp.delete("ref");
            setSearch(sp);
          }}
        />
      </div>

      <ErrorBanner error={error} />

      {commits === null ? (
        <Loading />
      ) : commits.length === 0 ? (
        <div style={{ color: "var(--muted)" }}>（无提交）</div>
      ) : (
        <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
          {commits.map((c) => (
            <li
              key={c.oid}
              style={{
                padding: "0.75rem",
                borderBottom: "1px solid var(--separator)",
                background: "var(--surface)",
              }}
            >
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "baseline" }}>
                <code style={{ color: "var(--muted)" }}>{c.oid.slice(0, 8)}</code>
                <strong style={{ flex: 1 }}>{firstLine(c.message)}</strong>
                <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                  {c.author.name} · <RelativeTime iso={c.author.when} />
                </span>
              </div>
              {restOfMessage(c.message) ? (
                <pre
                  style={{
                    margin: "0.5rem 0 0 5rem",
                    fontSize: "0.8rem",
                    color: "var(--muted)",
                    whiteSpace: "pre-wrap",
                    fontFamily: "ui-monospace, monospace",
                  }}
                >
                  {restOfMessage(c.message)}
                </pre>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function firstLine(s: string): string {
  const i = s.indexOf("\n");
  return i === -1 ? s : s.slice(0, i);
}
function restOfMessage(s: string): string {
  const i = s.indexOf("\n");
  return i === -1 ? "" : s.slice(i + 1).trim();
}
