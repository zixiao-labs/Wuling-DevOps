import { Button } from "@heroui/react";
import Ban from "@gravity-ui/icons/Ban";
import ArrowLeft from "@gravity-ui/icons/ArrowLeft";
import { Link, useParams } from "chen-the-dawnstreak";
import { useEffect, useRef, useState } from "react";

import { pipelines as pipelinesApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { RunStatusBadge, StepStatusIcon } from "@/components/pipeline-status";
import {
  PageContainer,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import type { PipelineJob, PipelineRun } from "@/api/types";

const ACTIVE = new Set(["queued", "running"]);

export default function RunDetail() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const runId = params.run_id ?? "";

  const [run, setRun] = useState<PipelineRun | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [selectedJob, setSelectedJob] = useState<string | null>(null);

  // Poll the run while it's active so statuses update live.
  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const poll = () => {
      pipelinesApi
        .getRun(org.slug, project.slug, runId)
        .then((r) => {
          if (cancelled) return;
          setRun(r);
          // Default-select the first job once loaded.
          setSelectedJob((cur) => cur ?? (r.jobs && r.jobs.length ? r.jobs[0]!.id : null));
          if (ACTIVE.has(r.status)) timer = setTimeout(poll, 2500);
        })
        .catch((e) => !cancelled && setError(e as ApiError));
    };
    poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [org.slug, project.slug, runId]);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/pipelines`;

  async function onCancel() {
    if (!run || !confirm("取消这次运行？运行中的 job 会尽快中止。")) return;
    try {
      await pipelinesApi.cancelRun(org.slug, project.slug, run.id);
    } catch (e) {
      setError(e as ApiError);
    }
  }

  if (error) {
    return (
      <PageContainer>
        <ErrorBanner error={error} />
      </PageContainer>
    );
  }
  if (!run) return <Loading />;

  const jobs = run.jobs ?? [];
  const active = ACTIVE.has(run.status);

  return (
    <PageContainer wide>
      <div className="mb-3">
        <Link to={base} className="inline-flex items-center gap-1 text-[12px] text-muted hover:text-fg">
          <ArrowLeft width={13} height={13} /> 返回 Pipelines
        </Link>
      </div>

      <Surface className="mb-4">
        <SurfaceBody>
          <div className="flex flex-wrap items-center gap-3">
            <RunStatusBadge status={run.status} />
            <h1 className="m-0 min-w-0 flex-1 truncate text-[16px] font-semibold text-fg">
              {run.workflow_name || run.workflow_path}
              <span className="ml-2 font-mono text-[12px] font-normal text-muted">#{run.number}</span>
            </h1>
            {active ? (
              <Button variant="danger-soft" size="sm" onPress={onCancel}>
                <Ban width={13} height={13} /> 取消运行
              </Button>
            ) : null}
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[11.5px] text-muted">
            <span className="font-mono">{run.event}</span>
            {run.git_ref ? <span className="font-mono">{run.git_ref.replace("refs/heads/", "")}</span> : null}
            <span className="font-mono">{run.commit_sha.slice(0, 10)}</span>
            <span>创建于 <RelativeTime iso={run.created_at} /></span>
            {run.triggered_by ? <span>由 {run.triggered_by.username} 触发</span> : null}
          </div>
        </SurfaceBody>
      </Surface>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[18rem_1fr]">
        {/* Jobs / steps */}
        <Surface>
          <SurfaceHeader dense>
            <span className="text-[12px] font-medium text-fg">Jobs · {jobs.length}</span>
          </SurfaceHeader>
          <SurfaceBody noPad>
            <ul className="list-none m-0 p-0 divide-y divide-[var(--separator)]">
              {jobs.map((job) => (
                <JobRow
                  key={job.id}
                  job={job}
                  selected={selectedJob === job.id}
                  onSelect={() => setSelectedJob(job.id)}
                />
              ))}
            </ul>
          </SurfaceBody>
        </Surface>

        {/* Logs */}
        <Surface>
          <SurfaceHeader dense>
            <span className="text-[12px] font-medium text-fg">日志</span>
            {selectedJob ? (
              <span className="text-[11.5px] text-muted">
                {jobs.find((j) => j.id === selectedJob)?.name}
              </span>
            ) : null}
          </SurfaceHeader>
          <SurfaceBody noPad>
            {selectedJob ? (
              <LogViewer key={selectedJob} jobId={selectedJob} orgSlug={org.slug} projectSlug={project.slug} />
            ) : (
              <div className="p-4 text-[12px] text-muted">选择一个 job 查看日志。</div>
            )}
          </SurfaceBody>
        </Surface>
      </div>
    </PageContainer>
  );
}

function JobRow({
  job,
  selected,
  onSelect,
}: {
  job: PipelineJob;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <li>
      <button
        onClick={onSelect}
        className={[
          "flex w-full items-center gap-2 px-3 py-2 text-left text-[13px]",
          selected ? "bg-[var(--surface-secondary)]" : "hover:bg-[var(--surface-secondary)]/50",
        ].join(" ")}
      >
        <RunStatusBadge status={job.status} />
        <span className="min-w-0 flex-1 truncate font-medium text-fg">{job.name}</span>
        <span className="shrink-0 rounded-sm bg-[var(--surface-secondary)] px-1.5 py-0.5 font-mono text-[10px] text-muted">
          {job.resource_tier}
        </span>
      </button>
      {job.steps && job.steps.length > 0 ? (
        <ul className="list-none m-0 px-3 pb-2 pt-0">
          {job.steps.map((s) => (
            <li key={s.id} className="flex items-center gap-2 py-0.5 pl-5 text-[12px] text-muted">
              <StepStatusIcon status={s.status} />
              <span className="min-w-0 truncate">{s.name}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </li>
  );
}

/**
 * LogViewer range-polls a job's log, appending new bytes each tick until the
 * job is done. We poll (rather than SSE) because EventSource can't carry the
 * bearer token; the offset cursor keeps it incremental and cheap.
 */
function LogViewer({
  jobId,
  orgSlug,
  projectSlug,
}: {
  jobId: string;
  orgSlug: string;
  projectSlug: string;
}) {
  const [text, setText] = useState("");
  const [done, setDone] = useState(false);
  const offsetRef = useRef(0);

  useEffect(() => {
    setText("");
    setDone(false);
    offsetRef.current = 0;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const ac = new AbortController();

    const poll = () => {
      pipelinesApi
        .jobLogs(orgSlug, projectSlug, jobId, offsetRef.current, 0, ac.signal)
        .then((chunk) => {
          if (cancelled) return;
          if (chunk.data) {
            setText((t) => t + chunk.data);
            offsetRef.current = chunk.offset;
          }
          if (chunk.is_done) {
            setDone(true);
            return;
          }
          timer = setTimeout(poll, 1500);
        })
        .catch(() => {
          if (!cancelled) timer = setTimeout(poll, 3000);
        });
    };
    poll();
    return () => {
      cancelled = true;
      ac.abort();
      if (timer) clearTimeout(timer);
    };
  }, [jobId, orgSlug, projectSlug]);

  return (
    <pre className="m-0 max-h-[28rem] overflow-auto bg-[#0b0d10] p-3 font-mono text-[12px] leading-relaxed text-[#d4d7dd]">
      {text || (done ? "（无日志输出）" : "等待日志…")}
    </pre>
  );
}
