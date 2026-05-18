import { Link, Outlet, useNavigate } from "chen-the-dawnstreak";
import { Button } from "@heroui/react";

import { authStore, clearSession } from "@/auth/store";
import { ThemeSwitcher } from "@/components/theme-switcher";
import { UserAvatar } from "@/components/user-avatar";

export default function RootLayout() {
  const { token, user } = authStore.useStore();
  const navigate = useNavigate();

  function logout() {
    clearSession();
    navigate("/login", { replace: true });
  }

  return (
    <div style={{ minHeight: "100%", display: "flex", flexDirection: "column" }}>
      <header
        style={{
          display: "flex",
          alignItems: "center",
          gap: "1rem",
          padding: "0.6rem 1rem",
          borderBottom: "1px solid var(--border)",
          background: "var(--surface)",
          position: "sticky",
          top: 0,
          zIndex: 10,
        }}
      >
        <Link
          to="/"
          style={{
            display: "inline-flex",
            alignItems: "baseline",
            gap: "0.4rem",
            color: "var(--foreground)",
            textDecoration: "none",
            fontWeight: 700,
          }}
        >
          <span style={{ fontSize: "1.1rem" }}>武陵 DevOps</span>
          <span style={{ fontSize: "0.7rem", color: "var(--muted)" }}>Stage 1</span>
        </Link>

        <nav style={{ display: "flex", gap: "0.75rem", marginLeft: "1rem" }}>
          {token ? (
            <Link to="/orgs" style={navLinkStyle}>
              组织
            </Link>
          ) : null}
          {token && user?.is_admin ? (
            <Link to="/admin/users" style={navLinkStyle}>
              管理
            </Link>
          ) : null}
        </nav>

        <div style={{ marginLeft: "auto", display: "flex", alignItems: "center", gap: "0.75rem" }}>
          <ThemeSwitcher />
          {token && user ? (
            <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
              <Link
                to="/settings/profile"
                style={{
                  display: "inline-flex",
                  alignItems: "center",
                  gap: "0.4rem",
                  color: "var(--foreground)",
                  textDecoration: "none",
                }}
              >
                <UserAvatar user={user} />
                <span style={{ fontSize: "0.85rem" }}>{user.display_name || user.username}</span>
              </Link>
              <Button size="sm" variant="outline" onPress={logout}>
                退出
              </Button>
            </div>
          ) : (
            <>
              <Link to="/login" style={navLinkStyle}>
                登录
              </Link>
              <Link to="/register" style={navLinkStyle}>
                注册
              </Link>
            </>
          )}
        </div>
      </header>

      <main style={{ flex: 1, padding: "1.25rem", maxWidth: 1280, margin: "0 auto", width: "100%" }}>
        <Outlet />
      </main>

      <footer
        style={{
          padding: "1rem",
          textAlign: "center",
          fontSize: "0.75rem",
          color: "var(--muted)",
          borderTop: "1px solid var(--border)",
        }}
      >
        武陵 DevOps · 紫霄实验室
      </footer>
    </div>
  );
}

const navLinkStyle = {
  color: "var(--foreground)",
  textDecoration: "none",
  fontSize: "0.9rem",
  padding: "0.2rem 0.4rem",
  borderRadius: "0.25rem",
} as const;
