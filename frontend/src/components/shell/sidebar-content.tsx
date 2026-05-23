/**
 * shell/sidebar-content.tsx — route-aware contextual sidebar.
 *
 * Reads the current pathname and renders the correct nav block:
 *   /                                       → "Your work" global nav
 *   /orgs                                   → orgs list (global)
 *   /orgs/{slug}                            → org context (overview / projects / settings)
 *   /orgs/{slug}/projects/{slug}*           → project context (overview / repos / issues / MRs / wiki / ...)
 *   /settings/*                             → account settings
 *   /admin/*                                → admin
 *
 * Org/project display names come from the `shellStore` if a child layout has
 * populated it; otherwise we fall back to the URL slug.
 */

import { useLocation } from "chen-the-dawnstreak";

import House from "@gravity-ui/icons/House";
import Layers from "@gravity-ui/icons/Layers";
import Folder from "@gravity-ui/icons/Folder";
import Code from "@gravity-ui/icons/Code";
import CircleQuestion from "@gravity-ui/icons/CircleQuestion";
import BookOpen from "@gravity-ui/icons/BookOpen";
import Tag from "@gravity-ui/icons/Tag";
import ChartLine from "@gravity-ui/icons/ChartLine";
import Person from "@gravity-ui/icons/Person";
import Persons from "@gravity-ui/icons/Persons";
import Pin from "@gravity-ui/icons/Pin";
import ShieldCheck from "@gravity-ui/icons/ShieldCheck";
import Lock from "@gravity-ui/icons/Lock";
import At from "@gravity-ui/icons/At";

import { authStore } from "@/auth/store";
import { NavItem, SidebarSection } from "./nav-primitives";
import { useShellContext } from "./sidebar-store";

interface RouteMatch {
  kind: "root" | "orgs" | "org" | "project" | "settings" | "admin";
  orgSlug?: string;
  projectSlug?: string;
}

function safeDecodeURIComponent(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

/** Parse `pathname` into one of the contextual buckets above. */
export function matchRoute(pathname: string): RouteMatch {
  if (pathname.startsWith("/settings")) return { kind: "settings" };
  if (pathname.startsWith("/admin")) return { kind: "admin" };

  // /orgs/{slug}/projects/{slug}...
  const proj = pathname.match(/^\/orgs\/([^/]+)\/projects\/([^/]+)/);
  if (proj) return { kind: "project", orgSlug: safeDecodeURIComponent(proj[1]!), projectSlug: safeDecodeURIComponent(proj[2]!) };

  const org = pathname.match(/^\/orgs\/([^/]+)/);
  if (org) return { kind: "org", orgSlug: safeDecodeURIComponent(org[1]!) };

  if (pathname === "/orgs" || pathname.startsWith("/orgs")) return { kind: "orgs" };
  return { kind: "root" };
}

export function SidebarContent() {
  const { pathname } = useLocation();
  const match = matchRoute(pathname);
  const { user } = authStore.useStore();

  if (match.kind === "settings") return <SettingsSidebar />;
  if (match.kind === "admin") return <AdminSidebar isSuper={!!user?.is_admin} />;
  if (match.kind === "project") {
    return <ProjectSidebar orgSlug={match.orgSlug!} projectSlug={match.projectSlug!} />;
  }
  if (match.kind === "org") return <OrgSidebar orgSlug={match.orgSlug!} />;
  return <RootSidebar isAdmin={!!user?.is_admin} />;
}

/* ------------------------------------------------------------------ Root  */

function RootSidebar({ isAdmin }: { isAdmin: boolean }) {
  return (
    <>
      <SidebarSection label="你的工作">
        <NavItem to="/orgs" icon={Layers} label="组织" exact />
        <NavItem to="/settings/profile" icon={Person} label="个人资料" />
      </SidebarSection>
      <SidebarSection label="账户">
        <NavItem to="/settings/tokens" icon={Pin} label="访问令牌" />
        <NavItem to="/settings/ssh-keys" icon={ShieldCheck} label="SSH 公钥" />
        <NavItem to="/settings/oauth-apps" icon={At} label="OAuth 应用" />
        <NavItem to="/settings/authorized-apps" icon={Lock} label="已授权应用" />
      </SidebarSection>
      {isAdmin ? (
        <SidebarSection label="管理员">
          <NavItem to="/admin/users" icon={Persons} label="用户审批" />
          <NavItem to="/admin/oauth-apps" icon={At} label="OAuth 应用" />
        </SidebarSection>
      ) : null}
    </>
  );
}

/* ------------------------------------------------------------------- Org  */

function OrgSidebar({ orgSlug }: { orgSlug: string }) {
  const ctx = useShellContext();
  const base = `/orgs/${encodeURIComponent(orgSlug)}`;
  const display = ctx.orgDisplayName || orgSlug;
  return (
    <>
      <SidebarSection>
        <div className="px-3 pt-1 pb-2">
          <div className="text-[10px] uppercase tracking-wider text-muted">组织</div>
          <div className="mt-0.5 truncate text-[13px] font-semibold text-fg" title={display}>
            {display}
          </div>
          <div className="text-[11px] text-muted">@{orgSlug}</div>
        </div>
      </SidebarSection>
      <SidebarSection label="导航">
        <NavItem to={base} icon={House} label="概览" exact />
        <NavItem to={`${base}/projects`} icon={Folder} label="项目" />
      </SidebarSection>
      <SidebarSection label="快捷">
        <NavItem to="/orgs" icon={Layers} label="所有组织" />
      </SidebarSection>
    </>
  );
}

/* ----------------------------------------------------------------- Project */

function ProjectSidebar({ orgSlug, projectSlug }: { orgSlug: string; projectSlug: string }) {
  const ctx = useShellContext();
  const base = `/orgs/${encodeURIComponent(orgSlug)}/projects/${encodeURIComponent(projectSlug)}`;
  const display = ctx.projectDisplayName || projectSlug;
  const visBadge =
    ctx.projectVisibility === "public" ? "公开" :
    ctx.projectVisibility === "internal" ? "内部" :
    ctx.projectVisibility === "private" ? "私有" : null;

  return (
    <>
      <SidebarSection>
        <div className="px-3 pt-1 pb-2">
          <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-muted">
            <span className="truncate" title={`${orgSlug} / ${projectSlug}`}>项目</span>
            {visBadge ? (
              <span className="inline-flex h-4 items-center rounded-sm border border-[var(--border)] px-1 text-[9px] tracking-normal normal-case text-muted">
                {visBadge}
              </span>
            ) : null}
          </div>
          <div className="mt-0.5 truncate text-[13px] font-semibold text-fg" title={display}>
            {display}
          </div>
          <div className="truncate text-[11px] text-muted" title={`@${orgSlug}/${projectSlug}`}>
            @{orgSlug}/{projectSlug}
          </div>
        </div>
      </SidebarSection>
      <SidebarSection label="规划">
        <NavItem to={base} icon={House} label="概览" exact />
        <NavItem to={`${base}/issues`} icon={CircleQuestion} label="Issues" />
        <NavItem to={`${base}/labels`} icon={Tag} label="标签" />
      </SidebarSection>
      <SidebarSection label="代码">
        <NavItem to={`${base}/repos`} icon={Code} label="仓库" />
      </SidebarSection>
      <SidebarSection label="协作">
        <NavItem to={`${base}/wiki`} icon={BookOpen} label="Wiki" />
      </SidebarSection>
      <SidebarSection label="洞察">
        <NavItem to={`${base}/insights`} icon={ChartLine} label="Insights" />
      </SidebarSection>
    </>
  );
}

/* ---------------------------------------------------------------- Settings */

function SettingsSidebar() {
  return (
    <>
      <SidebarSection>
        <div className="px-3 pt-1 pb-2">
          <div className="text-[10px] uppercase tracking-wider text-muted">用户设置</div>
        </div>
      </SidebarSection>
      <SidebarSection label="账户">
        <NavItem to="/settings/profile" icon={Person} label="个人资料" />
        <NavItem to="/settings/tokens" icon={Pin} label="访问令牌" />
        <NavItem to="/settings/ssh-keys" icon={ShieldCheck} label="SSH 公钥" />
      </SidebarSection>
      <SidebarSection label="应用">
        <NavItem to="/settings/oauth-apps" icon={At} label="OAuth 应用" />
        <NavItem to="/settings/authorized-apps" icon={Lock} label="已授权应用" />
      </SidebarSection>
    </>
  );
}

/* ------------------------------------------------------------------ Admin */

function AdminSidebar({ isSuper }: { isSuper: boolean }) {
  return (
    <>
      <SidebarSection>
        <div className="px-3 pt-1 pb-2">
          <div className="text-[10px] uppercase tracking-wider text-muted">系统管理</div>
        </div>
      </SidebarSection>
      <SidebarSection label="账号">
        <NavItem to="/admin/users" icon={Persons} label="用户审批" />
      </SidebarSection>
      <SidebarSection label="应用">
        <NavItem to="/admin/oauth-apps" icon={At} label="OAuth 应用" />
      </SidebarSection>
      {!isSuper ? (
        <SidebarSection>
          <div className="px-3 text-[11px] text-muted">需要管理员权限才能查看此处的内容。</div>
        </SidebarSection>
      ) : null}
    </>
  );
}
