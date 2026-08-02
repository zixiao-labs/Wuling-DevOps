export function SearchBox() {
  return (
    <form action="/help/search" method="get" role="search" className="relative">
      <label htmlFor="help-search" className="sr-only">
        搜索帮助文档
      </label>
      <input
        id="help-search"
        name="q"
        type="search"
        placeholder="搜索文档… ( / 或 ⌘K )"
        autoComplete="off"
        className="w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-fg placeholder:text-fg/40"
      />
      <ul
        id="help-search-results"
        className="absolute z-10 mt-1 hidden w-full rounded-md border border-[var(--border)] bg-[var(--surface)] shadow-lg"
        role="listbox"
      />
    </form>
  );
}
