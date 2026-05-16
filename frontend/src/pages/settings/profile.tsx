import { Card } from "@heroui/react";
import { useEffect, useState } from "react";

import { auth } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import { authStore, setUser } from "@/auth/store";
import type { User } from "@/api/types";

export default function ProfilePage() {
  const { user } = authStore.useStore();
  const [me, setMe] = useState<User | null>(user);
  const [error, setError] = useState<ApiError | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    setLoading(true);
    auth
      .me(ac.signal)
      .then((u) => {
        setMe(u);
        setUser(u);
      })
      .catch((e) => {
        // Surface every real error — including network — so the user can see
        // why their profile failed to load instead of getting a stuck spinner.
        // Aborts (unmount / navigation) come through as "network" too, so
        // gate them with the controller's signal.
        if (ac.signal.aborted) return;
        setError(e as ApiError);
      })
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, []);

  if (!me && loading) return <Loading />;
  if (error) return <ErrorBanner error={error} />;
  if (!me) return null;

  return (
    <Card>
      <Card.Header>
        <Card.Title>个人资料</Card.Title>
        <Card.Description>由服务端 /api/v1/auth/me 提供。修改字段尚未在 Stage 1 实现。</Card.Description>
      </Card.Header>
      <Card.Content>
        <div style={{ display: "flex", alignItems: "center", gap: "1rem", marginBottom: "1rem" }}>
          <UserAvatar user={me} size={56} />
          <div>
            <div style={{ fontSize: "1.1rem", fontWeight: 600 }}>
              {me.display_name || me.username}
            </div>
            <div style={{ color: "var(--muted)" }}>@{me.username}</div>
          </div>
        </div>
        <Field label="邮箱" value={me.email} />
        <Field label="ID" value={me.id} mono />
        <Field
          label="角色"
          value={
            <>
              {me.is_admin ? <Pill>管理员</Pill> : <Pill>普通用户</Pill>}
              {me.is_active ? null : <Pill tone="warning">已停用</Pill>}
            </>
          }
        />
        <Field label="注册时间" value={<RelativeTime iso={me.created_at} />} />
      </Card.Content>
    </Card>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "8rem 1fr",
        padding: "0.5rem 0",
        borderBottom: "1px solid var(--separator)",
        gap: "1rem",
      }}
    >
      <div style={{ color: "var(--muted)", fontSize: "0.85rem" }}>{label}</div>
      <div style={{ fontFamily: mono ? "ui-monospace, monospace" : undefined }}>{value}</div>
    </div>
  );
}

function Pill({ children, tone = "default" }: { children: React.ReactNode; tone?: "default" | "warning" }) {
  return (
    <span
      style={{
        display: "inline-block",
        padding: "0.1rem 0.5rem",
        borderRadius: "999px",
        background:
          tone === "warning"
            ? "color-mix(in oklab, var(--warning) 20%, var(--surface))"
            : "var(--surface-secondary)",
        color: tone === "warning" ? "var(--warning-foreground)" : "var(--foreground)",
        fontSize: "0.75rem",
        marginRight: "0.25rem",
      }}
    >
      {children}
    </span>
  );
}
