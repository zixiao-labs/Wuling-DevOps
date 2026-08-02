import { useSearchParams } from "chen-the-dawnstreak";
import searchIndex from "virtual:help-search-index";
import { searchDocs } from "../search";
import { slugToHref } from "../lookup";

export function SearchResults() {
  const [params] = useSearchParams();
  const q = params.get("q") ?? "";
  const results = searchDocs(searchIndex, q);

  return (
    <article className="wuling-prose">
      <h1>搜索结果</h1>
      {q.trim() === "" ? (
        <p>请输入搜索关键词。</p>
      ) : results.length === 0 ? (
        <p>
          未找到与「{q}」相关的文档。请尝试其他关键词或浏览<a href="/help">帮助中心首页</a>。
        </p>
      ) : (
        <>
          <p className="text-fg/70">
            找到 {results.length} 条与「{q}」相关的结果
          </p>
          <ul className="not-prose space-y-4 pl-0">
            {results.map((r) => (
              <li key={r.slug || "index"} className="list-none rounded border border-[var(--border)] p-4">
                <a href={slugToHref(r.slug)} className="text-lg font-semibold text-accent no-underline">
                  {r.title}
                </a>
                <p className="mt-1 text-sm text-fg/50">{r.group}</p>
                {r.description ? <p className="mt-2 text-sm text-fg/70">{r.description}</p> : null}
              </li>
            ))}
          </ul>
        </>
      )}
    </article>
  );
}
