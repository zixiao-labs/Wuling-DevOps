/**
 * Org landing (`/orgs/{slug}`).
 *
 * The canonical projects list lives at `./projects` (`/orgs/{slug}/projects`),
 * matching the sidebar "项目" nav item and the `projects/[project_slug]` route.
 * The org landing mirrors it so visiting an org drops you straight on its
 * projects — and so the sidebar "概览" item resolves to a real page rather than
 * an empty `<Outlet />`. When the planned GitLab-style overview lands, replace
 * the line below with a dedicated overview component.
 */
export { default } from "./projects";
