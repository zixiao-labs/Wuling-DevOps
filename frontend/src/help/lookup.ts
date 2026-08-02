import data from "virtual:help-docs";

export function pathnameToSlug(pathname: string): string {
  const p = pathname.replace(/\/+$/, "");
  if (p === "/help") return "";
  if (p.startsWith("/help/")) return p.slice("/help/".length);
  return "";
}

export function slugToHref(slug: string): string {
  if (slug === "") return "/help";
  return `/help/${slug}`;
}

export function lookupDoc(pathname: string) {
  const slug = pathnameToSlug(pathname);
  if (pathname.startsWith("/help/search")) return undefined;
  return data.docs.find((d) => d.slug === slug);
}

export { data as helpData };
