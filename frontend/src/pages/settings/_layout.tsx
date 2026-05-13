import { NavLink, Outlet } from "chen-the-dawnstreak";

import { RequireAuth } from "@/auth/guards";

const tabs = [
  { to: "/settings/profile", label: "个人资料" },
  { to: "/settings/tokens", label: "访问令牌" },
  { to: "/settings/ssh-keys", label: "SSH 公钥" },
];

export default function SettingsLayout() {
  return (
    <RequireAuth>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "200px 1fr",
          gap: "1.5rem",
        }}
      >
        <aside>
          <h2
            style={{
              fontSize: "0.7rem",
              textTransform: "uppercase",
              letterSpacing: "0.05em",
              color: "var(--muted)",
              marginBottom: "0.75rem",
            }}
          >
            账号设置
          </h2>
          <nav style={{ display: "flex", flexDirection: "column", gap: "0.15rem" }}>
            {tabs.map((t) => (
              <NavLink
                key={t.to}
                to={t.to}
                style={({ isActive }) => ({
                  padding: "0.4rem 0.6rem",
                  borderRadius: "var(--radius)",
                  textDecoration: "none",
                  color: isActive ? "var(--accent-foreground)" : "var(--foreground)",
                  background: isActive ? "var(--accent)" : "transparent",
                  fontSize: "0.9rem",
                })}
              >
                {t.label}
              </NavLink>
            ))}
          </nav>
        </aside>
        <section>
          <Outlet />
        </section>
      </div>
    </RequireAuth>
  );
}
