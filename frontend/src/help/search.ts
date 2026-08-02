import type { HelpSearchEntry } from "./help-types";

/** Shared scoring for /help/search SSR and client enhance.ts (keep in sync). */
export function scoreQuery(entry: HelpSearchEntry, q: string): number {
  const query = q.trim().toLowerCase();
  if (!query) return 0;
  const terms = query.split(/\s+/).filter(Boolean);
  let score = 0;
  const title = entry.title.toLowerCase();
  const desc = entry.description.toLowerCase();
  const text = entry.text.toLowerCase();
  for (const term of terms) {
    if (title.includes(term)) score += 10;
    if (desc.includes(term)) score += 5;
    if (text.includes(term)) score += 1;
  }
  if (title === query) score += 20;
  return score;
}

export function searchDocs(entries: HelpSearchEntry[], q: string, limit = 20) {
  return entries
    .map((entry) => ({ entry, score: scoreQuery(entry, q) }))
    .filter((r) => r.score > 0)
    .sort((a, b) => b.score - a.score)
    .slice(0, limit)
    .map((r) => r.entry);
}
