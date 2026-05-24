import { useEffect, useState } from "react";

import { auth } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
} from "@/components/page/primitives";
import { Pill } from "@/components/page/badges";
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
        if (ac.signal.aborted) return;
        setError(e as ApiError);
      })
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, []);

  if (!me && loading) return <Loading />;
  if (error) {
    return (
      <PageContainer>
        <ErrorBanner error={error} />
      </PageContainer>
    );
  }
  if (!me) return null;

  return (
    <PageContainer>
      <PageHeader
        title="个人资料"
        description="由服务端 /api/v1/auth/me 提供。修改字段尚未在 Stage 1 实现。"
      />
      <Surface>
        <SurfaceBody>
          <div className="mb-5 flex items-center gap-4">
            <UserAvatar user={me} size={64} />
            <div>
              <div className="text-[18px] font-semibold text-fg">{me.display_name || me.username}</div>
              <div className="text-[12.5px] text-muted">@{me.username}</div>
            </div>
          </div>
          <dl className="grid grid-cols-1 sm:grid-cols-[120px_1fr] sm:gap-y-0 gap-y-1 text-[13px]">
            <Field label="邮箱" value={me.email} />
            <Field label="ID" value={me.id} mono />
            <Field
              label="角色"
              value={
                <span className="inline-flex flex-wrap items-center gap-1.5">
                  {me.is_admin ? <Pill tone="info">管理员</Pill> : <Pill tone="neutral">普通用户</Pill>}
                  {me.is_active ? null : <Pill tone="warning">已停用</Pill>}
                </span>
              }
            />
            <Field label="注册时间" value={<RelativeTime iso={me.created_at} />} />
          </dl>
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}

function Field({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <>
      <dt className="border-b border-[var(--separator)] py-2 text-[11.5px] uppercase tracking-wider text-muted sm:py-2.5">
        {label}
      </dt>
      <dd
        className={[
          "border-b border-[var(--separator)] py-2 sm:py-2.5 text-fg",
          mono ? "font-mono text-[12.5px]" : "",
        ].join(" ")}
      >
        {value}
      </dd>
    </>
  );
}
