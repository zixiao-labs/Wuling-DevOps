import { Button, Card, Input, Label, TextField } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { RequireAuth } from "@/auth/guards";
import { ErrorBanner } from "@/components/error-banner";

/**
 * /oauth/device — the user_code entry page for RFC 8628 Device Authorization
 * Grant. A device (typically Esperanta or a CLI tool) shows the user a short
 * code and asks them to visit this page to enter it. We approve, the device
 * sees `status=approved` on its next /token poll, and exchanges for tokens.
 */
export default function OAuthDevicePage() {
  return (
    <RequireAuth>
      <DeviceInner />
    </RequireAuth>
  );
}

function DeviceInner() {
  const navigate = useNavigate();
  // Bootstrap from query string so `verification_uri_complete` links
  // (the device pre-fills the user_code for the user, RFC 8628 §3.3.1).
  const initialCode =
    typeof window !== "undefined"
      ? new URLSearchParams(window.location.search).get("user_code") ?? ""
      : "";
  const [code, setCode] = useState(initialCode);
  const [status, setStatus] = useState<"idle" | "approving" | "denying" | "approved" | "denied">(
    "idle",
  );
  const [error, setError] = useState<ApiError | null>(null);

  async function approve(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!code.trim()) return;
    setStatus("approving");
    setError(null);
    try {
      await oauthProvider.deviceApprove(code.trim());
      setStatus("approved");
    } catch (err) {
      setError(err as ApiError);
      setStatus("idle");
    }
  }

  async function deny() {
    if (!code.trim()) return;
    setStatus("denying");
    setError(null);
    try {
      await oauthProvider.deviceDeny(code.trim());
      setStatus("denied");
    } catch (err) {
      setError(err as ApiError);
      setStatus("idle");
    }
  }

  if (status === "approved") {
    return (
      <Centered>
        <Card>
          <Card.Header>
            <Card.Title>已授权</Card.Title>
            <Card.Description>请回到设备完成登录。</Card.Description>
          </Card.Header>
          <Card.Content>
            <p style={{ color: "var(--muted)", marginBottom: "1rem" }}>
              如果设备一直停留在等待状态，请确认你的 user_code 与设备上显示的完全一致；可以再次输入并授权。
            </p>
            <Button variant="secondary" onPress={() => navigate("/settings/authorized-apps")}>
              查看已授权应用
            </Button>
          </Card.Content>
        </Card>
      </Centered>
    );
  }

  if (status === "denied") {
    return (
      <Centered>
        <Card>
          <Card.Header>
            <Card.Title>已拒绝</Card.Title>
          </Card.Header>
          <Card.Content>
            <p>对应设备会收到拒绝结果，本次登录已取消。</p>
            <Button onPress={() => setStatus("idle")} variant="secondary">
              输入另一个代码
            </Button>
          </Card.Content>
        </Card>
      </Centered>
    );
  }

  return (
    <Centered>
      <Card>
        <Card.Header>
          <Card.Title>授权设备登录</Card.Title>
          <Card.Description>
            在你的设备（Esperanta、CLI 等）上会看到一个 8 位代码 — 在此输入。
          </Card.Description>
        </Card.Header>
        <Card.Content>
          <form onSubmit={approve} style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}>
            <TextField name="user_code" value={code} onChange={setCode} isRequired>
              <Label>设备代码</Label>
              <Input
                placeholder="ABCD-1234"
                style={{
                  fontFamily: "ui-monospace, monospace",
                  letterSpacing: "0.1em",
                  textTransform: "uppercase",
                }}
              />
            </TextField>
            <ErrorBanner error={error} />
            <div style={{ display: "flex", gap: "0.6rem", justifyContent: "flex-end" }}>
              <Button
                type="button"
                variant="secondary"
                isDisabled={status !== "idle" || !code.trim()}
                onPress={deny}
              >
                {status === "denying" ? "处理中…" : "拒绝"}
              </Button>
              <Button type="submit" isDisabled={status !== "idle" || !code.trim()}>
                {status === "approving" ? "处理中…" : "授权"}
              </Button>
            </div>
          </form>
        </Card.Content>
      </Card>
    </Centered>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return <div style={{ maxWidth: 480, margin: "3rem auto" }}>{children}</div>;
}
