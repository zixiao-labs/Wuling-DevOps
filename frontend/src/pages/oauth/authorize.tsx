import { Avatar, Button } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { AuthorizePreview, OAuthScope } from "@/api/types";
import { RequireAuth } from "@/auth/guards";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { PageContainer } from "@/components/page/primitives";
import { Pill } from "@/components/page/badges";

/**
 * /oauth/authorize — the consent screen that a third-party app sends the
 * user's browser to.
 */
export default function OAuthAuthorizePage() {
  return (
    <RequireAuth>
      <AuthorizeInner />
    </RequireAuth>
  );
}

function AuthorizeInner() {
  const navigate = useNavigate();
  const [preview, setPreview] = useState<AuthorizePreview | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [submitting, setSubmitting] = useState<"allow" | "deny" | null>(null);
  const [redirectURL, setRedirectURL] = useState<string | null>(null);

  const reqId =
    typeof window !== "undefined"
      ? new URLSearchParams(window.location.search).get("req")
      : null;

  useEffect(() => {
    if (!reqId) {
      setError(new ApiError(400, "bad_request", "缺少 req 参数（请重新启动授权流程）。"));
      return;
    }
    let cancelled = false;
    oauthProvider
      .authorizePreview(reqId)
      .then((p) => {
        if (!cancelled) setPreview(p);
      })
      .catch((e) => {
        if (!cancelled) setError(e as ApiError);
      });
    return () => {
      cancelled = true;
    };
  }, [reqId]);

  async function decide(decision: "allow" | "deny") {
    if (!reqId) return;
    setSubmitting(decision);
    setError(null);
    try {
      const res = await oauthProvider.authorizeDecision(reqId, decision);
      setRedirectURL(res.redirect_url);
      window.location.assign(res.redirect_url);
    } catch (e) {
      setError(e as ApiError);
      setSubmitting(null);
    }
  }

  if (!preview && !error) {
    return (
      <PageContainer>
        <div className="mx-auto max-w-[560px]">
          <Loading />
        </div>
      </PageContainer>
    );
  }

  if (error) {
    return (
      <PageContainer>
        <div className="mx-auto max-w-[560px] rounded-md border border-[var(--border)] bg-[var(--surface)] p-5 shadow-sm">
          <h1 className="m-0 text-[18px] font-semibold text-fg">无法继续授权</h1>
          <div className="mt-4">
            <ErrorBanner error={error} />
            <Button variant="outline" onPress={() => navigate("/", { replace: true })}>
              返回首页
            </Button>
          </div>
        </div>
      </PageContainer>
    );
  }
  if (!preview) return null;

  return (
    <PageContainer>
      <div className="mx-auto max-w-[560px] rounded-md border border-[var(--border)] bg-[var(--surface)] shadow-sm">
        <header className="flex items-center gap-3 border-b border-[var(--separator)] px-5 py-4">
          <Avatar size="lg">
            {preview.client.logo_url && (
              <Avatar.Image alt={preview.client.name} src={preview.client.logo_url} />
            )}
            <Avatar.Fallback>{initialsOf(preview.client.name)}</Avatar.Fallback>
          </Avatar>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h1 className="m-0 truncate text-[16px] font-semibold text-fg">
                {preview.client.name}
              </h1>
              {preview.client.is_first_party && (
                <Pill tone="info">官方</Pill>
              )}
            </div>
            {preview.client.homepage_url && (
              <a
                href={preview.client.homepage_url}
                target="_blank"
                rel="noopener noreferrer"
                className="block truncate text-[12px] text-muted hover:text-[var(--accent)] hover:underline"
              >
                {preview.client.homepage_url}
              </a>
            )}
          </div>
        </header>
        <div className="px-5 py-4">
          {preview.client.description && (
            <p className="mb-4 text-[13px] text-fg">{preview.client.description}</p>
          )}
          <div className="text-[12px] font-medium uppercase tracking-wider text-muted">
            申请的权限
          </div>
          <ul className="mt-2 list-none space-y-0 divide-y divide-[var(--separator)] rounded-md border border-[var(--border)] bg-[var(--surface-secondary)]/40 p-0">
            {preview.scopes_requested.map((s) => (
              <li key={s} className="flex items-start gap-3 px-3 py-2.5">
                <code className="mt-px rounded-sm bg-[var(--surface)] px-1.5 py-0.5 text-[11.5px] text-fg ring-1 ring-inset ring-[var(--border)]">
                  {s}
                </code>
                <span className="flex-1 text-[12.5px] text-muted">{scopeDescription(s)}</span>
              </li>
            ))}
          </ul>
          <p className="mt-4 text-[12px] text-muted">
            授权后可随时在「设置 → 已授权应用」撤销。
          </p>
        </div>
        <footer className="flex justify-end gap-2 border-t border-[var(--separator)] bg-[var(--surface-secondary)]/30 px-5 py-3">
          <Button
            variant="outline"
            isDisabled={submitting !== null || redirectURL !== null}
            onPress={() => decide("deny")}
          >
            {submitting === "deny" ? "处理中…" : "拒绝"}
          </Button>
          <Button
            isDisabled={submitting !== null || redirectURL !== null}
            onPress={() => decide("allow")}
          >
            {submitting === "allow" ? "处理中…" : "允许"}
          </Button>
        </footer>
      </div>
    </PageContainer>
  );
}

function scopeDescription(s: OAuthScope): string {
  switch (s) {
    case "user:read": return "读取你的用户名、头像和基本资料";
    case "user:write": return "修改你的基本资料";
    case "repo:read": return "读取你能访问的仓库元数据";
    case "repo:write": return "在你能访问的仓库内创建分支、推送提交";
    case "issue:read": return "读取 Issue 和评论";
    case "issue:write": return "创建 / 修改 Issue 与评论";
    case "mr:read": return "读取 Merge Request";
    case "mr:write": return "创建 / 修改 Merge Request";
    case "git:read": return "通过 git HTTPS 拉取代码";
    case "git:write": return "通过 git HTTPS 推送代码";
    default: return s;
  }
}

function initialsOf(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "?";
  const parts = trimmed.split(/\s+/);
  if (parts.length >= 2) {
    return (parts[0]!.charAt(0) + parts[1]!.charAt(0)).toUpperCase();
  }
  return trimmed.slice(0, 2).toUpperCase();
}
