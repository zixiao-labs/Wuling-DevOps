import { useEffect, useState } from "react";

import { orgMembers, orgInvitations } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import { DataList, ListRow } from "@/components/page/data-list";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { Pill } from "@/components/page/badges";
import { useOrgCtx } from "@/auth/org-context";
import { authStore } from "@/auth/store";
import type {
  CreateInvitationRequest,
  InvitableRole,
  ListMembersResponse,
  OrgInvitation,
  OrgMember,
  OrgRole,
} from "@/api/types";

const ALL_ROLES: { value: OrgRole; label: string; tone: "info" | "neutral" | "success" | "warning" }[] = [
  { value: "owner",      label: "Owner",      tone: "info" },
  { value: "maintainer", label: "Maintainer", tone: "success" },
  { value: "developer",  label: "Developer",  tone: "neutral" },
  { value: "reporter",   label: "Reporter",   tone: "neutral" },
  { value: "guest",      label: "Guest",      tone: "warning" },
];

const INVITABLE: InvitableRole[] = ["maintainer", "developer", "reporter", "guest"];

const RANK: Record<OrgRole, number> = {
  owner: 50,
  maintainer: 40,
  developer: 30,
  reporter: 20,
  guest: 10,
};

function canAssign(actor: OrgRole, target: OrgRole): boolean {
  if (target === "owner") return actor === "owner";
  if (RANK[actor] < RANK.maintainer) return false;
  return RANK[actor] > RANK[target];
}

function roleLabel(r: OrgRole | undefined | null): string {
  return ALL_ROLES.find((x) => x.value === r)?.label ?? String(r ?? "");
}

export default function OrgMembersPage() {
  const org = useOrgCtx();
  const me = authStore.useStore().user;
  const [data, setData] = useState<ListMembersResponse | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  // Invitations
  const [invites, setInvites] = useState<OrgInvitation[]>([]);
  const [identifier, setIdentifier] = useState("");
  const [inviteRole, setInviteRole] = useState<InvitableRole>("developer");
  const [creating, setCreating] = useState(false);
  const [createdLink, setCreatedLink] = useState<string | null>(null);
  const [inviteErr, setInviteErr] = useState<string | null>(null);

  function load() {
    setError(null);
    orgMembers.list(org.slug).then(setData).catch((e) => setError(e as ApiError));
  }
  function loadInvites() {
    orgInvitations.list(org.slug, "pending").then(setInvites).catch(() => {
      // Member-rank users will hit 403 here; that's OK, they don't see the panel.
      setInvites([]);
    });
  }

  useEffect(() => {
    load();
    loadInvites();
  }, [org.slug]);

  const myRole: OrgRole | "" = data?.role ?? "";
  const canManage = myRole === "owner" || myRole === "maintainer";

  async function onRoleChange(m: OrgMember, next: OrgRole) {
    setBusy(m.user_id);
    try {
      await orgMembers.setRole(org.slug, m.user_id, { role: next });
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusy(null);
    }
  }

  async function onRemove(m: OrgMember) {
    const isSelf = me?.id === m.user_id;
    const msg = isSelf
      ? `确认离开组织 ${org.display_name || org.slug} 吗？`
      : `确认将 @${m.username} 从组织中移除？`;
    if (!window.confirm(msg)) return;
    setBusy(m.user_id);
    try {
      await orgMembers.remove(org.slug, m.user_id);
      if (isSelf) {
        window.location.assign("/orgs");
        return;
      }
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusy(null);
    }
  }

  async function onInvite(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setInviteErr(null);
    setCreatedLink(null);
    const body: CreateInvitationRequest = {
      identifier: identifier.trim(),
      role: inviteRole,
    };
    if (!body.identifier) return;
    setCreating(true);
    try {
      const resp = await orgInvitations.create(org.slug, body);
      const fullURL = new URL(resp.url, window.location.origin).toString();
      setCreatedLink(fullURL);
      setIdentifier("");
      loadInvites();
    } catch (err) {
      const ae = err as ApiError;
      setInviteErr(ae.message ?? "邀请失败");
    } finally {
      setCreating(false);
    }
  }

  async function onRevoke(inv: OrgInvitation) {
    if (!window.confirm(`撤销邀请？(${inv.invitee_email || inv.invitee_user_id})`)) return;
    try {
      await orgInvitations.revoke(org.slug, inv.id);
      loadInvites();
    } catch (err) {
      setInviteErr((err as ApiError).message ?? "撤销失败");
    }
  }

  if (!data) {
    if (error) {
      return (
        <PageContainer>
          <ErrorBanner error={error} />
        </PageContainer>
      );
    }
    return <Loading />;
  }

  return (
    <PageContainer>
      <PageHeader
        title="组织成员"
        description={`你在该组织的角色是 ${roleLabel(myRole as OrgRole)}。`}
        eyebrow={`组织 · @${org.slug}`}
      />

      {error ? <ErrorBanner error={error} /> : null}

      {canManage ? (
        <Surface className="mb-4">
          <SurfaceHeader title="邀请新成员" description="按用户名或邮箱邀请。Magic-link 复制给对方后，对方登录并点击即可加入。" />
          <SurfaceBody>
            <form onSubmit={onInvite} className="flex flex-col gap-3 sm:flex-row sm:items-end">
              <div className="flex-1">
                <label className="mb-1 block text-[12px] font-medium text-fg">用户名或邮箱</label>
                <input
                  type="text"
                  value={identifier}
                  onChange={(e) => setIdentifier(e.target.value)}
                  placeholder="@alice 或 alice@example.com"
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-[13px] text-fg outline-none focus:border-[var(--accent)]"
                  required
                />
              </div>
              <div>
                <label className="mb-1 block text-[12px] font-medium text-fg">角色</label>
                <select
                  value={inviteRole}
                  onChange={(e) => setInviteRole(e.target.value as InvitableRole)}
                  className="rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 py-1.5 text-[13px] text-fg"
                >
                  {INVITABLE.filter((r) => canAssign(myRole as OrgRole, r)).map((r) => (
                    <option key={r} value={r}>{roleLabel(r)}</option>
                  ))}
                </select>
              </div>
              <button
                type="submit"
                disabled={creating || !identifier.trim()}
                className="rounded-md bg-[var(--accent)] px-3 py-1.5 text-[13px] font-medium text-[var(--accent-foreground)] transition-colors hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {creating ? "生成中…" : "生成邀请链接"}
              </button>
            </form>
            {inviteErr ? <div className="mt-2 text-[12.5px] text-[color:var(--danger,#c53030)]">{inviteErr}</div> : null}
            {createdLink ? (
              <div className="mt-3 rounded-md border border-[var(--accent)]/40 bg-[var(--surface-secondary)] p-3 text-[12.5px]">
                <div className="mb-1 font-medium text-fg">邀请链接已生成，发给对方即可：</div>
                <div className="flex items-center gap-2">
                  <code className="flex-1 overflow-x-auto whitespace-nowrap rounded bg-[var(--surface)] px-2 py-1 font-mono text-[12px] text-fg">
                    {createdLink}
                  </code>
                  <button
                    type="button"
                    onClick={() => navigator.clipboard.writeText(createdLink)}
                    className="rounded border border-[var(--border)] px-2 py-1 text-[12px] text-fg hover:bg-[var(--surface)]"
                  >
                    复制
                  </button>
                </div>
              </div>
            ) : null}
          </SurfaceBody>

          {invites.length > 0 ? (
            <SurfaceBody noPad>
              <div className="border-t border-[var(--separator)] px-4 py-2 text-[12px] font-medium uppercase tracking-wider text-muted">
                待接受邀请 ({invites.length})
              </div>
              <DataList>
                {invites.map((inv) => (
                  <ListRow
                    key={inv.id}
                    title={
                      <span className="font-mono text-[13px] text-fg">
                        {inv.invitee_email || inv.invitee_user_id || "（未知）"}
                      </span>
                    }
                    subtitle={
                      <span className="text-[12px] text-muted">
                        <Pill tone="neutral">{roleLabel(inv.role)}</Pill>
                        <span className="ml-2">过期 <RelativeTime iso={inv.expires_at} /></span>
                      </span>
                    }
                    meta={
                      <button
                        type="button"
                        onClick={() => onRevoke(inv)}
                        className="text-[12.5px] text-muted hover:text-[color:var(--danger,#c53030)]"
                      >
                        撤销
                      </button>
                    }
                  />
                ))}
              </DataList>
            </SurfaceBody>
          ) : null}
        </Surface>
      ) : null}

      <Surface>
        <SurfaceHeader title={`成员 (${data.members.length})`} />
        <SurfaceBody noPad>
          <DataList>
            {data.members.map((m) => {
              const isSelf = me?.id === m.user_id;
              const canChange = canManage && !isSelf && canAssign(myRole as OrgRole, m.role);
              const canRemove = isSelf || (canManage && RANK[myRole as OrgRole] > RANK[m.role]);
              return (
                <ListRow
                  key={m.user_id}
                  icon={<UserAvatar user={m} size={28} />}
                  title={
                    <span>
                      {m.display_name || m.username}
                      <span className="ml-2 text-[12px] text-muted">@{m.username}</span>
                      {isSelf ? <span className="ml-2 text-[11px] text-muted">(你)</span> : null}
                    </span>
                  }
                  subtitle={<span className="text-[12px] text-muted">加入 <RelativeTime iso={m.joined_at} /></span>}
                  meta={
                    <div className="flex items-center gap-2">
                      {canChange ? (
                        <select
                          value={m.role}
                          disabled={busy === m.user_id}
                          onChange={(e) => onRoleChange(m, e.target.value as OrgRole)}
                          className="rounded border border-[var(--border)] bg-[var(--surface)] px-2 py-1 text-[12px] text-fg"
                        >
                          {ALL_ROLES
                            .filter((r) => r.value === m.role || canAssign(myRole as OrgRole, r.value))
                            .map((r) => (
                              <option key={r.value} value={r.value}>{r.label}</option>
                            ))}
                        </select>
                      ) : (
                        <Pill tone={ALL_ROLES.find((r) => r.value === m.role)?.tone ?? "neutral"}>
                          {roleLabel(m.role)}
                        </Pill>
                      )}
                      {canRemove ? (
                        <button
                          type="button"
                          onClick={() => onRemove(m)}
                          disabled={busy === m.user_id}
                          className="text-[12px] text-muted hover:text-[color:var(--danger,#c53030)]"
                        >
                          {isSelf ? "离开" : "移除"}
                        </button>
                      ) : null}
                    </div>
                  }
                />
              );
            })}
          </DataList>
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
