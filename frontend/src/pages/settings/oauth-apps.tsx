import {
  Avatar,
  Button,
  Description,
  Input,
  Label,
  Modal,
  TextField,
} from "@heroui/react";
import CopyIcon from "@gravity-ui/icons/Copy";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { useEffect, useRef, useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { Pill } from "@/components/page/badges";
import {
  type CreateOAuthAppRequest,
  type CreateOAuthAppResponse,
  type OAuthAppView,
  type OAuthScope,
  SUPPORTED_OAUTH_SCOPES,
} from "@/api/types";

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
     
  }, []);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    const redirects = form.redirectUris.split("\n").map((s) => s.trim()).filter(Boolean);
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
    <PageContainer>
      <PageHeader
        title="OAuth 应用"
        description="创建一个 OAuth 应用让第三方在你的账户身份下访问数据。Authorization Code + PKCE 是默认流程；Device Authorization Grant 适用于 IDE、CLI 等环境。"
      />

      <Surface className="mb-4">
        <SurfaceHeader title="创建新应用" />
        <SurfaceBody>
          <form onSubmit={onCreate} className="flex flex-col gap-3.5">
            <TextField name="name" value={form.name} onChange={(v) => setForm({ ...form, name: v })} isRequired>
              <Label>名称</Label>
              <Input placeholder="My App" />
              <Description>展示给授权对话框上的用户看的名字。</Description>
            </TextField>
            <div className="grid gap-3.5 sm:grid-cols-2">
              <TextField name="homepage_url" value={form.homepageUrl} onChange={(v) => setForm({ ...form, homepageUrl: v })}>
                <Label>主页 URL</Label>
                <Input placeholder="https://example.com" />
              </TextField>
              <TextField name="logo_url" value={form.logoUrl} onChange={(v) => setForm({ ...form, logoUrl: v })}>
                <Label>Logo URL</Label>
                <Input placeholder="https://example.com/logo.png" />
              </TextField>
            </div>
            <TextField name="description" value={form.description} onChange={(v) => setForm({ ...form, description: v })}>
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
                className="mt-1 w-full rounded-md border border-[var(--border)] bg-[var(--field-background)] px-2.5 py-1.5 font-mono text-[12.5px] text-[var(--field-foreground)] focus:border-[var(--accent)] focus:outline-none"
              />
              <Description>
                每行一个；必须与发起授权时的 redirect_uri 完全一致。
                <code className="font-mono">http://127.0.0.1</code> 是 loopback 例外，允许任意端口。
              </Description>
            </div>
            <div>
              <Label>默认 scope</Label>
              <div className="mt-1.5 flex flex-wrap gap-2">
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
            <label className="inline-flex cursor-pointer items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={form.isConfidential}
                onChange={(e) => setForm({ ...form, isConfidential: e.target.checked })}
                className="accent-[var(--accent)]"
              />
              <span>这是个机密客户端（服务端持有 client_secret）</span>
            </label>
            <ErrorBanner error={error} />
            <div className="flex justify-end">
              <Button type="submit" isDisabled={creating || !form.name}>
                {creating ? "创建中…" : "创建 OAuth 应用"}
              </Button>
            </div>
          </form>
        </SurfaceBody>
      </Surface>

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            现有应用{items ? ` · ${items.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <Loading />
          ) : items.length === 0 ? (
            <EmptyState inset title="还没有 OAuth 应用" description="创建一个就能让第三方接入。" />
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {items.map((a) => (
                <li key={a.id} className="flex items-start gap-3 px-4 py-3">
                  <Avatar size="md">
                    {a.logo_url && <Avatar.Image alt={a.name} src={a.logo_url} />}
                    <Avatar.Fallback>{a.name.slice(0, 2).toUpperCase()}</Avatar.Fallback>
                  </Avatar>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="text-[13.5px] font-medium text-fg">{a.name}</span>
                      <Pill tone={a.is_confidential ? "info" : "neutral"}>
                        {a.is_confidential ? "机密" : "公开"}
                      </Pill>
                      {a.is_first_party ? <Pill tone="success">官方</Pill> : null}
                    </div>
                    <div className="mt-0.5 truncate text-[11.5px] text-muted">
                      <code className="font-mono">{a.client_id}</code> ·{" "}
                      <RelativeTime iso={a.created_at} /> 创建
                    </div>
                    {a.default_scopes.length > 0 ? (
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        {a.default_scopes.map((s) => (
                          <Pill key={s} tone="neutral">{s}</Pill>
                        ))}
                      </div>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    {a.is_confidential && (
                      <Button size="sm" variant="outline" onPress={() => onResetSecret(a.id)}>
                        重置 secret
                      </Button>
                    )}
                    <Button size="sm" variant="danger-soft" onPress={() => onDelete(a.id)}>
                      <TrashIcon width={13} height={13} /> 删除
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </SurfaceBody>
      </Surface>

      <Modal>
        <Modal.Backdrop isOpen={created !== null} onOpenChange={(o) => !o && setCreated(null)}>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>OAuth 应用已创建</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p className="text-[12.5px] font-medium text-fg">client_id</p>
                <CopyableBlock value={created?.client_id ?? ""} />
                {created?.client_secret ? (
                  <>
                    <p className="mt-4 text-[12.5px] font-medium text-fg">
                      client_secret（只显示一次）
                    </p>
                    <CopyableBlock
                      value={created.client_secret}
                      onCopiedChange={(v) => {
                        setCopied(v);
                        if (copyResetTimer.current !== null) clearTimeout(copyResetTimer.current);
                        if (v) copyResetTimer.current = setTimeout(() => setCopied(false), 1500);
                      }}
                      copied={copied}
                    />
                  </>
                ) : (
                  <p className="mt-4 text-[12.5px] text-muted">
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
                <p className="mb-3 text-[12.5px] text-fg">
                  <strong>这是新的 secret，只显示一次。</strong>
                  未更新的客户端会无法换 token。
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
    </PageContainer>
  );
}

interface NewAppForm {
  name: string;
  homepageUrl: string;
  description: string;
  logoUrl: string;
  isConfidential: boolean;
  redirectUris: string;
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
      className={[
        "inline-flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1 transition-colors",
        checked
          ? "border-[var(--accent)] bg-[var(--surface-secondary)]"
          : "border-[var(--border)] bg-[var(--surface)] hover:bg-[var(--surface-secondary)]",
      ].join(" ")}
    >
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="accent-[var(--accent)]"
      />
      <code className="font-mono text-[12px]">{scope}</code>
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
    <div className="mt-1.5 flex items-stretch gap-2">
      <pre className="m-0 flex-1 overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] px-2.5 py-2 font-mono text-[12.5px] text-fg select-all">
        {value}
      </pre>
      {onCopiedChange && (
        <Button variant="outline" onPress={copy}>
          <CopyIcon width={14} height={14} /> {copied ? "已复制" : "复制"}
        </Button>
      )}
    </div>
  );
}
