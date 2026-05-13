import FolderIcon from "@gravity-ui/icons/Folder";
import FileIcon from "@gravity-ui/icons/File";
import { Link, useParams, useSearchParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { repos as reposApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RefPicker } from "@/components/ref-picker";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { TreeResponse } from "@/api/types";

export default function TreePage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const [search, setSearch] = useSearchParams();

  const repoSlug = params.repo_slug ?? "";
  // The "..." catch-all parameter is exposed as a string by react-router.
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
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <div style={{ display: "flex", gap: "0.75rem", alignItems: "center" }}>
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
        <Breadcrumb base={`${base}/tree`} ref={ref} path={path} />
      </div>

      <ErrorBanner error={error} />

      {!data ? (
        <Loading />
      ) : data.entries.length === 0 ? (
        <div style={{ color: "var(--muted)", padding: "1rem" }}>（空目录）</div>
      ) : (
        <ul
          style={{
            listStyle: "none",
            padding: 0,
            margin: 0,
            border: "1px solid var(--border)",
            borderRadius: "var(--radius)",
            overflow: "hidden",
          }}
        >
          {sortEntries(data.entries).map((e) => {
            const isTree = e.kind === "tree";
            const childPath = path ? `${path}/${e.name}` : e.name;
            const linkTo = isTree
              ? `${base}/tree/${childPath}${ref ? `?ref=${encodeURIComponent(ref)}` : ""}`
              : `${base}/blob/${childPath}${ref ? `?ref=${encodeURIComponent(ref)}` : ""}`;
            return (
              <li
                key={e.oid + e.name}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "0.5rem",
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid var(--separator)",
                  background: "var(--surface)",
                }}
              >
                {isTree ? <FolderIcon width={16} height={16} /> : <FileIcon width={16} height={16} />}
                <Link to={linkTo} style={{ color: "var(--accent)", textDecoration: "none" }}>
                  {e.name}
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function sortEntries<T extends { kind: string; name: string }>(es: T[]): T[] {
  return [...es].sort((a, b) => {
    if (a.kind === "tree" && b.kind !== "tree") return -1;
    if (a.kind !== "tree" && b.kind === "tree") return 1;
    return a.name.localeCompare(b.name);
  });
}

function Breadcrumb({ base, ref, path }: { base: string; ref: string; path: string }) {
  const parts = path.split("/").filter(Boolean);
  const refSuffix = ref ? `?ref=${encodeURIComponent(ref)}` : "";
  return (
    <nav style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: "0.25rem" }}>
      <Link to={`${base}${refSuffix}`} style={crumbLink}>
        /
      </Link>
      {parts.map((p, i) => {
        const sub = parts.slice(0, i + 1).join("/");
        const isLast = i === parts.length - 1;
        return (
          <span key={i} style={{ display: "inline-flex", alignItems: "center", gap: "0.25rem" }}>
            <Link to={`${base}/${sub}${refSuffix}`} style={isLast ? crumbCurrent : crumbLink}>
              {p}
            </Link>
            {isLast ? null : <span style={{ color: "var(--muted)" }}>/</span>}
          </span>
        );
      })}
    </nav>
  );
}

const crumbLink = { color: "var(--accent)", textDecoration: "none" } as const;
const crumbCurrent = { color: "var(--foreground)", fontWeight: 600 } as const;
