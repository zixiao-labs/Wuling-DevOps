import { Card } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useEffect, useRef, useState } from "react";

import { auth as authApi } from "@/api/endpoints";
import { setSession, setUser } from "@/auth/store";

/**
 * /oauth/callback — terminal landing page for the GitHub OAuth flow.
 *
 * The backend redirects here with a URL fragment carrying one of:
 *   #access_token=...&expires_at=...
 *   #pending_approval=1&status=pending&username=...
 *   #error=...&error_description=...
 *
 * The fragment is the established SPA convention because it never leaves the
 * browser (the OAuth helper relies on this to keep the JWT out of server
 * logs). On success we hydrate the session via /me — the redirect handler
 * doesn't get to call setSession itself, so we round-trip through the API.
 */
export default function OAuthCallbackPage() {
  const navigate = useNavigate();
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "pending"; status: string; username: string }
    | { kind: "error"; code: string; message: string }
  >({ kind: "loading" });

  // Guard against React's StrictMode mounting the component twice in dev —
  // we only want to consume the fragment once.
  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return;
    ran.current = true;

    const hash = window.location.hash.startsWith("#")
      ? window.location.hash.slice(1)
      : window.location.hash;
    const params = new URLSearchParams(hash);
    // Wipe the fragment so a forward-nav doesn't re-process the token.
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
      // configureClient pulls the bearer through the auth store on every
      // request, so the token has to land in the store BEFORE /me — otherwise
      // /me goes out unauthenticated and the session never hydrates. Drop in
      // a placeholder user first, then replace it once /me returns.
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
        // /me failed — keep the token (it might just be a transient network
        // blip) and let the next page surface a real error via the auth guard.
        navigate("/orgs", { replace: true });
      }
    })();
  }, [navigate]);

  return (
    <div style={{ maxWidth: 480, margin: "3rem auto" }}>
      <Card>
        <Card.Header>
          <Card.Title>GitHub 登录</Card.Title>
        </Card.Header>
        <Card.Content>
          {state.kind === "loading" && <p>正在完成登录…</p>}
          {state.kind === "pending" && (
            <>
              <p>账号已经创建，但还需要管理员审核才能登录。</p>
              <p style={{ color: "var(--muted)", marginTop: "0.5rem", fontSize: "0.9rem" }}>
                {state.username ? `用户名：${state.username}。 ` : ""}
                管理员通过审核后，再次使用 GitHub 登录即可。
              </p>
            </>
          )}
          {state.kind === "error" && (
            <>
              <p style={{ color: "var(--danger, #c0392b)" }}>登录失败：{state.message}</p>
              <p style={{ color: "var(--muted)", fontSize: "0.85rem", marginTop: "0.5rem" }}>
                错误代码：<code>{state.code}</code>
              </p>
            </>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}
