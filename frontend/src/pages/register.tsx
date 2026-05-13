import { Button, Card, Description, FieldError, Input, Label, TextField } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useState } from "react";

import { auth } from "@/api/endpoints";
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

  const isPasswordWeak = password.length > 0 && password.length < 8;
  const isUsernameInvalid = username.length > 0 && username.length < 2;

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
      setSession(res.access_token, res.user);
      navigate("/orgs", { replace: true });
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div style={{ maxWidth: 480, margin: "3rem auto" }}>
      <Card>
        <Card.Header>
          <Card.Title>创建账号</Card.Title>
          <Card.Description>注册后将自动获得一个同名个人组织。</Card.Description>
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
                <FieldError>用户名至少 2 个字符。</FieldError>
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
        </Card.Content>
        <Card.Footer>
          <span style={{ color: "var(--muted)", fontSize: "0.85rem" }}>
            已有账号？<a href="/login" style={{ color: "var(--accent)" }}>去登录</a>
          </span>
        </Card.Footer>
      </Card>
    </div>
  );
}
