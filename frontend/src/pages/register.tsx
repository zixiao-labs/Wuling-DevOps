import { Button, Description, FieldError, Input, Label, TextField } from "@heroui/react";
import { Link, useNavigate } from "chen-the-dawnstreak";
import { useState } from "react";

import { auth, githubOAuth, isPendingResponse } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { RequireAnon } from "@/auth/guards";
import { setSession } from "@/auth/store";

export default function RegisterPage() {
  return (
    <RequireAnon>
      <RegisterForm />
    </RequireAnon>
  );
}

function RegisterForm() {
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const [pendingMessage, setPendingMessage] = useState<string | null>(null);

  const isPasswordWeak = password.length > 0 && password.length < 8;
  const isUsernameInvalid =
    username.length > 0 && (username.length < 2 || username.length > 64);

  async function onSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const res = await auth.register({
        username,
        email,
        password,
        display_name: displayName || undefined,
      });
      if (isPendingResponse(res)) {
        setPendingMessage(res.message);
        return;
      }
      setSession(res.access_token, res.user);
      navigate("/orgs", { replace: true });
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusy(false);
    }
  }

  function startGithubLogin() {
    window.location.assign(githubOAuth.startURL());
  }

  if (pendingMessage) {
    return (
      <div className="rounded-lg border border-[var(--border)] bg-[var(--surface)] p-6 shadow-md">
        <h1 className="m-0 text-[20px] font-semibold text-fg">账号待审核</h1>
        <p className="mt-1 text-[12.5px] text-muted">感谢注册武陵 DevOps。</p>
        <div className="mt-5 rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] px-3 py-2.5 text-[13px] text-fg">
          {pendingMessage}
        </div>
        <p className="mt-3 text-[12.5px] text-muted">
          管理员审核通过之后，你可以使用注册时填写的用户名和密码登录。
          如果长时间未收到回复，请联系实例的管理员。
        </p>
        <div className="mt-5 border-t border-[var(--separator)] pt-3 text-[12px]">
          <Link to="/login" className="text-[var(--accent)] hover:underline">返回登录页</Link>
        </div>
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-[var(--border)] bg-[var(--surface)] p-6 shadow-md">
      <div className="mb-5">
        <h1 className="m-0 text-[20px] font-semibold leading-tight text-fg">创建账号</h1>
        <p className="mt-1 text-[12.5px] text-muted">
          注册后将自动获得一个同名个人组织。新账号通常需要管理员审核才能登录。
        </p>
      </div>
      <form onSubmit={onSubmit} className="flex flex-col gap-3.5">
        <TextField
          name="username"
          value={username}
          onChange={setUsername}
          isRequired
          isInvalid={isUsernameInvalid}
          autoComplete="username"
        >
          <Label>用户名</Label>
          <Input placeholder="amiya" />
          {isUsernameInvalid ? (
            <FieldError>用户名需为 2–64 个字符。</FieldError>
          ) : (
            <Description>登录与项目路径中用到的名字（2–64 字符）。</Description>
          )}
        </TextField>
        <TextField name="email" type="email" value={email} onChange={setEmail} isRequired>
          <Label>邮箱</Label>
          <Input placeholder="amiya@example.com" />
        </TextField>
        <TextField name="display_name" value={displayName} onChange={setDisplayName}>
          <Label>显示名（可选）</Label>
          <Input placeholder="阿米娅" />
        </TextField>
        <TextField
          name="password"
          type="password"
          value={password}
          onChange={setPassword}
          isRequired
          isInvalid={isPasswordWeak}
          autoComplete="new-password"
        >
          <Label>密码</Label>
          <Input />
          {isPasswordWeak ? (
            <FieldError>至少 8 个字符。</FieldError>
          ) : (
            <Description>至少 8 个字符；建议使用密码管理器生成。</Description>
          )}
        </TextField>
        <ErrorBanner error={error} />
        <Button type="submit" isDisabled={busy || isPasswordWeak || isUsernameInvalid}>
          {busy ? "创建中…" : "注册"}
        </Button>
      </form>
      <div className="my-4 flex items-center gap-3 text-[11px] text-muted">
        <span className="h-px flex-1 bg-[var(--border)]" />
        或
        <span className="h-px flex-1 bg-[var(--border)]" />
      </div>
      <Button variant="outline" onPress={startGithubLogin} isDisabled={busy} className="w-full">
        使用 GitHub 注册 / 登录
      </Button>
      <div className="mt-5 border-t border-[var(--separator)] pt-3 text-center text-[12px] text-muted">
        已有账号？
        <Link to="/login" className="ml-1 font-medium text-[var(--accent)] hover:underline">
          去登录
        </Link>
      </div>
    </div>
  );
}
