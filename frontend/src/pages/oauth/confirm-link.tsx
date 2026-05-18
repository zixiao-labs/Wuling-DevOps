import { Button, Card } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useState } from "react";

import { githubOAuth } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { isOAuthConfirmPending } from "@/api/types";
import { ErrorBanner } from "@/components/error-banner";
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
        // 202 — admin approval still required.
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

  if (pending) {
    return (
      <div style={{ maxWidth: 520, margin: "3rem auto" }}>
        <Card>
          <Card.Header>
            <Card.Title>账号待审核</Card.Title>
          </Card.Header>
          <Card.Content>
            <p>{pending}</p>
          </Card.Content>
        </Card>
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 520, margin: "3rem auto" }}>
      <Card>
        <Card.Header>
          <Card.Title>关联到已有账号？</Card.Title>
          <Card.Description>
            我们发现 GitHub 上的邮箱已经在武陵 DevOps 注册过本地账号。请选择：
          </Card.Description>
        </Card.Header>
        <Card.Content>
          <ul style={{ paddingLeft: "1.1rem", color: "var(--muted)", fontSize: "0.9rem" }}>
            <li>
              <strong>关联</strong>：把本次的 GitHub 身份合并到已有账号上，下次可以直接用 GitHub 登录。
            </li>
            <li>
              <strong>创建新账号</strong>：忽略邮箱冲突，新建一个独立账号（用户名会自动添加后缀）。
            </li>
          </ul>
          <ErrorBanner error={error} />
          <div style={{ display: "flex", gap: "0.5rem", marginTop: "1rem" }}>
            <Button onPress={() => decide("link")} isDisabled={busy !== null}>
              {busy === "link" ? "关联中…" : "关联到已有账号"}
            </Button>
            <Button variant="outline" onPress={() => decide("new")} isDisabled={busy !== null}>
              {busy === "new" ? "创建中…" : "创建新账号"}
            </Button>
          </div>
        </Card.Content>
      </Card>
    </div>
  );
}
