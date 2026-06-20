import {
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

import { tokens as patApi } from "@/api/endpoints";
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
import type { AccessTokenView, PatScope } from "@/api/types";

const SCOPES: PatScope[] = ["repo:read", "repo:write"];

export default function TokensPage() {
  const [items, setItems] = useState<AccessTokenView[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<PatScope[]>(["repo:read"]);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<AccessTokenView | null>(null);
  const [copied, setCopied] = useState(false);

  const loadController = useRef<AbortController | null>(null);
  const copyResetTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  function load() {
    loadController.current?.abort();
    const ac = new AbortController();
    loadController.current = ac;
    setError(null);
    patApi
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
    if (!scopes || scopes.length === 0) {
      setError(new ApiError(0, "unknown", "请至少选择一个范围（scope）。"));
      return;
    }
    setCreating(true);
    setError(null);
    try {
      const tok = await patApi.create({ name, scopes });
      setCreated(tok);
      setName("");
      setScopes(["repo:read"]);
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  async function onRevoke(id: string) {
    if (!confirm("吊销这个令牌？已使用它的客户端会立即失效。")) return;
    try {
      await patApi.revoke(id);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <PageContainer>
      <PageHeader
        title="访问令牌（PAT）"
        description={
          <>
            创建后<strong>原始令牌只显示一次</strong>。
            CLI 推/拉时填到密码栏（用户名随意）。
          </>
        }
      />

      <Surface className="mb-4">
        <SurfaceHeader title="创建新令牌" />
        <SurfaceBody>
          <form onSubmit={onCreate} className="flex flex-col gap-3.5">
            <TextField name="name" value={name} onChange={setName} isRequired>
              <Label>名称</Label>
              <Input placeholder="laptop / ci-runner / …" />
              <Description>仅自己看，长度不超过 64。</Description>
            </TextField>
            <div>
              <div className="mb-1.5 text-[12.5px] font-medium text-fg">范围（scope）</div>
              <div className="flex flex-wrap gap-2">
                {SCOPES.map((s) => {
                  const selected = scopes.includes(s);
                  return (
                    <label
                      key={s}
                      className={[
                        "inline-flex cursor-pointer items-center gap-2 rounded-md border px-3 py-1.5 transition-colors",
                        selected
                          ? "border-[var(--accent)] bg-[var(--surface-secondary)]"
                          : "border-[var(--border)] bg-[var(--surface)] hover:bg-[var(--surface-secondary)]",
                      ].join(" ")}
                    >
                      <input
                        type="checkbox"
                        checked={selected}
                        onChange={(e) =>
                          setScopes((cur) =>
                            e.target.checked ? [...cur, s] : cur.filter((x) => x !== s),
                          )
                        }
                        className="accent-[var(--accent)]"
                      />
                      <code className="font-mono text-[12.5px]">{s}</code>
                    </label>
                  );
                })}
              </div>
            </div>
            <ErrorBanner error={error} />
            <div className="flex justify-end">
              <Button type="submit" isDisabled={creating || !name || scopes.length === 0}>
                {creating ? "创建中…" : "创建令牌"}
              </Button>
            </div>
          </form>
        </SurfaceBody>
      </Surface>

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            现有令牌{items ? ` · ${items.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <Loading />
          ) : items.length === 0 ? (
            <EmptyState inset title="还没有令牌" description="创建一个 PAT 让 CLI 可以推/拉。" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-[13px]">
                <thead>
                  <tr className="border-b border-[var(--separator)] bg-[var(--surface-secondary)]/40 text-left">
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">名称</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">范围</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">创建</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">到期</th>
                    <th className="px-4 py-2"></th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((it) => (
                    <tr key={it.id} className="border-b border-[var(--separator)] last:border-0">
                      <td className="px-4 py-2 font-medium text-fg">{it.name}</td>
                      <td className="px-4 py-2">
                        <span className="flex flex-wrap gap-1">
                          {it.scopes.map((s) => (
                            <Pill key={s} tone="neutral">{s}</Pill>
                          ))}
                        </span>
                      </td>
                      <td className="px-4 py-2 text-[11.5px] text-muted">
                        <RelativeTime iso={it.created_at} />
                      </td>
                      <td className="px-4 py-2 text-[11.5px] text-muted">
                        {it.expires_at ? <RelativeTime iso={it.expires_at} /> : "永久"}
                      </td>
                      <td className="px-4 py-2 text-right">
                        <Button variant="danger-soft" size="sm" onPress={() => onRevoke(it.id)}>
                          <TrashIcon width={13} height={13} /> 吊销
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </SurfaceBody>
      </Surface>

      <Modal>
        <Modal.Backdrop isOpen={created !== null} onOpenChange={(o) => !o && setCreated(null)}>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>令牌已创建</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p className="mb-3 text-[13px] text-fg">
                  <strong>这是你最后一次看到原始令牌。</strong>
                  关闭窗口后将无法再次获取，只能吊销重建。
                </p>
                <pre className="m-0 overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] p-3 font-mono text-[12.5px] text-fg select-all">
                  {created?.token}
                </pre>
              </Modal.Body>
              <Modal.Footer>
                <Button
                  variant="outline"
                  onPress={async () => {
                    if (!created?.token) return;
                    if (!navigator.clipboard) {
                      alert("当前浏览器不支持自动复制；请手动选中令牌文本。");
                      return;
                    }
                    try {
                      await navigator.clipboard.writeText(created.token);
                      setCopied(true);
                      if (copyResetTimer.current !== null) clearTimeout(copyResetTimer.current);
                      copyResetTimer.current = setTimeout(() => setCopied(false), 1500);
                    } catch {
                      alert("复制失败，请手动选中令牌文本。");
                    }
                  }}
                >
                  <CopyIcon width={14} height={14} /> {copied ? "已复制" : "复制"}
                </Button>
                <Button onPress={() => setCreated(null)}>关闭</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </PageContainer>
  );
}
