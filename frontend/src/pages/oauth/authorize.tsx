import { Avatar, Button, Card, Chip } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { AuthorizePreview, OAuthScope } from "@/api/types";
import { RequireAuth } from "@/auth/guards";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";

/**
 * /oauth/authorize — the consent screen that a third-party app sends the
 * user's browser to. The backend has already validated client_id / scopes /
 * redirect_uri and stashed them under an opaque `req` id; we only carry that
 * id in the URL, so query-string tampering can't reshape the request.
 *
 * Flow:
 *   1. read `?req=` from the URL
 *   2. GET /authorize/preview → render the app name, logo, scopes
 *   3. user clicks Allow / Deny → POST /authorize/decision
 *   4. server returns `redirect_url`; we top-level navigate the browser back
 *      to the third-party with code + state in the query.
 *
 * The user has to be logged in for the decision POST to authenticate; the
 * RequireAuth guard wrapping the page handles the redirect to /login.
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

  // Pull req from query at first render — the SPA-side router pushed the
  // user here on a 302 from /api/v1/oauth/authorize.
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
      // Hand the user back to the third-party app via top-level navigation.
      window.location.assign(res.redirect_url);
    } catch (e) {
      setError(e as ApiError);
      setSubmitting(null);
    }
  }

  if (!preview && !error) {
    return (
      <Centered>
        <Loading />
      </Centered>
    );
  }
  if (error) {
    return (
      <Centered>
        <Card>
          <Card.Header>
            <Card.Title>无法继续授权</Card.Title>
          </Card.Header>
          <Card.Content>
            <ErrorBanner error={error} />
            <Button variant="secondary" onPress={() => navigate("/", { replace: true })}>
              返回首页
            </Button>
          </Card.Content>
        </Card>
      </Centered>
    );
  }
  if (!preview) return null;

  return (
    <Centered>
      <Card>
        <Card.Header>
          <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
            <Avatar size="lg">
              {preview.client.logo_url && (
                <Avatar.Image alt={preview.client.name} src={preview.client.logo_url} />
              )}
              <Avatar.Fallback>
                {initialsOf(preview.client.name)}
              </Avatar.Fallback>
            </Avatar>
            <div>
              <Card.Title>
                {preview.client.name}{" "}
                {preview.client.is_first_party && (
                  <Chip color="accent" size="sm" style={{ marginLeft: "0.4rem" }}>
                    官方
                  </Chip>
                )}
              </Card.Title>
              {preview.client.homepage_url && (
                <Card.Description>
                  <a
                    href={preview.client.homepage_url}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    {preview.client.homepage_url}
                  </a>
                </Card.Description>
              )}
            </div>
          </div>
        </Card.Header>
        <Card.Content>
          {preview.client.description && (
            <p style={{ color: "var(--muted)", marginBottom: "0.85rem" }}>
              {preview.client.description}
            </p>
          )}
          <p style={{ marginBottom: "0.4rem" }}>该应用希望获得以下权限：</p>
          <ul style={{ listStyle: "none", padding: 0, marginBottom: "1rem" }}>
            {preview.scopes_requested.map((s) => (
              <li
                key={s}
                style={{
                  display: "flex",
                  alignItems: "flex-start",
                  gap: "0.6rem",
                  padding: "0.4rem 0",
                  borderBottom: "1px solid var(--border)",
                }}
              >
                <code style={{ fontSize: "0.85rem" }}>{s}</code>
                <span style={{ color: "var(--muted)", fontSize: "0.85rem" }}>
                  {scopeDescription(s)}
                </span>
              </li>
            ))}
          </ul>
          <p style={{ color: "var(--muted)", fontSize: "0.85rem", marginBottom: "1rem" }}>
            授权后可随时在「设置 → 已授权应用」撤销。
          </p>
          <div style={{ display: "flex", gap: "0.6rem", justifyContent: "flex-end" }}>
            <Button
              variant="secondary"
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
          </div>
        </Card.Content>
      </Card>
    </Centered>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return <div style={{ maxWidth: 520, margin: "3rem auto" }}>{children}</div>;
}

// scopeDescription maps each scope to a one-line Chinese explanation. Kept
// here rather than in types.ts because the wording is UI copy and shouldn't
// be cargo-culted by other surfaces.
function scopeDescription(s: OAuthScope): string {
  switch (s) {
    case "user:read":
      return "读取你的用户名、头像和基本资料";
    case "user:write":
      return "修改你的基本资料";
    case "repo:read":
      return "读取你能访问的仓库元数据";
    case "repo:write":
      return "在你能访问的仓库内创建分支、推送提交";
    case "issue:read":
      return "读取 Issue 和评论";
    case "issue:write":
      return "创建 / 修改 Issue 与评论";
    case "mr:read":
      return "读取 Merge Request";
    case "mr:write":
      return "创建 / 修改 Merge Request";
    case "git:read":
      return "通过 git HTTPS 拉取代码";
    case "git:write":
      return "通过 git HTTPS 推送代码";
    default:
      return s;
  }
}

// initialsOf returns 1-2 visible characters to seed the avatar fallback. We
// don't require ASCII — the fallback slot renders any text just fine.
function initialsOf(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "?";
  const parts = trimmed.split(/\s+/);
  if (parts.length >= 2) {
    return (parts[0]!.charAt(0) + parts[1]!.charAt(0)).toUpperCase();
  }
  return trimmed.slice(0, 2).toUpperCase();
}
