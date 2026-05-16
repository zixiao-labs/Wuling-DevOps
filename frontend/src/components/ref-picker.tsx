import BranchesIcon from "@gravity-ui/icons/BranchesRight";
import { useEffect, useState } from "react";

import { ApiError } from "@/api/errors";
import { repos as reposApi } from "@/api/endpoints";
import type { GitRef } from "@/api/types";

export function RefPicker({
  org,
  project,
  repo,
  value,
  onChange,
  defaultBranch,
}: {
  org: string;
  project: string;
  repo: string;
  value: string;
  onChange: (ref: string) => void;
  defaultBranch?: string;
}) {
  const [refs, setRefs] = useState<GitRef[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    let cancelled = false;
    reposApi
      .refs(org, project, repo)
      .then((r) => {
        if (!cancelled) setRefs(r);
      })
      .catch((e) => {
        if (!cancelled) setError(e as ApiError);
      });
    return () => {
      cancelled = true;
    };
  }, [org, project, repo]);

  if (error) {
    return <span style={{ color: "var(--danger)", fontSize: "0.85rem" }}>refs 加载失败</span>;
  }
  if (!refs) {
    return (
      <select disabled style={{ padding: "0.25rem 0.5rem" }}>
        <option>加载中…</option>
      </select>
    );
  }

  const branches = refs.filter((r) => r.is_branch);
  const tags = refs.filter((r) => r.is_tag);

  return (
    <label
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: "0.4rem",
        background: "var(--field-background)",
        border: "1px solid var(--border)",
        borderRadius: "var(--field-radius)",
        padding: "0.2rem 0.5rem",
      }}
    >
      <BranchesIcon width={16} height={16} />
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          border: "none",
          background: "transparent",
          color: "var(--field-foreground)",
          outline: "none",
          minWidth: "8rem",
        }}
      >
        {branches.length > 0 ? (
          <optgroup label="branches">
            {branches.map((r) => (
              <option key={r.name} value={shortName(r.name)}>
                {shortName(r.name)}
                {defaultBranch && shortName(r.name) === defaultBranch ? " (default)" : ""}
              </option>
            ))}
          </optgroup>
        ) : null}
        {tags.length > 0 ? (
          <optgroup label="tags">
            {tags.map((r) => (
              <option key={r.name} value={shortName(r.name)}>
                {shortName(r.name)}
              </option>
            ))}
          </optgroup>
        ) : null}
      </select>
    </label>
  );
}

function shortName(fullRef: string): string {
  return fullRef.replace(/^refs\/(heads|tags)\//, "");
}
