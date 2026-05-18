import { Button, Card } from "@heroui/react";
import { Navigate } from "chen-the-dawnstreak";
import { useCallback, useEffect, useState } from "react";

import { admin } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { User, UserApprovalStatus, PatchUserRequest } from "@/api/types";
import { RequireAuth } from "@/auth/guards";
import { authStore } from "@/auth/store";
import { ErrorBanner } from "@/components/error-banner";
import { RelativeTime } from "@/components/relative-time";

/**
 * /admin/users — admin-only user management.
 *
 * Approve or reject pending sign-ups, promote/demote admins, and disable
 * compromised accounts. The page is gated client-side (only renders for
 * is_admin=true users) and again server-side; bypassing the client gate
 * still hits a 403 from /api/v1/admin/*.
 */
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
  if (!user.is_admin) {
    // Bounce non-admins out — the server enforces this too, but failing
    // fast in the SPA keeps the URL bar honest.
    return <Navigate to="/orgs" replace />;
  }

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
    <div style={{ maxWidth: 980, margin: "1rem auto" }}>
      <Card>
        <Card.Header>
          <Card.Title>用户管理</Card.Title>
          <Card.Description>
            审核新注册、提升或撤销管理员权限、禁用账号。仅管理员可见。
          </Card.Description>
        </Card.Header>
        <Card.Content>
          <div style={{ display: "flex", gap: "0.4rem", marginBottom: "1rem" }}>
            {STATUSES.map((s) => (
              <Button
                key={s.value || "all"}
                size="sm"
                variant={filter === s.value ? "primary" : "outline"}
                onPress={() => setFilter(s.value)}
              >
                {s.label}
              </Button>
            ))}
            <span style={{ flex: 1 }} />
            <Button size="sm" variant="outline" onPress={refresh} isDisabled={loading}>
              {loading ? "刷新中…" : "刷新"}
            </Button>
          </div>
          <ErrorBanner error={error} />
          <table style={{ width: "100%", borderCollapse: "collapse", fontSize: "0.9rem" }}>
            <thead>
              <tr style={{ textAlign: "left", color: "var(--muted)", fontSize: "0.75rem" }}>
                <th style={th}>用户</th>
                <th style={th}>邮箱</th>
                <th style={th}>状态</th>
                <th style={th}>角色</th>
                <th style={th}>注册时间</th>
                <th style={th}>操作</th>
              </tr>
            </thead>
            <tbody>
              {users.length === 0 && !loading ? (
                <tr>
                  <td colSpan={6} style={{ ...td, color: "var(--muted)", textAlign: "center" }}>
                    没有匹配的用户。
                  </td>
                </tr>
              ) : null}
              {users.map((u) => {
                const isSelf = u.id === user.id;
                const busy = busyID === u.id;
                return (
                  <tr key={u.id} style={{ borderTop: "1px solid var(--border)" }}>
                    <td style={td}>
                      <div style={{ display: "flex", flexDirection: "column" }}>
                        <strong>{u.username}</strong>
                        <span style={{ color: "var(--muted)", fontSize: "0.8rem" }}>
                          {u.display_name}
                          {u.github_login ? ` · GitHub: ${u.github_login}` : ""}
                        </span>
                      </div>
                    </td>
                    <td style={td}>{u.email}</td>
                    <td style={td}>
                      <StatusChip status={u.approval_status} />
                      {!u.is_active ? (
                        <span style={{ marginLeft: "0.4rem", color: "var(--danger,#c0392b)" }}>已停用</span>
                      ) : null}
                    </td>
                    <td style={td}>{u.is_admin ? "管理员" : "成员"}</td>
                    <td style={td}>
                      <RelativeTime iso={u.created_at} />
                    </td>
                    <td style={{ ...td, whiteSpace: "nowrap" }}>
                      {u.approval_status === "pending" && (
                        <>
                          <Button
                            size="sm"
                            onPress={() => patch(u, { approval_status: "approved" })}
                            isDisabled={busy}
                          >
                            批准
                          </Button>{" "}
                          <Button
                            size="sm"
                            variant="outline"
                            onPress={() => patch(u, { approval_status: "rejected" })}
                            isDisabled={busy}
                          >
                            拒绝
                          </Button>
                        </>
                      )}
                      {u.approval_status === "approved" && !isSelf && (
                        <>
                          <Button
                            size="sm"
                            variant="outline"
                            onPress={() => patch(u, { is_admin: !u.is_admin })}
                            isDisabled={busy}
                          >
                            {u.is_admin ? "取消管理员" : "设为管理员"}
                          </Button>{" "}
                          <Button
                            size="sm"
                            variant="outline"
                            onPress={() => patch(u, { is_active: !u.is_active })}
                            isDisabled={busy}
                          >
                            {u.is_active ? "停用" : "启用"}
                          </Button>
                        </>
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
                      {isSelf && <span style={{ color: "var(--muted)" }}>（当前账号）</span>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </Card.Content>
      </Card>
    </div>
  );
}

function StatusChip({ status }: { status: UserApprovalStatus }) {
  const styles: Record<UserApprovalStatus, React.CSSProperties> = {
    pending: { background: "#fff8dd", color: "#7a5d00", border: "1px solid #f1d97a" },
    approved: { background: "#e6f6ea", color: "#1f6d2f", border: "1px solid #9cd6ab" },
    rejected: { background: "#fceaea", color: "#a52a2a", border: "1px solid #ed9a9a" },
  };
  const labels: Record<UserApprovalStatus, string> = {
    pending: "待审核",
    approved: "已批准",
    rejected: "已拒绝",
  };
  return (
    <span
      style={{
        ...styles[status],
        padding: "0.05rem 0.4rem",
        borderRadius: "999px",
        fontSize: "0.75rem",
      }}
    >
      {labels[status]}
    </span>
  );
}

const th: React.CSSProperties = { padding: "0.4rem 0.5rem", fontWeight: 500 };
const td: React.CSSProperties = { padding: "0.55rem 0.5rem", verticalAlign: "top" };
