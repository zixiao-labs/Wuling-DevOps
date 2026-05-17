// Restore theme + mode before paint to avoid flash.
// Kept as an external file (not inline) so CSP `script-src 'self'` doesn't
// need 'unsafe-inline' or a content hash to allow it.
(function () {
  try {
    var t = localStorage.getItem("wuling.theme") || "clean";
    var m = localStorage.getItem("wuling.mode") || "light";
    document.documentElement.setAttribute("data-theme", t);
    if (m === "dark") document.documentElement.classList.add("dark");
  } catch (e) {}
})();
