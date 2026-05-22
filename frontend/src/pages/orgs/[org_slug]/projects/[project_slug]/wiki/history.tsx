import { useEffect, useState } from "react";

import { wiki as wikiApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
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
    <PageContainer wide>
      <PageHeader title="Wiki 历史" description="按时间倒序展示最近的 wiki 提交。" />
      <ErrorBanner error={error} />
      <Surface>
        <SurfaceBody noPad>
          {commits === null ? (
            <Loading />
          ) : commits.length === 0 ? (
            <div className="px-4 py-6 text-[13px] text-muted">（无提交）</div>
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {commits.map((c) => (
                <li key={c.oid} className="flex items-baseline gap-3 px-4 py-2.5 text-[12.5px]">
                  <code className="font-mono text-[11px] text-muted">{c.oid.slice(0, 8)}</code>
                  <span className="min-w-0 flex-1 truncate font-medium text-fg">
                    {c.message.split("\n")[0]}
                  </span>
                  <span className="shrink-0 text-[11.5px] text-muted">
                    {c.author.name} · <RelativeTime iso={c.author.when} />
                  </span>
                </li>
              ))}
            </ul>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
