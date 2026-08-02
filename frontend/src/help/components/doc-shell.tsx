import { Outlet, useLocation } from "chen-the-dawnstreak";
import data from "virtual:help-docs";
import { SearchBox } from "./search-box";
import { lookupDoc, slugToHref } from "../lookup";

export function DocShell() {
  const { pathname } = useLocation();
  const doc = lookupDoc(pathname);
  const isSearch = pathname.startsWith("/help/search");

  return (
    <div className="flex min-h-screen flex-col bg-bg text-fg">
      <header className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3 md:px-6">
        <div className="flex items-center gap-3">
          <a href="/" className="flex items-center gap-2 text-sm font-semibold text-fg no-underline">
            <img src="/brand-wordmark.png" alt="武陵 DevOps" className="h-6 w-auto" width={120} height={24} />
          </a>
          <span className="hidden text-fg/50 sm:inline">/</span>
          <a href="/help" className="hidden text-sm text-fg/70 no-underline hover:text-fg sm:inline">
            帮助中心
          </a>
        </div>
        <a
          href="/"
          className="rounded-md border border-[var(--border)] px-3 py-1.5 text-sm text-fg/80 no-underline hover:bg-[var(--surface-secondary)]"
        >
          返回控制台
        </a>
      </header>

      <div className="flex flex-1">
        <aside className="hidden w-56 shrink-0 border-r border-[var(--border)] bg-[var(--surface-secondary)] p-4 md:block">
          <SearchBox />
          <nav className="mt-4 space-y-4" aria-label="文档导航">
            {data.nav.map((g) => (
              <div key={g.group}>
                <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-fg/50">{g.group}</p>
                <ul className="space-y-0.5">
                  {g.items.map((item) => {
                    const href = slugToHref(item.slug);
                    const active = pathname === href || pathname === `${href}/`;
                    return (
                      <li key={item.slug || "index"}>
                        <a
                          href={href}
                          className={[
                            "block rounded px-2 py-1 text-sm no-underline",
                            active ? "bg-[var(--surface)] font-medium text-fg" : "text-fg/70 hover:text-fg",
                          ].join(" ")}
                        >
                          {item.title}
                        </a>
                      </li>
                    );
                  })}
                </ul>
              </div>
            ))}
          </nav>
        </aside>

        <main className="min-w-0 flex-1 p-4 md:p-8">
          <details className="mb-4 rounded border border-[var(--border)] p-3 md:hidden">
            <summary className="cursor-pointer text-sm font-medium">菜单与搜索</summary>
            <div className="mt-3">
              <SearchBox />
              <nav className="mt-3 space-y-3" aria-label="文档导航">
                {data.nav.map((g) => (
                  <div key={g.group}>
                    <p className="mb-1 text-xs font-semibold text-fg/50">{g.group}</p>
                    <ul className="space-y-0.5">
                      {g.items.map((item) => (
                        <li key={item.slug || "index"}>
                          <a href={slugToHref(item.slug)} className="text-sm text-accent no-underline">
                            {item.title}
                          </a>
                        </li>
                      ))}
                    </ul>
                  </div>
                ))}
              </nav>
            </div>
          </details>

          <div className="flex gap-8">
            <div className="min-w-0 flex-1">
              <Outlet />
            </div>
            {!isSearch && doc && doc.headings.length > 0 ? (
              <aside className="hidden w-48 shrink-0 xl:block" aria-label="本页目录">
                <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-fg/50">本页目录</p>
                <nav className="space-y-1 border-l border-[var(--border)] pl-3">
                  {doc.headings.map((h) => (
                    <a
                      key={h.id}
                      href={`#${h.id}`}
                      data-toc-id={h.id}
                      className="block text-sm text-fg/60 no-underline hover:text-fg"
                      style={{ paddingLeft: `${(h.level - 2) * 0.5}rem` }}
                    >
                      {h.text}
                    </a>
                  ))}
                </nav>
              </aside>
            ) : null}
          </div>
        </main>
      </div>
    </div>
  );
}
