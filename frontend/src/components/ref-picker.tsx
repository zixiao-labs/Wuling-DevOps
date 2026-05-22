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
    return <span className="text-[12px] text-[var(--danger)]">refs 加载失败</span>;
  }
  if (!refs) {
    return (
      <select
        disabled
        className="h-7 rounded-sm border border-[var(--border)] bg-[var(--surface-secondary)] px-2 text-[12px] text-muted"
      >
        <option>加载中…</option>
      </select>
    );
  }

  const branches = refs.filter((r) => r.is_branch);
  const tags = refs.filter((r) => r.is_tag);

  return (
    <label className="inline-flex h-7 items-center gap-1.5 rounded-sm border border-[var(--border)] bg-[var(--field-background)] px-2 text-[12px]">
      <BranchesIcon width={13} height={13} className="opacity-70" />
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="min-w-[8rem] border-none bg-transparent text-[var(--field-foreground)] outline-none"
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
