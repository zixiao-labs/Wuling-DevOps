import {
  Button,
  Description,
  Input,
  Label,
  TextArea,
  TextField,
} from "@heroui/react";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { useEffect, useState } from "react";

import { sshKeys as keysApi } from "@/api/endpoints";
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
import type { SSHKey } from "@/api/types";

export default function SshKeysPage() {
  const [items, setItems] = useState<SSHKey[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [title, setTitle] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [creating, setCreating] = useState(false);

  function load(signal?: AbortSignal) {
    setError(null);
    keysApi
      .list()
      .then((res) => {
        if (!signal?.aborted) setItems(res);
      })
      .catch((e) => {
        if (signal?.aborted) return;
        setError(e as ApiError);
      });
  }

  useEffect(() => {
    const ac = new AbortController();
    load(ac.signal);
    return () => ac.abort();
  }, []);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    setError(null);
    try {
      await keysApi.create({ title, public_key: publicKey.trim() });
      setTitle("");
      setPublicKey("");
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  async function onRevoke(id: string) {
    if (!confirm("吊销这把公钥？依赖它推/拉的客户端会立即失败。")) return;
    try {
      await keysApi.revoke(id);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <PageContainer>
      <PageHeader
        title="SSH 公钥"
        description={
          <>
            注册公钥后即可用{" "}
            <code className="font-mono">ssh://git@&lt;host&gt;:2222/&lt;org&gt;/&lt;project&gt;/&lt;repo&gt;.git</code>{" "}
            推/拉。
          </>
        }
      />

      <Surface className="mb-4">
        <SurfaceHeader title="添加新公钥" />
        <SurfaceBody>
          <form onSubmit={onCreate} className="flex flex-col gap-3.5">
            <TextField name="title" value={title} onChange={setTitle} isRequired>
              <Label>标题</Label>
              <Input placeholder="laptop / ci-runner / …" />
            </TextField>
            <TextField name="public_key" value={publicKey} onChange={setPublicKey} isRequired>
              <Label>公钥</Label>
              <TextArea rows={4} placeholder="ssh-ed25519 AAAA…" className="font-mono text-[12.5px]" />
              <Description>
                粘贴 <code className="font-mono">~/.ssh/id_*.pub</code> 全部内容。支持
                ssh-rsa / ssh-ed25519 / ecdsa-sha2-*。
              </Description>
            </TextField>
            <ErrorBanner error={error} />
            <div className="flex justify-end">
              <Button type="submit" isDisabled={creating || !title || !publicKey}>
                {creating ? "添加中…" : "添加公钥"}
              </Button>
            </div>
          </form>
        </SurfaceBody>
      </Surface>

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            已注册公钥{items ? ` · ${items.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <Loading />
          ) : items.length === 0 ? (
            <EmptyState inset title="还没有公钥" description="添加一把公钥就能用 SSH 推/拉了。" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-[13px]">
                <thead>
                  <tr className="border-b border-[var(--separator)] bg-[var(--surface-secondary)]/40 text-left">
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">标题</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">指纹</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">添加</th>
                    <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">最近使用</th>
                    <th className="px-4 py-2"></th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((k) => (
                    <tr key={k.id} className="border-b border-[var(--separator)] last:border-0">
                      <td className="px-4 py-2 font-medium text-fg">{k.title}</td>
                      <td className="px-4 py-2"><code className="font-mono text-[11px] text-muted">{k.fingerprint}</code></td>
                      <td className="px-4 py-2 text-[11.5px] text-muted"><RelativeTime iso={k.created_at} /></td>
                      <td className="px-4 py-2 text-[11.5px] text-muted">
                        {k.last_used_at ? <RelativeTime iso={k.last_used_at} /> : "—"}
                      </td>
                      <td className="px-4 py-2 text-right">
                        <Button variant="danger-soft" size="sm" onPress={() => onRevoke(k.id)}>
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
    </PageContainer>
  );
}
