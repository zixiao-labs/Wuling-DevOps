import { useEffect, useRef, useState } from "react";

import { auth, avatars } from "@/api/endpoints";
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

const ACCEPT = "image/png,image/jpeg,image/gif";
const MAX_BYTES = 2 * 1024 * 1024;

export default function ProfilePage() {
  const { user } = authStore.useStore();
  const [me, setMe] = useState<User | null>(user);
  const [error, setError] = useState<ApiError | null>(null);
  const [loading, setLoading] = useState(false);

  const fileInput = useRef<HTMLInputElement | null>(null);
  const [uploading, setUploading] = useState(false);
  const [avatarErr, setAvatarErr] = useState<string | null>(null);

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

  async function onPickFile(e: React.ChangeEvent<HTMLInputElement>) {
    setAvatarErr(null);
    const file = e.target.files?.[0];
    e.target.value = ""; // reset so the same file can be re-picked
    if (!file) return;
    if (!ACCEPT.split(",").includes(file.type)) {
      setAvatarErr("仅支持 PNG / JPEG / GIF");
      return;
    }
    if (file.size > MAX_BYTES) {
      setAvatarErr("文件超过 2 MiB");
      return;
    }
    setUploading(true);
    try {
      const resp = await avatars.upload(file);
      if (me) {
        const next = { ...me, avatar_url: resp.avatar_url };
        setMe(next);
        setUser(next);
      }
    } catch (err) {
      const ae = err as ApiError;
      setAvatarErr(ae.message ?? "上传失败");
    } finally {
      setUploading(false);
    }
  }

  async function onRemove() {
    setAvatarErr(null);
    if (!me) return;
    setUploading(true);
    try {
      await avatars.remove();
      const next = { ...me, avatar_url: "" };
      setMe(next);
      setUser(next);
    } catch (err) {
      const ae = err as ApiError;
      setAvatarErr(ae.message ?? "删除失败");
    } finally {
      setUploading(false);
    }
  }

  if (!me && loading) return <Loading />;
  if (error) {
    return (
      <PageContainer>
        <ErrorBanner error={error} />
      </PageContainer>
    );
  }
  if (!me) return null;

  const hasAvatar = !!me.avatar_url;

  return (
    <PageContainer>
      <PageHeader
        title="个人资料"
        description="管理你的显示信息和头像。"
      />
      <Surface>
        <SurfaceBody>
          <div className="mb-5 flex items-start gap-4">
            <UserAvatar user={me} size={96} />
            <div className="flex-1">
              <div className="text-[18px] font-semibold text-fg">{me.display_name || me.username}</div>
              <div className="text-[12.5px] text-muted">@{me.username}</div>
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <input
                  ref={fileInput}
                  type="file"
                  accept={ACCEPT}
                  className="hidden"
                  onChange={onPickFile}
                />
                <button
                  type="button"
                  onClick={() => fileInput.current?.click()}
                  disabled={uploading}
                  className="inline-flex items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-[12.5px] font-medium text-fg transition-colors hover:bg-[var(--surface-secondary)] disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {uploading ? "上传中…" : hasAvatar ? "更换头像" : "上传头像"}
                </button>
                {hasAvatar ? (
                  <button
                    type="button"
                    onClick={onRemove}
                    disabled={uploading}
                    className="inline-flex items-center gap-1.5 rounded-md border border-transparent px-3 py-1.5 text-[12.5px] font-medium text-muted transition-colors hover:text-fg disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    移除
                  </button>
                ) : null}
                <span className="text-[11.5px] text-muted">PNG / JPEG / GIF，最大 2 MiB；服务器会重新压缩为 256×256。</span>
              </div>
              {avatarErr ? (
                <div className="mt-2 text-[12.5px] text-[color:var(--danger,#c53030)]">{avatarErr}</div>
              ) : null}
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
