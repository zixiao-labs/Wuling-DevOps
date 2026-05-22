import { Button } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useState } from "react";

import { githubOAuth } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { isOAuthConfirmPending } from "@/api/types";
import { ErrorBanner } from "@/components/error-banner";
import { PageContainer } from "@/components/page/primitives";
import { setSession } from "@/auth/store";

/**
 * /oauth/confirm-link — shown by the OAuth callback when an existing local
 * account already uses the email GitHub reported. The pending decision is
 * stashed in a signed cookie set by the API; this page just asks the user
 * which way to resolve the collision.
 */
export default function OAuthConfirmLinkPage() {
  const navigate = useNavigate();
  const [busy, setBusy] = useState<"link" | "new" | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [pending, setPending] = useState<string | null>(null);

  async function decide(action: "link" | "new") {
    setError(null);
    setBusy(action);
    try {
      const res = await githubOAuth.confirm(action);
      if (isOAuthConfirmPending(res)) {
        setPending("账号已记录，但需要管理员审核才能登录。");
        return;
      }
      setSession(res.access_token, res.user);
      navigate("/orgs", { replace: true });
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusy(null);
    }
  }

  return (
    <PageContainer>
      <div className="mx-auto max-w-[560px]">
        {pending ? (
          <Panel title="账号待审核">
            <p className="text-[13px] text-fg">{pending}</p>
          </Panel>
        ) : (
          <Panel
            title="关联到已有账号？"
            description="我们发现 GitHub 上的邮箱已经在武陵 DevOps 注册过本地账号。请选择："
          >
            <ul className="mb-3 mt-0 list-disc space-y-1 pl-5 text-[12.5px] text-muted">
              <li>
                <span className="font-semibold text-fg">关联</span>：把本次的 GitHub
                身份合并到已有账号上，下次可以直接用 GitHub 登录。
              </li>
              <li>
                <span className="font-semibold text-fg">创建新账号</span>：忽略邮箱冲突，
                新建一个独立账号（用户名会自动添加后缀）。
              </li>
            </ul>
            <ErrorBanner error={error} />
            <div className="mt-4 flex flex-wrap gap-2">
              <Button onPress={() => decide("link")} isDisabled={busy !== null}>
                {busy === "link" ? "关联中…" : "关联到已有账号"}
              </Button>
              <Button variant="outline" onPress={() => decide("new")} isDisabled={busy !== null}>
                {busy === "new" ? "创建中…" : "创建新账号"}
              </Button>
            </div>
          </Panel>
        )}
      </div>
    </PageContainer>
  );
}

function Panel({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-md border border-[var(--border)] bg-[var(--surface)] p-5 shadow-sm">
      <h1 className="m-0 text-[18px] font-semibold text-fg">{title}</h1>
      {description ? <p className="mt-1 text-[12.5px] text-muted">{description}</p> : null}
      <div className="mt-4">{children}</div>
    </section>
  );
}
