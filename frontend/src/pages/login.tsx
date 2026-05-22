import { Button, Description, FieldError, Input, Label, TextField } from "@heroui/react";
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
    window.location.assign(githubOAuth.startURL(from));
  }

  return (
    <div className="rounded-lg border border-[var(--border)] bg-[var(--surface)] p-6 shadow-md">
      <div className="mb-5">
        <h1 className="m-0 text-[20px] font-semibold leading-tight text-fg">登录到武陵 DevOps</h1>
        <p className="mt-1 text-[12.5px] text-muted">
          使用用户名或邮箱与密码登录，或者通过 GitHub 登录。
        </p>
      </div>
      <form onSubmit={onSubmit} className="flex flex-col gap-3.5">
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
      <div className="my-4 flex items-center gap-3 text-[11px] text-muted">
        <span className="h-px flex-1 bg-[var(--border)]" />
        或
        <span className="h-px flex-1 bg-[var(--border)]" />
      </div>
      <Button variant="outline" onPress={startGithubLogin} isDisabled={busy} className="w-full">
        使用 GitHub 登录
      </Button>
      <div className="mt-5 border-t border-[var(--separator)] pt-3 text-center text-[12px] text-muted">
        还没账号？
        <Link to="/register" className="ml-1 font-medium text-[var(--accent)] hover:underline">
          立即注册
        </Link>
      </div>
    </div>
  );
}
