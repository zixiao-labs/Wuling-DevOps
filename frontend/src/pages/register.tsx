import { Button, Card, Description, FieldError, Input, Label, TextField } from "@heroui/react";
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
        // Approval required — don't issue a session, surface a clear "your
        // account is waiting for an admin to approve it" message instead.
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
      <div style={{ maxWidth: 520, margin: "3rem auto" }}>
        <Card>
          <Card.Header>
            <Card.Title>账号待审核</Card.Title>
            <Card.Description>感谢注册武陵 DevOps。</Card.Description>
          </Card.Header>
          <Card.Content>
            <p>
              {pendingMessage}
            </p>
            <p style={{ color: "var(--muted)", marginTop: "0.75rem", fontSize: "0.9rem" }}>
              管理员审核通过之后，你可以使用注册时填写的用户名和密码登录。
              如果长时间未收到回复，请联系实例的管理员。
            </p>
          </Card.Content>
          <Card.Footer>
            <Link to="/login" style={{ color: "var(--accent)" }}>返回登录页</Link>
          </Card.Footer>
        </Card>
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 480, margin: "3rem auto" }}>
      <Card>
        <Card.Header>
          <Card.Title>创建账号</Card.Title>
          <Card.Description>
            注册后将自动获得一个同名个人组织。新账号通常需要管理员审核才能登录。
          </Card.Description>
        </Card.Header>
        <Card.Content>
          <form
            onSubmit={onSubmit}
            style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}
          >
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
            使用 GitHub 注册 / 登录
          </Button>
        </Card.Content>
        <Card.Footer>
          <span style={{ color: "var(--muted)", fontSize: "0.85rem" }}>
            已有账号？<Link to="/login" style={{ color: "var(--accent)" }}>去登录</Link>
          </span>
        </Card.Footer>
      </Card>
    </div>
  );
}
