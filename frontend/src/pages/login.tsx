import { Button, Card, Description, FieldError, Input, Label, TextField } from "@heroui/react";
import { Link, useNavigate, useLocation } from "chen-the-dawnstreak";
import { useState } from "react";

import { auth, githubOAuth } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { RequireAnon } from "@/auth/guards";
import { setSession } from "@/auth/store";

interface RedirectState {
  from?: string;
}

export default function LoginPage() {
  return (
    <RequireAnon>
      <LoginForm />
    </RequireAnon>
  );
}

function LoginForm() {
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as RedirectState | null)?.from ?? "/orgs";

  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const res = await auth.login({ login, password });
      setSession(res.access_token, res.user);
      navigate(from, { replace: true });
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusy(false);
    }
  }

  function startGithubLogin() {
    // Top-level navigation — the redirect dance must run in the document,
    // not inside fetch (the browser has to follow GitHub's 302 back to us).
    window.location.assign(githubOAuth.startURL(from));
  }

  return (
    <div style={{ maxWidth: 420, margin: "3rem auto" }}>
      <Card>
        <Card.Header>
          <Card.Title>登录到武陵 DevOps</Card.Title>
          <Card.Description>使用用户名或邮箱与密码登录，或者通过 GitHub 登录。</Card.Description>
        </Card.Header>
        <Card.Content>
          <form
            onSubmit={onSubmit}
            style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}
          >
            <TextField name="login" value={login} onChange={setLogin} isRequired autoComplete="username">
              <Label>用户名或邮箱</Label>
              <Input placeholder="amiya" />
            </TextField>
            <TextField
              name="password"
              type="password"
              value={password}
              onChange={setPassword}
              isRequired
              autoComplete="current-password"
            >
              <Label>密码</Label>
              <Input placeholder="••••••••" />
              <Description>至少 8 个字符。</Description>
              <FieldError />
            </TextField>
            <ErrorBanner error={error} />
            <Button type="submit" isDisabled={busy}>
              {busy ? "登录中…" : "登录"}
            </Button>
          </form>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.6rem",
              margin: "1rem 0 0.6rem",
              color: "var(--muted)",
              fontSize: "0.75rem",
            }}
          >
            <span style={{ flex: 1, height: 1, background: "var(--border)" }} />
            或
            <span style={{ flex: 1, height: 1, background: "var(--border)" }} />
          </div>
          <Button variant="outline" onPress={startGithubLogin} isDisabled={busy} style={{ width: "100%" }}>
            使用 GitHub 登录
          </Button>
        </Card.Content>
        <Card.Footer>
          <span style={{ color: "var(--muted)", fontSize: "0.85rem" }}>
            还没账号？<Link to="/register" style={{ color: "var(--accent)" }}>立即注册</Link>
          </span>
        </Card.Footer>
      </Card>
    </div>
  );
}
