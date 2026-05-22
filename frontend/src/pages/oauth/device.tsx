import { Button, Input, Label, TextField } from "@heroui/react";
import { useNavigate } from "chen-the-dawnstreak";
import { useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { RequireAuth } from "@/auth/guards";
import { ErrorBanner } from "@/components/error-banner";
import { PageContainer } from "@/components/page/primitives";

/**
 * /oauth/device — the user_code entry page for RFC 8628 Device Authorization
 * Grant.
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

  return (
    <PageContainer>
      <div className="mx-auto max-w-[480px]">
        {status === "approved" ? (
          <Panel title="已授权" description="请回到设备完成登录。">
            <p className="mb-4 text-[12.5px] text-muted">
              如果设备一直停留在等待状态，请确认你的 user_code 与设备上显示的完全一致；可以再次输入并授权。
            </p>
            <Button variant="outline" onPress={() => navigate("/settings/authorized-apps")}>
              查看已授权应用
            </Button>
          </Panel>
        ) : status === "denied" ? (
          <Panel title="已拒绝">
            <p className="mb-3 text-[13px] text-fg">对应设备会收到拒绝结果，本次登录已取消。</p>
            <Button variant="outline" onPress={() => setStatus("idle")}>
              输入另一个代码
            </Button>
          </Panel>
        ) : (
          <Panel
            title="授权设备登录"
            description="在你的设备（Esperanta、CLI 等）上会看到一个 8 位代码 — 在此输入。"
          >
            <form onSubmit={approve} className="flex flex-col gap-3.5">
              <TextField name="user_code" value={code} onChange={setCode} isRequired>
                <Label>设备代码</Label>
                <Input
                  placeholder="ABCD-1234"
                  className="font-mono tracking-[0.15em] uppercase"
                />
              </TextField>
              <ErrorBanner error={error} />
              <div className="flex justify-end gap-2">
                <Button
                  type="button"
                  variant="outline"
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
          </Panel>
        )}
      </div>
    </PageContainer>
  );
}

function Panel({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-md border border-[var(--border)] bg-[var(--surface)] p-5 shadow-sm">
      <h1 className="m-0 text-[18px] font-semibold text-fg">{title}</h1>
      {description ? <p className="mt-1 text-[12.5px] text-muted">{description}</p> : null}
      <div className="mt-4">{children}</div>
    </section>
  );
}
