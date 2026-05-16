import { Card } from "@heroui/react";
import { useEffect, useState } from "react";

import { wiki as wikiApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { WikiHistoryCommit } from "@/api/types";

export default function WikiHistoryPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [commits, setCommits] = useState<WikiHistoryCommit[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    wikiApi
      .history(org.slug, project.slug, 100)
      .then(setCommits)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <h1 style={{ margin: 0, fontSize: "1.5rem" }}>Wiki 历史</h1>
      <Card>
        <Card.Content>
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
                    padding: "0.5rem 0",
                    borderBottom: "1px solid var(--separator)",
                    display: "flex",
                    gap: "0.5rem",
                    alignItems: "baseline",
                    fontSize: "0.9rem",
                  }}
                >
                  <code style={{ color: "var(--muted)" }}>{c.oid.slice(0, 8)}</code>
                  <strong style={{ flex: 1 }}>{c.message.split("\n")[0]}</strong>
                  <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                    {c.author.name} · <RelativeTime iso={c.author.when} />
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}
