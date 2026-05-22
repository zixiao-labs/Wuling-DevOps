import FolderIcon from "@gravity-ui/icons/Folder";
import FileIcon from "@gravity-ui/icons/File";
import ArrowChevronRight from "@gravity-ui/icons/ArrowChevronRight";
import { Link, useParams, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RefPicker } from "@/components/ref-picker";
import {
  PageContainer,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { TreeResponse } from "@/api/types";

export default function TreePage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const [search, setSearch] = useSearchParams();

  const repoSlug = params.repo_slug ?? "";
  const path = (params["*"] ?? "") as string;
  const ref = search.get("ref") ?? "";

  const [data, setData] = useState<TreeResponse | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setData(null);
    setError(null);
    reposApi
      .tree(org.slug, project.slug, repoSlug, { ref: ref || undefined, path: path || undefined })
      .then(setData)
      .catch((e) => setError(e as ApiError));
  }, [org.slug, project.slug, repoSlug, ref, path]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repoSlug)}`;

  return (
    <PageContainer wide>
      <Surface className="mb-3">
        <SurfaceHeader dense>
          <span className="inline-flex items-center gap-3">
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
            <TreeBreadcrumb
              base={`${base}/tree`}
              repoBase={base}
              gitRef={ref}
              path={path}
              repoSlug={repoSlug}
            />
          </span>
        </SurfaceHeader>
      </Surface>

      <ErrorBanner error={error} />

      <Surface>
        <SurfaceBody noPad>
          {!data ? (
            <Loading />
          ) : data.entries.length === 0 ? (
            <div className="px-4 py-6 text-[12.5px] text-muted">（空目录）</div>
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {sortEntries(data.entries).map((e) => {
                const isTree = e.kind === "tree";
                const childPath = path ? `${path}/${e.name}` : e.name;
                const linkTo = isTree
                  ? `${base}/tree/${childPath}${ref ? `?ref=${encodeURIComponent(ref)}` : ""}`
                  : `${base}/blob/${childPath}${ref ? `?ref=${encodeURIComponent(ref)}` : ""}`;
                return (
                  <li key={e.oid + e.name}>
                    <Link
                      to={linkTo}
                      className="flex items-center gap-2.5 px-4 py-1.5 text-[13px] text-fg hover:bg-[var(--surface-secondary)]"
                    >
                      <span
                        className={[
                          "shrink-0",
                          isTree ? "text-[var(--accent)]" : "text-fg/60",
                        ].join(" ")}
                      >
                        {isTree ? <FolderIcon width={14} height={14} /> : <FileIcon width={14} height={14} />}
                      </span>
                      <span className="truncate font-mono">{e.name}</span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}

function sortEntries<T extends { kind: string; name: string }>(es: T[]): T[] {
  return [...es].sort((a, b) => {
    if (a.kind === "tree" && b.kind !== "tree") return -1;
    if (a.kind !== "tree" && b.kind === "tree") return 1;
    return a.name.localeCompare(b.name);
  });
}

function TreeBreadcrumb({
  base,
  repoBase,
  gitRef,
  path,
  repoSlug,
}: {
  base: string;
  repoBase: string;
  gitRef: string;
  path: string;
  repoSlug: string;
}) {
  const parts = path.split("/").filter(Boolean);
  const refSuffix = gitRef ? `?ref=${encodeURIComponent(gitRef)}` : "";
  return (
    <nav className="inline-flex flex-wrap items-center gap-0.5 text-[12px]">
      <Link
        to={`${repoBase}`}
        className="font-mono text-[var(--accent)] hover:underline"
      >
        {repoSlug}
      </Link>
      <ArrowChevronRight width={11} height={11} className="opacity-50" />
      <Link
        to={`${base}${refSuffix}`}
        className={parts.length === 0 ? "text-fg font-medium" : "text-[var(--accent)] hover:underline"}
      >
        /
      </Link>
      {parts.map((p, i) => {
        const sub = parts.slice(0, i + 1).join("/");
        const isLast = i === parts.length - 1;
        return (
          <span key={i} className="inline-flex items-center gap-0.5">
            <ArrowChevronRight width={11} height={11} className="opacity-50" />
            <Link
              to={`${base}/${sub}${refSuffix}`}
              className={
                isLast
                  ? "font-mono text-fg font-medium"
                  : "font-mono text-[var(--accent)] hover:underline"
              }
            >
              {p}
            </Link>
          </span>
        );
      })}
    </nav>
  );
}
