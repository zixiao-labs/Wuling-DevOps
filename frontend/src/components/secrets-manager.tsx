/**
 * secrets-manager.tsx — reusable CRUD UI for encrypted secrets, used by both
 * the org-level and project-level secrets pages. Values are write-only: the
 * API never returns a value, so this only ever lists names + sets + deletes.
 */

import { Button, Description, Input, Label, TextField } from "@heroui/react";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import KeyIcon from "@gravity-ui/icons/Key";
import { useEffect, useRef, useState } from "react";

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
import type { Secret } from "@/api/types";

export interface SecretsManagerProps {
  title: string;
  description: React.ReactNode;
  list: () => Promise<Secret[]>;
  set: (name: string, value: string) => Promise<unknown>;
  remove: (name: string) => Promise<unknown>;
  /** Identity key so a scope switch refetches. */
  scopeKey: string;
}

export function SecretsManager({ title, description, list, set, remove, scopeKey }: SecretsManagerProps) {
  const [items, setItems] = useState<Secret[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const reqIdRef = useRef(0);

  function load() {
    // Guard against out-of-order responses: a slow request for the previous
    // scope must not overwrite the current one. Only the latest id wins.
    const reqId = ++reqIdRef.current;
    setItems(null);
    setError(null);
    list()
      .then((rows) => {
        if (reqId === reqIdRef.current) setItems(rows);
      })
      .catch((e) => {
        if (reqId === reqIdRef.current) setError(e as ApiError);
      });
  }

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(load, [scopeKey]);

  async function onSet(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await set(name, value);
      setName("");
      setValue("");
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(secretName: string) {
    if (!confirm(`删除机密 ${secretName}？引用它的工作流会立即取不到该值。`)) return;
    try {
      await remove(secretName);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <PageContainer>
      <PageHeader title={title} description={description} />

      <Surface className="mb-4">
        <SurfaceHeader title="新增 / 更新机密" />
        <SurfaceBody>
          <form onSubmit={onSet} className="flex flex-col gap-3.5">
            <TextField name="name" value={name} onChange={setName} isRequired>
              <Label>名称</Label>
              <Input placeholder="NPM_TOKEN" />
              <Description>仅限字母、数字、下划线，且不以数字开头（可作环境变量名）。</Description>
            </TextField>
            <TextField name="value" value={value} onChange={setValue} isRequired>
              <Label>值</Label>
              <Input placeholder="机密内容（保存后不可再读）" type="password" />
              <Description>用 AES-256-GCM 加密存储；API 永不回显明文。</Description>
            </TextField>
            <ErrorBanner error={error} />
            <div className="flex justify-end">
              <Button type="submit" isDisabled={busy || !name || !value}>
                {busy ? "保存中…" : "保存机密"}
              </Button>
            </div>
          </form>
        </SurfaceBody>
      </Surface>

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">现有机密{items ? ` · ${items.length}` : ""}</span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <Loading />
          ) : items.length === 0 ? (
            <EmptyState
              inset
              icon={<KeyIcon width={20} height={20} />}
              title="还没有机密"
              description="添加后即可在工作流里用 ${{ secrets.NAME }} 引用。"
            />
          ) : (
            <table className="w-full border-collapse text-[13px]">
              <thead>
                <tr className="border-b border-[var(--separator)] bg-[var(--surface-secondary)]/40 text-left">
                  <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">名称</th>
                  <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">更新</th>
                  <th className="px-4 py-2"></th>
                </tr>
              </thead>
              <tbody>
                {items.map((s) => (
                  <tr key={s.id} className="border-b border-[var(--separator)] last:border-0">
                    <td className="px-4 py-2 font-mono font-medium text-fg">{s.name}</td>
                    <td className="px-4 py-2 text-[11.5px] text-muted">
                      <RelativeTime iso={s.updated_at} />
                    </td>
                    <td className="px-4 py-2 text-right">
                      <Button variant="danger-soft" size="sm" onPress={() => onDelete(s.name)}>
                        <TrashIcon width={13} height={13} /> 删除
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
