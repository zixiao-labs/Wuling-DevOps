import { useNavigate } from "chen-the-dawnstreak";
import { useEffect, useRef, useState } from "react";

import { auth as authApi } from "@/api/endpoints";
import { setSession, setUser } from "@/auth/store";
import { Loading } from "@/components/loading";

/**
 * /oauth/callback — terminal landing page for the GitHub OAuth flow.
 *
 * The backend redirects here with a URL fragment carrying one of:
 *   #access_token=...&expires_at=...
 *   #pending_approval=1&status=pending&username=...
 *   #error=...&error_description=...
 */
export default function OAuthCallbackPage() {
  const navigate = useNavigate();
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "pending"; status: string; username: string }
    | { kind: "error"; code: string; message: string }
  >({ kind: "loading" });

  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return;
    ran.current = true;

    const hash = window.location.hash.startsWith("#")
      ? window.location.hash.slice(1)
      : window.location.hash;
    const params = new URLSearchParams(hash);
    history.replaceState(null, "", window.location.pathname);

    const err = params.get("error");
    if (err) {
      setState({
        kind: "error",
        code: err,
        message: params.get("error_description") ?? "GitHub login failed.",
      });
      return;
    }

    if (params.get("pending_approval")) {
      setState({
        kind: "pending",
        status: params.get("status") ?? "pending",
        username: params.get("username") ?? "",
      });
      return;
    }

    const token = params.get("access_token");
    if (!token) {
      setState({ kind: "error", code: "bad_request", message: "missing access token in callback" });
      return;
    }

    (async () => {
      const placeholder = {
        id: "",
        username: "",
        email: "",
        display_name: "",
        is_admin: false,
        is_active: true,
        approval_status: "approved" as const,
        created_at: new Date().toISOString(),
      };
      setSession(token, placeholder);
      try {
        const user = await authApi.me();
        setUser(user);
        const returnTo = params.get("return_to") ?? "/orgs";
        navigate(returnTo, { replace: true });
      } catch {
        navigate("/orgs", { replace: true });
      }
    })();
  }, [navigate]);

  return (
    <div className="rounded-lg border border-[var(--border)] bg-[var(--surface)] p-6 shadow-md">
      <h1 className="m-0 text-[18px] font-semibold text-fg">GitHub 登录</h1>
      <div className="mt-4">
        {state.kind === "loading" && <Loading label="正在完成登录…" />}
        {state.kind === "pending" && (
          <>
            <p className="text-[13px] text-fg">账号已经创建，但还需要管理员审核才能登录。</p>
            <p className="mt-2 text-[12.5px] text-muted">
              {state.username ? `用户名：${state.username}。 ` : ""}
              管理员通过审核后，再次使用 GitHub 登录即可。
            </p>
          </>
        )}
        {state.kind === "error" && (
          <>
            <p className="text-[13px] text-[var(--danger)]">登录失败：{state.message}</p>
            <p className="mt-2 text-[12.5px] text-muted">
              错误代码：<code className="rounded-sm bg-[var(--surface-secondary)] px-1.5 py-0.5">{state.code}</code>
            </p>
          </>
        )}
      </div>
    </div>
  );
}
