import { Button } from "@heroui/react";
import { Navigate } from "chen-the-dawnstreak";
import { useCallback, useEffect, useState } from "react";

import { admin } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { User, UserApprovalStatus, PatchUserRequest } from "@/api/types";
import { RequireAuth } from "@/auth/guards";
import { authStore } from "@/auth/store";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { Pill } from "@/components/page/badges";

export default function AdminUsersPage() {
  return (
    <RequireAuth>
      <AdminUsers />
    </RequireAuth>
  );
}

const STATUSES: { label: string; value: UserApprovalStatus | "" }[] = [
  { label: "全部", value: "" },
  { label: "待审核", value: "pending" },
  { label: "已批准", value: "approved" },
  { label: "已拒绝", value: "rejected" },
];

function AdminUsers() {
  const { user } = authStore.useStore();
  const [filter, setFilter] = useState<UserApprovalStatus | "">("pending");
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [busyID, setBusyID] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await admin.users.list(filter || undefined);
      setUsers(list);
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setLoading(false);
    }
  }, [filter]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!user) return null;
  if (!user.is_admin) return <Navigate to="/orgs" replace />;

  async function patch(target: User, body: PatchUserRequest) {
    setBusyID(target.id);
    setError(null);
    try {
      await admin.users.patch(target.id, body);
      await refresh();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusyID(null);
    }
  }

  return (
    <PageContainer wide>
      <PageHeader
        title="用户管理"
        description="审核新注册、提升或撤销管理员权限、禁用账号。仅管理员可见。"
      />

      <Surface className="mb-3">
        <SurfaceBody>
          <div className="flex flex-wrap items-center gap-2">
            <div className="inline-flex h-7 items-center overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)]">
              {STATUSES.map((s, i) => {
                const active = filter === s.value;
                return (
                  <button
                    key={s.value || "all"}
                    onClick={() => setFilter(s.value)}
                    className={[
                      "h-full px-3 text-[12px]",
                      i > 0 ? "border-l border-[var(--border)]" : "",
                      active
                        ? "bg-[var(--surface-secondary)] font-medium text-fg"
                        : "text-fg/70 hover:bg-[var(--surface-secondary)] hover:text-fg",
                    ].join(" ")}
                  >
                    {s.label}
                  </button>
                );
              })}
            </div>
            <span className="flex-1" />
            <Button size="sm" variant="outline" onPress={refresh} isDisabled={loading}>
              {loading ? "刷新中…" : "刷新"}
            </Button>
          </div>
        </SurfaceBody>
      </Surface>

      <ErrorBanner error={error} />

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">用户 · {users.length}</span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {loading && users.length === 0 ? (
            <Loading />
          ) : users.length === 0 ? (
            <EmptyState inset title="没有匹配的用户" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-[13px]">
                <thead>
                  <tr className="border-b border-[var(--separator)] bg-[var(--surface-secondary)]/40 text-left">
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">用户</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">邮箱</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">状态</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">角色</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">注册</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((u) => {
                    const isSelf = u.id === user.id;
                    const busy = busyID === u.id;
                    return (
                      <tr key={u.id} className="border-b border-[var(--separator)] last:border-0 align-top">
                        <td className="px-4 py-2.5">
                          <div className="flex items-center gap-2">
                            <UserAvatar user={u} size={24} />
                            <div className="flex min-w-0 flex-col">
                              <span className="truncate text-[13px] font-medium text-fg">{u.username}</span>
                              <span className="truncate text-[11.5px] text-muted">
                                {u.display_name}
                                {u.github_login ? ` · GitHub: ${u.github_login}` : ""}
                              </span>
                            </div>
                          </div>
                        </td>
                        <td className="px-4 py-2.5 text-[12.5px] text-fg">{u.email}</td>
                        <td className="px-4 py-2.5">
                          <span className="inline-flex flex-wrap items-center gap-1">
                            <StatusPill status={u.approval_status} />
                            {!u.is_active ? <Pill tone="danger">已停用</Pill> : null}
                          </span>
                        </td>
                        <td className="px-4 py-2.5">
                          {u.is_admin ? <Pill tone="info">管理员</Pill> : <span className="text-muted">成员</span>}
                        </td>
                        <td className="px-4 py-2.5 text-[11.5px] text-muted">
                          <RelativeTime iso={u.created_at} />
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5">
                          {u.approval_status === "pending" && (
                            <span className="inline-flex gap-1.5">
                              <Button size="sm" onPress={() => patch(u, { approval_status: "approved" })} isDisabled={busy}>
                                批准
                              </Button>
                              <Button
                                size="sm"
                                variant="outline"
                                onPress={() => patch(u, { approval_status: "rejected" })}
                                isDisabled={busy}
                              >
                                拒绝
                              </Button>
                            </span>
                          )}
                          {u.approval_status === "approved" && !isSelf && (
                            <span className="inline-flex flex-wrap gap-1.5">
                              <Button
                                size="sm"
                                variant="outline"
                                onPress={() => patch(u, { is_admin: !u.is_admin })}
                                isDisabled={busy}
                              >
                                {u.is_admin ? "取消管理员" : "设为管理员"}
                              </Button>
                              <Button
                                size="sm"
                                variant="outline"
                                onPress={() => patch(u, { is_active: !u.is_active })}
                                isDisabled={busy}
                              >
                                {u.is_active ? "停用" : "启用"}
                              </Button>
                            </span>
                          )}
                          {u.approval_status === "rejected" && (
                            <Button
                              size="sm"
                              variant="outline"
                              onPress={() => patch(u, { approval_status: "approved" })}
                              isDisabled={busy}
                            >
                              重新批准
                            </Button>
                          )}
                          {isSelf && <span className="text-[11.5px] text-muted">（当前账号）</span>}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}

function StatusPill({ status }: { status: UserApprovalStatus }) {
  if (status === "pending") return <Pill tone="warning">待审核</Pill>;
  if (status === "approved") return <Pill tone="success">已批准</Pill>;
  return <Pill tone="danger">已拒绝</Pill>;
}
