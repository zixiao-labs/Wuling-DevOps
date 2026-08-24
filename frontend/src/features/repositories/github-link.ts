export interface GitHubRepository {
  owner: string;
  name: string;
}

/**
 * Accept the repository identifiers users commonly copy from GitHub: a web
 * URL, an SSH clone URL, or the compact owner/name form.
 */
export function parseGitHubRepository(value: string): GitHubRepository | null {
  let candidate = value.trim();
  if (!candidate) return null;

  if (candidate.startsWith("git@github.com:")) {
    candidate = candidate.slice("git@github.com:".length);
  } else if (/^https?:\/\//i.test(candidate)) {
    try {
      const url = new URL(candidate);
      if (url.hostname.toLowerCase() !== "github.com") return null;
      candidate = url.pathname;
    } catch {
      return null;
    }
  }

  candidate = candidate.replace(/^\/+|\/+$/g, "").replace(/\.git$/i, "");
  const parts = candidate.split("/");
  const owner = parts[0];
  const name = parts[1];
  if (parts.length !== 2 || !owner?.trim() || !name?.trim()) return null;

  return { owner, name };
}
