import { Route, Routes } from "chen-the-dawnstreak";
import { DocShell } from "./components/doc-shell";
import { DocPage } from "./components/doc-page";
import { SearchResults } from "./components/search-results";
import { NotFound } from "./components/not-found";

export function HelpRoutes() {
  return (
    <Routes>
      <Route path="/help" element={<DocShell />}>
        <Route index element={<DocPage slug="" />} />
        <Route path="search" element={<SearchResults />} />
        <Route path="*" element={<DocPage />} />
      </Route>
      <Route path="*" element={<NotFound />} />
    </Routes>
  );
}
