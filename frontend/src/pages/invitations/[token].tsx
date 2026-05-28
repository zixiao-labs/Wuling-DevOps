import { useEffect, useState } from "react";
import { useNavigate, useParams, Link } from "chen-the-dawnstreak";

import { orgInvitations } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
import { Pill } from "@/components/page/badges";
import { RequireAuth } from "@/auth/guards";
import { authStore } from "@/auth/store";
import type { OrgInvitation } from "@/api/types";

export default function InvitationAcceptPage() {
  return (
    <RequireAuth>
      <AcceptInner />
    </RequireAuth>
  );
}

function AcceptInner() {
  const { token = "" } = useParams<{ token: string }>();
  const navigate = useNavigate();
  const me = authStore.useStore().user;

  const [inv, setInv] = useState<OrgInvitation | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [accepting, setAccepting] = useState(false);
  const [accepted, setAccepted] = useState<OrgInvitation | null>(null);

  useEffect(() => {
    if (!token) return;
    orgInvitations.preview(token).then(setInv).catch((e) => setError(e as ApiError));
  }, [token]);

  async function onAccept() {
    setAccepting(true);
    try {
      const result = await orgInvitations.accept(token);
      setAccepted(result);
      // Navigate after a short pause so the user sees the "accepted" state.
      setTimeout(() => {
        navigate(`/orgs/${result.org_slug ?? ""}`);
      }, 800);
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setAccepting(false);
    }
  }

  if (!token) {
    return (
      <PageContainer>
        <PageHeader title="邀请链接无效" />
      </PageContainer>
    );
  }
  if (error) {
    return (
      <PageContainer>
        <PageHeader title="无法接受邀请" />
        <Surface>
          <SurfaceBody>
            <div className="text-[13px] text-muted">{error.message}</div>
            <div className="mt-3">
              <Link to="/orgs" className="text-[13px] text-[var(--accent)] hover:underline">
                返回我的组织
              </Link>
            </div>
          </SurfaceBody>
        </Surface>
      </PageContainer>
    );
  }
  if (!inv) return <Loading />;

  const expired = inv.status !== "pending";
  const mismatch =
    me &&
    inv.invitee_user_id &&
    inv.invitee_user_id !== me.id;
  const emailMismatch =
    me &&
    inv.invitee_email &&
    !inv.invitee_user_id &&
    me.email?.toLowerCase() !== inv.invitee_email.toLowerCase();

  return (
    <PageContainer>
      <PageHeader title="组织邀请" />
      <Surface>
        <SurfaceBody>
          <div className="mb-4">
            <div className="text-[12px] uppercase tracking-wider text-muted">受邀加入</div>
            <div className="text-[20px] font-semibold text-fg">
              {inv.org_display_name || inv.org_slug}
              {inv.org_slug ? <span className="ml-2 text-[13px] text-muted">@{inv.org_slug}</span> : null}
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-2 text-[13px] text-muted">
              <span>角色：</span>
              <Pill tone="info">{inv.role}</Pill>
              {inv.inviter ? (
                <span>· 邀请人 @{inv.inviter.username}</span>
              ) : null}
              <span>· 过期 <RelativeTime iso={inv.expires_at} /></span>
            </div>
          </div>

          {accepted ? (
            <div className="rounded-md border border-[var(--accent)]/40 bg-[var(--surface-secondary)] p-3 text-[13px]">
              已加入。正在跳转到组织页…
            </div>
          ) : expired ? (
            <div className="rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] p-3 text-[13px] text-muted">
              此邀请已 {inv.status === "accepted" ? "被接受" : inv.status === "revoked" ? "撤销" : "过期"}。
            </div>
          ) : mismatch || emailMismatch ? (
            <div className="rounded-md border border-[color:var(--warning,#d4a017)]/40 bg-[var(--surface-secondary)] p-3 text-[13px] text-muted">
              此邀请发给的是
              {inv.invitee_email ? ` ${inv.invitee_email}` : " 另一位用户"}
              ，与你当前登录的账户不匹配。请使用对应账户登录后再点击链接。
            </div>
          ) : (
            <div className="flex gap-2">
              <button
                type="button"
                onClick={onAccept}
                disabled={accepting}
                className="rounded-md bg-[var(--accent)] px-3 py-1.5 text-[13px] font-medium text-[var(--accent-foreground)] transition-colors hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {accepting ? "处理中…" : "接受邀请并加入组织"}
              </button>
              <Link
                to="/orgs"
                className="rounded-md border border-[var(--border)] px-3 py-1.5 text-[13px] text-fg hover:bg-[var(--surface-secondary)]"
              >
                忽略
              </Link>
            </div>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
