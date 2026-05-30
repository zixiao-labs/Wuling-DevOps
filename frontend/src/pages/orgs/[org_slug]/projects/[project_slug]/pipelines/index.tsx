import { Button, Description, Input, Label, Modal, TextField } from "@heroui/react";
import PlayIcon from "@gravity-ui/icons/Play";
import Rocket from "@gravity-ui/icons/Rocket";
import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { pipelines as pipelinesApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { UserAvatar } from "@/components/user-avatar";
import { RunStatusBadge } from "@/components/pipeline-status";
import {
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { PipelineRun } from "@/api/types";

export default function PipelinesIndex() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [items, setItems] = useState<PipelineRun[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [showTrigger, setShowTrigger] = useState(false);

  function load() {
    setItems(null);
    setError(null);
    pipelinesApi
      .listRuns(org.slug, project.slug, { limit: 50 })
      .then(setItems)
      .catch((e) => setError(e as ApiError));
  }

  useEffect(load, [org.slug, project.slug]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/pipelines`;

  return (
    <PageContainer wide>
      <PageHeader
        title="Pipelines"
        description="工作流运行记录。push 与 PR 会自动触发；声明了 workflow_dispatch 的工作流可手动触发。"
        actions={
          <Button onPress={() => setShowTrigger(true)}>
            <PlayIcon width={14} height={14} /> 手动触发
          </Button>
        }
      />

      <ErrorBanner error={error} />

      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            运行{items ? ` · ${items.length}` : ""}
          </span>
          <span className="text-[11.5px] text-muted">按创建时间倒序</span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <SkeletonRows count={6} />
          ) : items.length === 0 ? (
            <EmptyState
              inset
              icon={<Rocket width={20} height={20} />}
              title="还没有运行"
              description="把工作流放在仓库的 .wuling/workflows/*.yml，push 后会自动触发。"
            />
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {items.map((run) => (
                <li key={run.id} className="px-4 py-2.5 hover:bg-[var(--surface-secondary)]/40">
                  <div className="flex items-baseline gap-3">
                    <RunStatusBadge status={run.status} />
                    <Link
                      to={`${base}/${run.id}`}
                      className="min-w-0 flex-1 truncate text-[13.5px] font-medium text-fg hover:text-[var(--accent)] hover:underline"
                    >
                      {run.workflow_name || run.workflow_path}
                    </Link>
                    <span className="shrink-0 font-mono text-[11.5px] text-muted">#{run.number}</span>
                  </div>
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[11.5px] text-muted">
                    <span className="inline-flex items-center gap-1 rounded-sm bg-[var(--surface-secondary)] px-1.5 py-0.5 font-mono">
                      {run.event}
                    </span>
                    {run.git_ref ? (
                      <span className="font-mono">{run.git_ref.replace("refs/heads/", "")}</span>
                    ) : null}
                    <span className="font-mono">{run.commit_sha.slice(0, 8)}</span>
                    {run.commit_message ? (
                      <span className="min-w-0 max-w-[28rem] truncate">{run.commit_message}</span>
                    ) : null}
                    {run.triggered_by ? (
                      <span className="inline-flex items-center gap-1.5">
                        <UserAvatar user={run.triggered_by} size={16} />
                        {run.triggered_by.username}
                      </span>
                    ) : null}
                    <span>
                      <RelativeTime iso={run.created_at} />
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </SurfaceBody>
      </Surface>

      <TriggerModal
        open={showTrigger}
        onClose={() => setShowTrigger(false)}
        onDone={() => {
          setShowTrigger(false);
          load();
        }}
      />
    </PageContainer>
  );
}

function TriggerModal({
  open,
  onClose,
  onDone,
}: {
  open: boolean;
  onClose: () => void;
  onDone: () => void;
}) {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [repo, setRepo] = useState("");
  const [ref, setRef] = useState("");
  const [workflow, setWorkflow] = useState(".wuling/workflows/ci.yml");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  async function submit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await pipelinesApi.trigger(org.slug, project.slug, {
        repo,
        ref: ref || undefined,
        workflow,
      });
      onDone();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal>
      <Modal.Backdrop isOpen={open} onOpenChange={(o) => !o && onClose()}>
        <Modal.Container>
          <Modal.Dialog>
            <Modal.Header>
              <Modal.Heading>手动触发运行</Modal.Heading>
            </Modal.Header>
            <form onSubmit={submit}>
              <Modal.Body>
                <div className="flex flex-col gap-3.5">
                  <TextField name="repo" value={repo} onChange={setRepo} isRequired>
                    <Label>仓库</Label>
                    <Input placeholder="repo-slug" />
                    <Description>本项目下的仓库 slug。</Description>
                  </TextField>
                  <TextField name="ref" value={ref} onChange={setRef}>
                    <Label>分支 / 引用</Label>
                    <Input placeholder="留空 = 默认分支" />
                  </TextField>
                  <TextField name="workflow" value={workflow} onChange={setWorkflow} isRequired>
                    <Label>工作流文件</Label>
                    <Input placeholder=".wuling/workflows/ci.yml" />
                    <Description>工作流需声明 workflow_dispatch 才能手动触发。</Description>
                  </TextField>
                  <ErrorBanner error={error} />
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="outline" type="button" onPress={onClose}>
                  取消
                </Button>
                <Button type="submit" isDisabled={busy || !repo || !workflow}>
                  {busy ? "触发中…" : "触发"}
                </Button>
              </Modal.Footer>
            </form>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
