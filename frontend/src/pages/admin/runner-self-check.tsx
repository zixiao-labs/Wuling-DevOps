import {
  Alert,
  AlertDialog,
  Button,
  Description,
  Input,
  Label,
  ProgressBar,
  TextField,
} from "@heroui/react";
import { Navigate } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { admin } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type {
  RunnerSelfCheck,
  RunnerSelfCheckCheckStatus,
  RunnerSelfCheckPool,
} from "@/api/types";
import { RequireAuth } from "@/auth/guards";
import { authStore } from "@/auth/store";
import { EmptyState } from "@/components/empty-state";
import { RelativeTime } from "@/components/relative-time";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";

export default function AdminRunnerSelfCheckPage() {
  return (
    <RequireAuth>
      <AdminRunnerSelfCheck />
    </RequireAuth>
  );
}

function AdminRunnerSelfCheck() {
  const { user } = authStore.useStore();
  const [orgSlug, setOrgSlug] = useState("");
  const [poolNamesInput, setPoolNamesInput] = useState("");
  const [checks, setChecks] = useState<RunnerSelfCheck[]>([]);
  const [blockedPools, setBlockedPools] = useState<RunnerSelfCheckPool[]>([]);
  const [configCheck, setConfigCheck] = useState<{
    status: RunnerSelfCheckCheckStatus;
    message: string;
  } | null>(null);
  const [loadAttempted, setLoadAttempted] = useState(false);
  const [loading, setLoading] = useState(false);
  const [running, setRunning] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [apiError, setApiError] = useState<ApiError | null>(null);

  const normalizedOrgSlug = orgSlug.trim();
  const poolNames = parsePoolNames(poolNamesInput);
  const hasActiveCheck = checks.some((check) => isActiveLifecycle(check));

  useEffect(() => {
    if (!normalizedOrgSlug || !hasActiveCheck) return;
    const timer = window.setInterval(() => {
      void admin.runnerSelfChecks.list(normalizedOrgSlug).then(setChecks).catch(() => undefined);
    }, 5000);
    return () => window.clearInterval(timer);
  }, [hasActiveCheck, normalizedOrgSlug]);

  if (!user) return null;
  // This is only a UX gate. The API is mounted below the server's fresh
  // active-is_admin middleware and remains the authorization boundary.
  if (!user.is_admin) return <Navigate to="/orgs" replace />;

  async function loadChecks() {
    if (!normalizedOrgSlug) {
      setFormError("请输入要检查的组织 slug。");
      return;
    }
    setFormError(null);
    setApiError(null);
    setLoading(true);
    setLoadAttempted(true);
    try {
      setChecks(await admin.runnerSelfChecks.list(normalizedOrgSlug));
    } catch (err) {
      setApiError(err as ApiError);
      setChecks([]);
    } finally {
      setLoading(false);
    }
  }

  function requestRun(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!normalizedOrgSlug) {
      setFormError("请输入要检查的组织 slug。");
      return;
    }
    setFormError(null);
    setApiError(null);
    setConfirmOpen(true);
  }

  async function runSelfCheck() {
    setConfirmOpen(false);
    setFormError(null);
    setApiError(null);
    setRunning(true);
    try {
      const result = await admin.runnerSelfChecks.run({
        org_slug: normalizedOrgSlug,
        pool_names: poolNames.length > 0 ? poolNames : undefined,
      });
      setConfigCheck(result.config_check);
      setBlockedPools(result.blocked_pools ?? []);
      setChecks((current) => mergeChecks(result.checks, current));
      setLoadAttempted(true);
    } catch (err) {
      setApiError(err as ApiError);
    } finally {
      setRunning(false);
    }
  }

  return (
    <PageContainer wide>
      <PageHeader
        title="Runner 自检"
        description="以全局管理员身份运行可审计的真实 autoscaler 探针。"
        actions={
          <Button
            size="sm"
            variant="outline"
            onPress={() => void loadChecks()}
            isDisabled={loading || running || !normalizedOrgSlug}
          >
            {loading ? "读取中…" : "查看最近记录"}
          </Button>
        }
      />

      <Alert status="warning" className="mb-4">
        <Alert.Indicator />
        <Alert.Content>
          <Alert.Title>会创建临时云实例并产生费用</Alert.Title>
          <Alert.Description>
            每个通过本地前置检查的 pool 都会排队一个独立、一次性的 Runner VM。它会验证命令执行、非 OS
            数据盘启动（如已配置）、一次性环境变量注入和流式日志脱敏，然后自动销毁。不会读取或注入组织已有
            Secret；清理失败会保留可审计记录并继续重试。
          </Alert.Description>
        </Alert.Content>
      </Alert>

      <Surface className="mb-4">
        <SurfaceHeader
          dense
          title={<span className="text-[12px] font-medium text-fg">启动真实 Runner 自检</span>}
          description={<span className="text-[11.5px] text-muted">记录持久化保存；页面会在 VM 生命周期进行时自动刷新。</span>}
        />
        <SurfaceBody>
          <form onSubmit={requestRun} className="flex flex-col gap-3.5">
            <div className="grid gap-3 md:grid-cols-2">
              <TextField name="org_slug" value={orgSlug} onChange={(value) => {
                setOrgSlug(value);
                setChecks([]);
                setBlockedPools([]);
                setConfigCheck(null);
                setLoadAttempted(false);
              }}>
                <Label>组织 slug</Label>
                <Input placeholder="例如：platform" autoComplete="off" />
                <Description>全局管理员可以检查任意组织；这不要求组织成员身份。</Description>
              </TextField>
              <TextField name="pool_names" value={poolNamesInput} onChange={setPoolNamesInput}>
                <Label>Pool 名称（可选）</Label>
                <Input placeholder="aws-linux, aliyun-windows" autoComplete="off" />
                <Description>以逗号或换行分隔。留空会为所有通过 preflight 的 pool 创建一次性探针。</Description>
              </TextField>
            </div>

            {formError ? (
              <Alert status="danger">
                <Alert.Indicator />
                <Alert.Content>
                  <Alert.Title>无法开始检查</Alert.Title>
                  <Alert.Description>{formError}</Alert.Description>
                </Alert.Content>
              </Alert>
            ) : null}

            {apiError ? (
              <Alert status="danger">
                <Alert.Indicator />
                <Alert.Content>
                  <Alert.Title>请求失败</Alert.Title>
                  <Alert.Description>{apiError.message}</Alert.Description>
                </Alert.Content>
              </Alert>
            ) : null}

            {running ? (
              <ProgressBar isIndeterminate aria-label="正在排队 Runner 自检">
                <ProgressBar.Track>
                  <ProgressBar.Fill />
                </ProgressBar.Track>
              </ProgressBar>
            ) : hasActiveCheck ? (
              <ProgressBar isIndeterminate aria-label="Runner 自检正在执行">
                <ProgressBar.Track>
                  <ProgressBar.Fill />
                </ProgressBar.Track>
              </ProgressBar>
            ) : null}

            <div className="flex flex-wrap justify-end gap-2">
              <Button type="submit" isDisabled={running || loading}>
                {running ? "正在排队…" : "启动真实自检"}
              </Button>
            </div>
          </form>
        </SurfaceBody>
      </Surface>

      {configCheck ? (
        <Alert status={alertTone(configCheck.status)} className="mb-4">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>配置前置检查：{statusLabel(configCheck.status)}</Alert.Title>
            <Alert.Description>{configCheck.message}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      {blockedPools.length > 0 ? (
        <Surface className="mb-4">
          <SurfaceHeader
            dense
            title={<span className="text-[12px] font-medium text-fg">未启动的 pool</span>}
            description={<span className="text-[11.5px] text-muted">这些 pool 未通过本地前置检查，因此没有创建 VM。</span>}
          />
          <SurfaceBody className="flex flex-col gap-2">
            {blockedPools.map((pool) => (
              <div key={pool.pool_name} className="rounded-md border border-[var(--border)] px-3 py-2 text-[12px]">
                <div className="font-medium text-fg">{pool.pool_name}</div>
                <ul className="mt-1.5 m-0 list-none space-y-1 p-0">
                  {pool.checks.filter((item) => item.status !== "passed").map((item) => (
                    <li key={item.name} className="flex gap-1.5 text-muted">
                      <CheckBadge status={item.status} />
                      <span>{item.message}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </SurfaceBody>
        </Surface>
      ) : null}

      {checks.length > 0 ? (
        <div className="flex flex-col gap-4">
          {checks.map((check) => (
            <SelfCheckRecord key={check.id} check={check} />
          ))}
        </div>
      ) : loadAttempted && !loading ? (
        <Surface>
          <SurfaceBody>
            <EmptyState inset title="该组织还没有持久化的自检记录" />
          </SurfaceBody>
        </Surface>
      ) : null}

      <AlertDialog>
        <AlertDialog.Backdrop isOpen={confirmOpen} onOpenChange={setConfirmOpen}>
          <AlertDialog.Container>
            <AlertDialog.Dialog className="sm:max-w-[460px]">
              <AlertDialog.CloseTrigger />
              <AlertDialog.Header>
                <AlertDialog.Icon status="warning" />
                <AlertDialog.Heading>启动真实 Runner 自检？</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>
                  将检查组织 <code className="font-mono">{normalizedOrgSlug}</code>
                  {poolNames.length > 0 ? ` 的 ${poolNames.length} 个指定 pool` : " 的全部已配置 pool"}。
                </p>
                <p className="mt-2 text-muted">
                  每个通过前置检查的 pool 都会创建一台一次性云 VM，产生相应的实例、磁盘和网络费用。系统会等待
                  Runner、执行命令与一次性 Secret/日志脱敏探针，并在完成或失败后自动回收。若回收失败，记录会显示
                  待重试状态。
                </p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="outline" isDisabled={running}>
                  取消
                </Button>
                <Button onPress={() => void runSelfCheck()} isDisabled={running}>
                  确认并创建临时 VM
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </PageContainer>
  );
}

function SelfCheckRecord({ check }: { check: RunnerSelfCheck }) {
  const stateTone = lifecycleTone(check.state);
  return (
    <Surface>
      <SurfaceHeader
        dense
        title={
          <span className="text-[12px] font-medium text-fg">
            {check.pool_name} · {lifecycleLabel(check.state)}
          </span>
        }
        description={
          <span className="text-[11.5px] text-muted">
            <RelativeTime iso={check.created_at} /> · {check.provider} · {check.os}
          </span>
        }
      />
      <SurfaceBody className="flex flex-col gap-3">
        <Alert status={stateTone}>
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>生命周期：{check.phase} · {lifecycleLabel(check.state)}</Alert.Title>
            <Alert.Description>{check.summary || lifecycleDescription(check.state)}</Alert.Description>
          </Alert.Content>
        </Alert>

        <div className="rounded-md border border-[var(--border)] bg-[var(--surface-secondary)]/30 px-3 py-2 text-[12px] text-muted">
          {check.runner_id ? <>Runner：<span className="font-mono text-fg">{check.runner_id}</span> · </> : null}
          {check.external_id ? <>实例：<span className="font-mono text-fg">{check.external_id}</span> · </> : null}
          清理尝试：<span className="font-medium text-fg">{check.cleanup_attempts}</span>
          {check.next_cleanup_at ? <> · 下次重试：<RelativeTime iso={check.next_cleanup_at} /></> : null}
        </div>

        <ul className="m-0 list-none space-y-1.5 p-0 text-[12.5px]">
          {check.checks.map((item) => (
            <li key={item.name} className="flex gap-1.5">
              <CheckBadge status={item.status} />
              <span className="text-fg">{item.message}</span>
            </li>
          ))}
        </ul>
      </SurfaceBody>
    </Surface>
  );
}

function CheckBadge({ status }: { status: RunnerSelfCheckCheckStatus }) {
  return (
    <span className={`shrink-0 font-medium ${statusClass(status)}`}>
      {statusLabel(status)}
    </span>
  );
}

function parsePoolNames(value: string): string[] {
  return [...new Set(value.split(/[\n,]/).map((name) => name.trim()).filter(Boolean))];
}

function statusLabel(status: RunnerSelfCheckCheckStatus): string {
  switch (status) {
    case "passed":
      return "通过";
    case "failed":
      return "失败";
    case "unsupported":
      return "不支持";
    case "error":
      return "无法确认";
    case "not_run":
      return "未执行";
  }
}

function alertTone(status: RunnerSelfCheckCheckStatus): "success" | "danger" | "warning" | "accent" {
  if (status === "passed") return "success";
  if (status === "unsupported" || status === "not_run") return "warning";
  if (status === "error") return "accent";
  return "danger";
}

function statusClass(status: RunnerSelfCheckCheckStatus): string {
  if (status === "passed") return "text-[var(--success)]";
  if (status === "failed") return "text-[var(--danger)]";
  if (status === "unsupported" || status === "not_run") return "text-[var(--warning)]";
  return "text-[var(--accent)]";
}

function mergeChecks(incoming: RunnerSelfCheck[], current: RunnerSelfCheck[]): RunnerSelfCheck[] {
  const byID = new Map(current.map((check) => [check.id, check]));
  for (const check of incoming) byID.set(check.id, check);
  return [...byID.values()].sort((a, b) => b.created_at.localeCompare(a.created_at));
}

function isActiveLifecycle(check: RunnerSelfCheck): boolean {
  // Probe success/failure still waits for VM cleanup. Keep polling until the
  // durable cleaned_at stamp arrives (MarkStartFailed also stamps it so
  // pre-VM failures do not spin forever).
  return !check.cleaned_at;
}

function lifecycleLabel(state: RunnerSelfCheck["state"]): string {
  switch (state) {
    case "preflight":
      return "前置检查";
    case "queued":
      return "等待置备";
    case "provisioning":
      return "正在创建 VM";
    case "waiting_for_runner":
      return "等待 Runner 注册";
    case "executing":
      return "正在执行探针";
    case "cleanup_pending":
      return "等待清理重试";
    case "succeeded":
      return "探针通过，等待清理";
    case "failed":
      return "探针失败，等待清理";
    case "cleaned":
      return "已清理";
    case "not_run":
      return "未运行";
  }
}

function lifecycleDescription(state: RunnerSelfCheck["state"]): string {
  if (state === "queued") return "已创建内部隔离任务，等待 autoscaler 置备临时 VM。";
  if (state === "waiting_for_runner") return "云实例已创建，正在等待一次性 Runner 注册。";
  if (state === "executing") return "Runner 正在验证命令执行、一次性环境变量和日志脱敏。";
  if (state === "cleanup_pending") return "临时资源尚未完全删除；控制面会继续重试。";
  if (state === "cleaned") return "临时 Runner 和云实例已回收，一次性探针值已销毁。";
  return "正在等待下一阶段。";
}

function lifecycleTone(state: RunnerSelfCheck["state"]): "success" | "danger" | "warning" | "accent" {
  if (state === "cleaned" || state === "succeeded") return "success";
  if (state === "failed") return "danger";
  if (state === "cleanup_pending" || state === "not_run") return "warning";
  return "accent";
}
