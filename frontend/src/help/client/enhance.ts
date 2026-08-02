/**
 * Progressive enhancement for /help — no imports, runs as type=module.
 * Scoring logic mirrors src/help/search.ts; keep in sync manually.
 */
(function () {
  const input = document.getElementById("help-search");
  const resultsEl = document.getElementById("help-search-results");
  if (!input || !resultsEl) return;

  /** @type {Array<{slug:string,title:string,group:string,description:string,text:string}>|null} */
  let index = null;
  let activeIdx = -1;

  function scoreQuery(entry, q) {
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

  function slugToHref(slug) {
    return slug === "" ? "/help" : "/help/" + slug;
  }

  async function ensureIndex() {
    if (index) return index;
    const script = document.querySelector('script[type="module"][src*="enhance"]');
    const v = script?.getAttribute("src")?.match(/[?&]v=([^&]+)/)?.[1] ?? "";
    const res = await fetch("/help/_assets/search-index.json?v=" + v);
    index = await res.json();
    return index;
  }

  function renderResults(q) {
    if (!index || !q.trim()) {
      resultsEl.classList.add("hidden");
      resultsEl.innerHTML = "";
      activeIdx = -1;
      return;
    }
    const hits = index
      .map((entry) => ({ entry, score: scoreQuery(entry, q) }))
      .filter((r) => r.score > 0)
      .sort((a, b) => b.score - a.score)
      .slice(0, 8);

    if (hits.length === 0) {
      resultsEl.classList.add("hidden");
      return;
    }

    resultsEl.innerHTML = hits
      .map(
        ({ entry }, i) =>
          `<li role="option" data-idx="${i}" class="cursor-pointer px-3 py-2 hover:bg-[var(--surface-secondary)]">` +
          `<a href="${slugToHref(entry.slug)}" class="block text-sm font-medium text-fg no-underline">${entry.title}</a>` +
          `<span class="text-xs text-fg/50">${entry.group}</span></li>`,
      )
      .join("");
    resultsEl.classList.remove("hidden");
    activeIdx = -1;
  }

  input.addEventListener("input", async () => {
    await ensureIndex();
    renderResults(input.value);
  });

  input.addEventListener("keydown", async (e) => {
    const items = resultsEl.querySelectorAll("[data-idx]");
    if (e.key === "ArrowDown") {
      e.preventDefault();
      activeIdx = Math.min(activeIdx + 1, items.length - 1);
      items.forEach((el, i) => el.classList.toggle("bg-[var(--surface-secondary)]", i === activeIdx));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      activeIdx = Math.max(activeIdx - 1, 0);
      items.forEach((el, i) => el.classList.toggle("bg-[var(--surface-secondary)]", i === activeIdx));
    } else if (e.key === "Enter" && activeIdx >= 0 && items[activeIdx]) {
      e.preventDefault();
      const link = items[activeIdx].querySelector("a");
      if (link) window.location.href = link.href;
    } else if (e.key === "Escape") {
      resultsEl.classList.add("hidden");
      activeIdx = -1;
    }
  });

  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      input.focus();
    } else if (e.key === "/" && document.activeElement !== input) {
      const tag = document.activeElement?.tagName;
      if (tag !== "INPUT" && tag !== "TEXTAREA") {
        e.preventDefault();
        input.focus();
      }
    }
  });

  document.addEventListener("click", (e) => {
    if (!input.contains(e.target) && !resultsEl.contains(e.target)) {
      resultsEl.classList.add("hidden");
    }
  });

  const tocLinks = document.querySelectorAll("[data-toc-id]");
  if (tocLinks.length && "IntersectionObserver" in window) {
    const byId = new Map();
    tocLinks.forEach((a) => {
      const id = a.getAttribute("data-toc-id");
      const el = id ? document.getElementById(id) : null;
      if (el) byId.set(el, a);
    });
    const obs = new IntersectionObserver(
      (entries) => {
        for (const ent of entries) {
          if (ent.isIntersecting) {
            tocLinks.forEach((a) => a.classList.remove("text-accent", "font-medium"));
            const link = byId.get(ent.target);
            link?.classList.add("text-accent", "font-medium");
          }
        }
      },
      { rootMargin: "-20% 0px -70% 0px", threshold: 0 },
    );
    byId.forEach((_, el) => obs.observe(el));
  }
})();
