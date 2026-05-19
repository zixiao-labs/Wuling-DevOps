import {
  Button,
  Card,
  Description,
  Input,
  Label,
  Modal,
  Table,
  TextField,
} from "@heroui/react";
import CopyIcon from "@gravity-ui/icons/Copy";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { useEffect, useRef, useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import {
  type CreateOAuthAppRequest,
  type CreateOAuthAppResponse,
  type OAuthAppView,
  type OAuthScope,
  SUPPORTED_OAUTH_SCOPES,
} from "@/api/types";

/**
 * /settings/oauth-apps — owner-facing CRUD for OAuth Apps. Patterned after
 * tokens.tsx: a creation form on top, a table of existing apps below, and a
 * one-time-only modal that shows the raw client_secret. Confidential apps
 * get a long-lived `wlocs_…` secret; public apps (default) get nothing — the
 * Authorization Code + PKCE flow doesn't need a secret.
 */
export default function OAuthAppsPage() {
  const [items, setItems] = useState<OAuthAppView[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [form, setForm] = useState<NewAppForm>(blankForm());
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreateOAuthAppResponse | null>(null);
  const [resetSecret, setResetSecret] = useState<{ id: string; secret: string } | null>(null);
  const [copied, setCopied] = useState(false);

  const loadController = useRef<AbortController | null>(null);
  const copyResetTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  function load() {
    loadController.current?.abort();
    const ac = new AbortController();
    loadController.current = ac;
    setError(null);
    oauthProvider.apps
      .list()
      .then((res) => {
        if (!ac.signal.aborted) setItems(res);
      })
      .catch((e) => {
        if (ac.signal.aborted) return;
        setError(e as ApiError);
      });
  }

  useEffect(() => {
    load();
    return () => {
      loadController.current?.abort();
      if (copyResetTimer.current !== null) clearTimeout(copyResetTimer.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    const redirects = form.redirectUris
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    if (redirects.length === 0) {
      setError(new ApiError(0, "bad_request", "请至少填一个 redirect URI（每行一个）。"));
      return;
    }
    if (form.scopes.length === 0) {
      setError(new ApiError(0, "bad_request", "请至少选择一个 scope。"));
      return;
    }
    const body: CreateOAuthAppRequest = {
      name: form.name,
      homepage_url: form.homepageUrl || undefined,
      description: form.description || undefined,
      logo_url: form.logoUrl || undefined,
      is_confidential: form.isConfidential,
      redirect_uris: redirects,
      default_scopes: form.scopes,
    };
    setCreating(true);
    setError(null);
    try {
      const res = await oauthProvider.apps.create(body);
      setCreated(res);
      setForm(blankForm());
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  async function onDelete(id: string) {
    if (!confirm("删除这个 OAuth App？所有用户对它的授权与 token 都会立刻失效。")) return;
    try {
      await oauthProvider.apps.delete(id);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  async function onResetSecret(id: string) {
    if (!confirm("重置 client_secret？旧 secret 会立刻失效，未更新的客户端会无法换取 token。")) {
      return;
    }
    try {
      const res = await oauthProvider.apps.resetSecret(id);
      setResetSecret({ id, secret: res.client_secret });
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <Card>
        <Card.Header>
          <Card.Title>OAuth 应用</Card.Title>
          <Card.Description>
            创建一个 OAuth 应用让第三方在你的账户身份下访问数据。Authorization Code + PKCE 是默认流程；
            Device Authorization Grant 适用于 IDE、CLI 等环境。
          </Card.Description>
        </Card.Header>
        <Card.Content>
          <form onSubmit={onCreate} style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}>
            <TextField name="name" value={form.name} onChange={(v) => setForm({ ...form, name: v })} isRequired>
              <Label>名称</Label>
              <Input placeholder="My App" />
              <Description>展示给授权对话框上的用户看的名字。</Description>
            </TextField>
            <TextField
              name="homepage_url"
              value={form.homepageUrl}
              onChange={(v) => setForm({ ...form, homepageUrl: v })}
            >
              <Label>主页 URL</Label>
              <Input placeholder="https://example.com" />
            </TextField>
            <TextField
              name="logo_url"
              value={form.logoUrl}
              onChange={(v) => setForm({ ...form, logoUrl: v })}
            >
              <Label>Logo URL</Label>
              <Input placeholder="https://example.com/logo.png" />
            </TextField>
            <TextField
              name="description"
              value={form.description}
              onChange={(v) => setForm({ ...form, description: v })}
            >
              <Label>简介</Label>
              <Input placeholder="一句话说明这个 app 做什么。" />
            </TextField>
            <div>
              <Label>回调 URI</Label>
              <textarea
                value={form.redirectUris}
                onChange={(e) => setForm({ ...form, redirectUris: e.target.value })}
                placeholder={"https://example.com/oauth/callback\nhttp://127.0.0.1"}
                rows={3}
                style={{
                  width: "100%",
                  padding: "0.5rem 0.6rem",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--field-radius)",
                  background: "var(--surface)",
                  fontFamily: "ui-monospace, monospace",
                  fontSize: "0.85rem",
                }}
              />
              <Description>
                每行一个；必须与发起授权时的 redirect_uri 完全一致。<code>http://127.0.0.1</code> 是
                loopback 例外，允许任意端口。
              </Description>
            </div>
            <div>
              <Label>默认 scope</Label>
              <div style={{ display: "flex", flexWrap: "wrap", gap: "0.4rem", marginTop: "0.3rem" }}>
                {SUPPORTED_OAUTH_SCOPES.map((s) => (
                  <ScopeCheckbox
                    key={s}
                    scope={s}
                    checked={form.scopes.includes(s)}
                    onChange={(checked) =>
                      setForm({
                        ...form,
                        scopes: checked
                          ? [...form.scopes, s]
                          : form.scopes.filter((x) => x !== s),
                      })
                    }
                  />
                ))}
              </div>
            </div>
            <label
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "0.5rem",
                fontSize: "0.9rem",
              }}
            >
              <input
                type="checkbox"
                checked={form.isConfidential}
                onChange={(e) => setForm({ ...form, isConfidential: e.target.checked })}
              />
              <span>这是个机密客户端（服务端持有 client_secret）</span>
            </label>
            <ErrorBanner error={error} />
            <div>
              <Button type="submit" isDisabled={creating || !form.name}>
                {creating ? "创建中…" : "创建 OAuth 应用"}
              </Button>
            </div>
          </form>
        </Card.Content>
      </Card>

      {items === null ? (
        <Loading />
      ) : items.length === 0 ? (
        <Card>
          <Card.Content>
            <div style={{ color: "var(--muted)", padding: "1rem 0" }}>还没有 OAuth 应用。</div>
          </Card.Content>
        </Card>
      ) : (
        <Card>
          <Card.Header>
            <Card.Title>现有应用</Card.Title>
          </Card.Header>
          <Card.Content>
            <Table>
              <Table.ScrollContainer>
                <Table.Content aria-label="OAuth 应用">
                  <Table.Header>
                    <Table.Column isRowHeader>名称</Table.Column>
                    <Table.Column>Client ID</Table.Column>
                    <Table.Column>类型</Table.Column>
                    <Table.Column>scope</Table.Column>
                    <Table.Column>创建</Table.Column>
                    <Table.Column>操作</Table.Column>
                  </Table.Header>
                  <Table.Body>
                    {items.map((a) => (
                      <Table.Row key={a.id}>
                        <Table.Cell>{a.name}</Table.Cell>
                        <Table.Cell>
                          <code style={{ fontSize: "0.8rem" }}>{a.client_id}</code>
                        </Table.Cell>
                        <Table.Cell>{a.is_confidential ? "机密" : "公开"}</Table.Cell>
                        <Table.Cell>
                          <code style={{ fontSize: "0.75rem" }}>
                            {a.default_scopes.join(", ") || "—"}
                          </code>
                        </Table.Cell>
                        <Table.Cell>
                          <RelativeTime iso={a.created_at} />
                        </Table.Cell>
                        <Table.Cell>
                          <div style={{ display: "flex", gap: "0.4rem" }}>
                            {a.is_confidential && (
                              <Button size="sm" variant="secondary" onPress={() => onResetSecret(a.id)}>
                                重置 secret
                              </Button>
                            )}
                            <Button
                              variant="danger-soft"
                              size="sm"
                              onPress={() => onDelete(a.id)}
                            >
                              <TrashIcon width={14} height={14} /> 删除
                            </Button>
                          </div>
                        </Table.Cell>
                      </Table.Row>
                    ))}
                  </Table.Body>
                </Table.Content>
              </Table.ScrollContainer>
            </Table>
          </Card.Content>
        </Card>
      )}

      <Modal>
        <Modal.Backdrop isOpen={created !== null} onOpenChange={(o) => !o && setCreated(null)}>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>OAuth 应用已创建</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p>
                  <strong>client_id</strong>
                </p>
                <CopyableBlock value={created?.client_id ?? ""} />
                {created?.client_secret ? (
                  <>
                    <p style={{ marginTop: "1rem" }}>
                      <strong>client_secret（只显示一次）</strong>
                    </p>
                    <CopyableBlock
                      value={created.client_secret}
                      onCopiedChange={(v) => {
                        setCopied(v);
                        if (copyResetTimer.current !== null) {
                          clearTimeout(copyResetTimer.current);
                        }
                        if (v) {
                          copyResetTimer.current = setTimeout(() => setCopied(false), 1500);
                        }
                      }}
                      copied={copied}
                    />
                  </>
                ) : (
                  <p style={{ marginTop: "1rem", color: "var(--muted)" }}>
                    公开客户端不签发 client_secret —— 使用 Authorization Code + PKCE 即可。
                  </p>
                )}
              </Modal.Body>
              <Modal.Footer>
                <Button onPress={() => setCreated(null)}>关闭</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <Modal>
        <Modal.Backdrop isOpen={resetSecret !== null} onOpenChange={(o) => !o && setResetSecret(null)}>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>新的 client_secret</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p>
                  <strong>这是新的 secret，只显示一次。</strong>未更新的客户端会无法换 token。
                </p>
                <CopyableBlock value={resetSecret?.secret ?? ""} />
              </Modal.Body>
              <Modal.Footer>
                <Button onPress={() => setResetSecret(null)}>关闭</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}

// ---------- helpers ----------

interface NewAppForm {
  name: string;
  homepageUrl: string;
  description: string;
  logoUrl: string;
  isConfidential: boolean;
  redirectUris: string; // newline-separated
  scopes: OAuthScope[];
}

function blankForm(): NewAppForm {
  return {
    name: "",
    homepageUrl: "",
    description: "",
    logoUrl: "",
    isConfidential: false,
    redirectUris: "",
    scopes: ["user:read", "repo:read"],
  };
}

function ScopeCheckbox({
  scope,
  checked,
  onChange,
}: {
  scope: OAuthScope;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: "0.3rem",
        padding: "0.25rem 0.6rem",
        border: "1px solid var(--border)",
        borderRadius: "var(--field-radius)",
        cursor: "pointer",
        background: checked ? "var(--surface-secondary)" : "var(--surface)",
      }}
    >
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <code style={{ fontSize: "0.85rem" }}>{scope}</code>
    </label>
  );
}

function CopyableBlock({
  value,
  onCopiedChange,
  copied,
}: {
  value: string;
  onCopiedChange?: (copied: boolean) => void;
  copied?: boolean;
}) {
  async function copy() {
    if (!value || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(value);
      onCopiedChange?.(true);
    } catch {
      alert("复制失败，请手动选中文本。");
    }
  }
  return (
    <div style={{ display: "flex", gap: "0.4rem", alignItems: "stretch", marginTop: "0.3rem" }}>
      <pre
        style={{
          flex: 1,
          background: "var(--surface-secondary)",
          padding: "0.6rem 0.75rem",
          borderRadius: "var(--radius)",
          overflowX: "auto",
          fontFamily: "ui-monospace, monospace",
          fontSize: "0.85rem",
          userSelect: "all",
          margin: 0,
        }}
      >
        {value}
      </pre>
      {onCopiedChange && (
        <Button variant="secondary" onPress={copy}>
          <CopyIcon width={16} height={16} /> {copied ? "已复制" : "复制"}
        </Button>
      )}
    </div>
  );
}
