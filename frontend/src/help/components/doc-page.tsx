import { lookupDoc, pathnameToSlug } from "../lookup";
import { useLocation } from "chen-the-dawnstreak";

export function DocPage({ slug: slugProp }: { slug?: string }) {
  const { pathname } = useLocation();
  const slug = slugProp ?? pathnameToSlug(pathname);
  const doc = lookupDoc(pathname) ?? lookupDoc(slug === "" ? "/help" : `/help/${slug}`);

  if (!doc) {
    return (
      <article className="wuling-prose">
        <h1>页面不存在</h1>
        <p>
          找不到该文档。请从<a href="/help">帮助中心首页</a>浏览可用内容。
        </p>
      </article>
    );
  }

  return (
    <article className="wuling-prose">
      <header className="mb-6 border-b border-[var(--border)] pb-4">
        <p className="mb-1 text-sm text-fg/50">{doc.group}</p>
        <h1>{doc.title}</h1>
        {doc.description ? <p className="text-fg/70">{doc.description}</p> : null}
        <time className="text-xs text-fg/40" dateTime={doc.updatedAt}>
          更新于 {new Date(doc.updatedAt).toLocaleDateString("zh-CN")}
        </time>
      </header>
      <div dangerouslySetInnerHTML={{ __html: doc.html }} />
    </article>
  );
}
