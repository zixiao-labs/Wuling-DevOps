import { Button, Card, Description, FieldError, Input, Label, TextField } from "@heroui/react";
import { useNavigate, useLocation } from "chen-the-dawnstreak";
import { useState } from "react";

import { auth } from "@/api/endpoints";
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

  return (
    <div style={{ maxWidth: 420, margin: "3rem auto" }}>
      <Card>
        <Card.Header>
          <Card.Title>登录到武陵 DevOps</Card.Title>
          <Card.Description>使用用户名或邮箱与密码登录。</Card.Description>
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
        </Card.Content>
        <Card.Footer>
          <span style={{ color: "var(--muted)", fontSize: "0.85rem" }}>
            还没账号？<a href="/register" style={{ color: "var(--accent)" }}>立即注册</a>
          </span>
        </Card.Footer>
      </Card>
    </div>
  );
}
