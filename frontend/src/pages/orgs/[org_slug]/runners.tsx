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
import Cpu from "@gravity-ui/icons/Cpu";
import { useEffect, useRef, useState } from "react";

import { runners as runnersApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { Pill } from "@/components/page/badges";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { useOrgCtx } from "@/auth/org-context";
import type { ResourceTier, Runner } from "@/api/types";

const TIERS: ResourceTier[] = ["low", "medium", "high"];

function statusTone(s: Runner["status"]): "success" | "warning" | "neutral" {
  if (s === "idle") return "success";
  if (s === "busy") return "warning";
  return "neutral";
}
function statusLabel(s: Runner["status"]): string {
  return s === "idle" ? "空闲" : s === "busy" ? "忙碌" : "离线";
}

export default function RunnersPage() {
  const org = useOrgCtx();
  const [items, setItems] = useState<Runner[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [labels, setLabels] = useState("linux,docker");
  const [tier, setTier] = useState<ResourceTier>("medium");
  const [creating, setCreating] = useState(false);
  const [token, setToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  function load() {
    setError(null);
    runnersApi
      .list(org.slug)
      .then(setItems)
      .catch((e) => setError(e as ApiError));
  }

  useEffect(() => {
    load();
    return () => {
      if (copyTimer.current) clearTimeout(copyTimer.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [org.slug]);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    setError(null);
    try {
      const labelList = labels.split(",").map((s) => s.trim()).filter(Boolean);
      const res = await runnersApi.createRegistrationToken(org.slug, {
        labels: labelList,
        resource_tier: tier,
      });
      setToken(res.token);
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  async function onDelete(id: string, name: string) {
    if (!confirm(`移除 runner ${name}？正在运行的 job 会被回收重排。`)) return;
    try {
      await runnersApi.delete(org.slug, id);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  const serverURL = typeof window !== "undefined" ? window.location.origin : "https://wuling.example.com";
  const command = `wuling-runner \\\n  --server-url ${serverURL} \\\n  --registration-token ${token ?? "<TOKEN>"}`;

  return (
    <PageContainer>
      <PageHeader
        title="Runners"
        description="本组织的 CI 执行器。手动注册一台静态 runner，或在 config 仓库的 runner-config.yaml 里配置云自动扩缩容。"
      />

      <Surface className="mb-4">
        <SurfaceHeader title="注册静态 Runner" />
        <SurfaceBody>
          <form onSubmit={onCreate} className="flex flex-col gap-3.5">
            <TextField name="labels" value={labels} onChange={setLabels}>
              <Label>标签</Label>
              <Input placeholder="linux,docker" />
              <Description>逗号分隔。job 的 runs-on 标签需是 runner 标签的子集才会派发。</Description>
            </TextField>
            <div>
              <div className="mb-1.5 text-[12.5px] font-medium text-fg">资源档位</div>
              <div className="inline-flex h-8 items-center overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)]">
                {TIERS.map((t, i) => {
                  const active = t === tier;
                  return (
                    <button
                      key={t}
                      type="button"
                      onClick={() => setTier(t)}
                      className={[
                        "h-full px-3 text-[12px]",
                        i > 0 ? "border-l border-[var(--border)]" : "",
                        active
                          ? "bg-[var(--surface-secondary)] font-medium text-fg"
                          : "text-fg/70 hover:bg-[var(--surface-secondary)] hover:text-fg",
                      ].join(" ")}
                    >
                      {t}
                    </button>
                  );
                })}
              </div>
            </div>
            <ErrorBanner error={error} />
            <div className="flex justify-end">
              <Button type="submit" isDisabled={creating}>
                {creating ? "生成中…" : "生成注册令牌"}
              </Button>
            </div>
          </form>
        </SurfaceBody>
      </Surface>

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">已注册 Runner{items ? ` · ${items.length}` : ""}</span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <Loading />
          ) : items.length === 0 ? (
            <EmptyState
              inset
              icon={<Cpu width={20} height={20} />}
              title="还没有 Runner"
              description="生成注册令牌并在一台装有容器运行时的机器上启动 wuling-runner。"
            />
          ) : (
            <table className="w-full border-collapse text-[13px]">
              <thead>
                <tr className="border-b border-[var(--separator)] bg-[var(--surface-secondary)]/40 text-left">
                  <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">名称</th>
                  <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">状态</th>
                  <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">档位</th>
                  <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">标签</th>
                  <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">来源</th>
                  <th className="px-4 py-2 text-[11.5px] uppercase tracking-wider text-muted">心跳</th>
                  <th className="px-4 py-2"></th>
                </tr>
              </thead>
              <tbody>
                {items.map((r) => (
                  <tr key={r.id} className="border-b border-[var(--separator)] last:border-0">
                    <td className="px-4 py-2 font-medium text-fg">{r.name}</td>
                    <td className="px-4 py-2">
                      <Pill tone={statusTone(r.status)}>{statusLabel(r.status)}</Pill>
                    </td>
                    <td className="px-4 py-2 font-mono text-[12px] text-muted">{r.resource_tier}</td>
                    <td className="px-4 py-2">
                      <span className="flex flex-wrap gap-1">
                        {r.labels.map((l) => (
                          <Pill key={l} tone="neutral">{l}</Pill>
                        ))}
                      </span>
                    </td>
                    <td className="px-4 py-2 text-[12px] text-muted">
                      {r.provider === "static" ? "手动" : `${r.provider}${r.ephemeral ? " · 临时" : ""}`}
                    </td>
                    <td className="px-4 py-2 text-[11.5px] text-muted">
                      {r.last_seen_at ? <RelativeTime iso={r.last_seen_at} /> : "—"}
                    </td>
                    <td className="px-4 py-2 text-right">
                      <Button variant="danger-soft" size="sm" onPress={() => onDelete(r.id, r.name)}>
                        <TrashIcon width={13} height={13} /> 移除
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </SurfaceBody>
      </Surface>

      <Modal>
        <Modal.Backdrop
          isOpen={token !== null}
          onOpenChange={(o) => {
            if (!o) {
              setToken(null);
              load();
            }
          }}
        >
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>注册令牌已生成</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p className="mb-3 text-[13px] text-fg">
                  <strong>令牌只显示一次。</strong>在一台装有容器运行时（Docker/Podman）和 git
                  的机器上运行：
                </p>
                <pre className="m-0 overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] p-3 font-mono text-[12px] text-fg select-all whitespace-pre-wrap">
                  {command}
                </pre>
              </Modal.Body>
              <Modal.Footer>
                <Button
                  variant="outline"
                  onPress={async () => {
                    try {
                      await navigator.clipboard?.writeText(command);
                      setCopied(true);
                      if (copyTimer.current) clearTimeout(copyTimer.current);
                      copyTimer.current = setTimeout(() => setCopied(false), 1500);
                    } catch {
                      alert("复制失败，请手动选中。");
                    }
                  }}
                >
                  <CopyIcon width={14} height={14} /> {copied ? "已复制" : "复制命令"}
                </Button>
                <Button
                  onPress={() => {
                    setToken(null);
                    load();
                  }}
                >
                  完成
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </PageContainer>
  );
}
