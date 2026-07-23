import { Button, Input, Label, ListBox, Select, TextArea, TextField } from "@heroui/react";
import Plus from "@gravity-ui/icons/Plus";
import { useEffect, useState } from "react";

import { testPlans as testPlansApi } from "@/api/endpoints";
import type { TestAutomation, TestCase, TestPlan, TestRunStatus, TestSuite } from "@/api/types";
import { ApiError } from "@/api/errors";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { SkeletonRows } from "@/components/loading";
import { Pill } from "@/components/page/badges";
import { PageContainer, PageHeader, Surface, SurfaceBody, SurfaceHeader } from "@/components/page/primitives";

type CreateMode = "plan" | "suite" | "case" | null;
const RUN_TONE: Record<TestRunStatus, "success" | "danger" | "warning" | "neutral"> = {
  passed: "success", failed: "danger", blocked: "warning", skipped: "neutral",
};

export default function TestPlansPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const [plans, setPlans] = useState<TestPlan[] | null>(null);
  const [suites, setSuites] = useState<TestSuite[]>([]);
  const [cases, setCases] = useState<TestCase[]>([]);
  const [planID, setPlanID] = useState("");
  const [suiteID, setSuiteID] = useState("");
  const [mode, setMode] = useState<CreateMode>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [expected, setExpected] = useState("");
  const [automation, setAutomation] = useState<TestAutomation>("manual");
  const [automationRef, setAutomationRef] = useState("");
  const [error, setError] = useState<ApiError | null>(null);

  function loadPlans() {
    testPlansApi.list(org.slug, project.slug)
      .then((next) => {
        setPlans(next);
        setPlanID((current) => current && next.some((item) => item.id === current) ? current : next[0]?.id ?? "");
      })
      .catch((err) => setError(err as ApiError));
  }
  useEffect(loadPlans, [org.slug, project.slug]);

  useEffect(() => {
    if (!planID) { setSuites([]); setSuiteID(""); return; }
    testPlansApi.listSuites(org.slug, project.slug, planID)
      .then((next) => {
        setSuites(next);
        setSuiteID((current) => current && next.some((item) => item.id === current) ? current : next[0]?.id ?? "");
      })
      .catch((err) => setError(err as ApiError));
  }, [org.slug, project.slug, planID]);

  function loadCases() {
    if (!planID || !suiteID) { setCases([]); return; }
    testPlansApi.listCases(org.slug, project.slug, planID, suiteID)
      .then(setCases)
      .catch((err) => setError(err as ApiError));
  }
  useEffect(loadCases, [org.slug, project.slug, planID, suiteID]);

  function resetForm() { setName(""); setDescription(""); setExpected(""); setAutomation("manual"); setAutomationRef(""); setMode(null); }

  async function create(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    try {
      if (mode === "plan") {
        const created = await testPlansApi.create(org.slug, project.slug, { name, description });
        resetForm(); loadPlans(); setPlanID(created.id);
      } else if (mode === "suite" && planID) {
        const created = await testPlansApi.createSuite(org.slug, project.slug, planID, { name, description });
        resetForm();
        const next = await testPlansApi.listSuites(org.slug, project.slug, planID);
        setSuites(next); setSuiteID(created.id);
      } else if (mode === "case" && planID && suiteID) {
        await testPlansApi.createCase(org.slug, project.slug, planID, suiteID, {
          title: name,
          steps: description ? description.split("\n").filter(Boolean).map((step, index) => ({ index: index + 1, action: step })) : [],
          expected,
          automation,
          automation_ref: automationRef,
        });
        resetForm(); loadCases();
      }
    } catch (err) { setError(err as ApiError); }
  }

  async function record(caseID: string, status: TestRunStatus) {
    try { await testPlansApi.recordRun(org.slug, project.slug, caseID, { status }); loadCases(); }
    catch (err) { setError(err as ApiError); }
  }

  return (
    <PageContainer wide>
      <PageHeader eyebrow="TEST · PLANS" title="Test Plans" description="管理测试计划、Suite、手工用例与 Lightning 自动化用例，并记录每次执行结果。" actions={<Button onPress={() => setMode("plan")}><Plus width={14} height={14} /> 新建 Test Plan</Button>} />
      <ErrorBanner error={error} />

      {mode ? (
        <Surface className="mb-4">
          <SurfaceHeader title={mode === "plan" ? "新建 Test Plan" : mode === "suite" ? "新建 Test Suite" : "添加 Test Case"} actions={<Button size="sm" variant="ghost" onPress={resetForm}>取消</Button>} />
          <SurfaceBody>
            <form onSubmit={create} className="grid gap-3 md:grid-cols-2">
              <TextField value={name} onChange={setName} isRequired><Label>{mode === "case" ? "用例标题" : "名称"}</Label><Input /></TextField>
              {mode === "case" ? (
                <Select value={automation} onChange={(value) => value && setAutomation(String(value) as TestAutomation)}>
                  <Label>执行方式</Label>
                  <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
                  <Select.Popover><ListBox>
                    <ListBox.Item id="manual" textValue="手工"><span>手工</span><ListBox.ItemIndicator /></ListBox.Item>
                    <ListBox.Item id="lightning" textValue="Lightning"><span>Lightning 自动化</span><ListBox.ItemIndicator /></ListBox.Item>
                  </ListBox></Select.Popover>
                </Select>
              ) : null}
              <TextField className="md:col-span-2" value={description} onChange={setDescription}>
                <Label>{mode === "case" ? "测试步骤（每行一步）" : "说明"}</Label><TextArea rows={mode === "case" ? 5 : 3} />
              </TextField>
              {mode === "case" ? <>
                <TextField value={expected} onChange={setExpected}><Label>预期结果</Label><Input /></TextField>
                <TextField value={automationRef} onChange={setAutomationRef}><Label>自动化引用</Label><Input placeholder="src/example.test.ts#case" /></TextField>
              </> : null}
              <div className="md:col-span-2 flex justify-end"><Button type="submit" isDisabled={!name.trim()}>创建</Button></div>
            </form>
          </SurfaceBody>
        </Surface>
      ) : null}

      {plans === null ? <Surface><SkeletonRows count={6} /></Surface> : plans.length === 0 ? (
        <Surface><EmptyState inset title="还没有 Test Plan" description="创建计划后，可按功能模块组织 Suite 与测试用例。" action={<Button onPress={() => setMode("plan")}>新建 Test Plan</Button>} /></Surface>
      ) : (
        <div className="grid min-h-[560px] grid-cols-[240px_260px_minmax(0,1fr)] overflow-hidden rounded-md border border-[var(--border)] bg-[var(--surface)]">
          <aside className="border-r border-[var(--border)]">
            <div className="flex h-10 items-center justify-between border-b border-[var(--border)] px-3 text-[12px] font-semibold">Test Plans <button className="text-[var(--accent)]" onClick={() => setMode("plan")}>＋</button></div>
            {plans.map((plan) => <button key={plan.id} className={`block w-full border-b border-[var(--separator)] px-3 py-2.5 text-left ${planID === plan.id ? "bg-[var(--surface-secondary)]" : "hover:bg-[var(--surface-secondary)]"}`} onClick={() => setPlanID(plan.id)}><div className="truncate text-[12.5px] font-medium">{plan.name}</div><div className="mt-0.5 text-[10.5px] text-muted">{plan.state}</div></button>)}
          </aside>
          <aside className="border-r border-[var(--border)]">
            <div className="flex h-10 items-center justify-between border-b border-[var(--border)] px-3 text-[12px] font-semibold">Test Suites <button className="text-[var(--accent)] disabled:opacity-40" disabled={!planID} onClick={() => setMode("suite")}>＋</button></div>
            {suites.map((suite) => <button key={suite.id} className={`block w-full border-b border-[var(--separator)] px-3 py-2.5 text-left ${suiteID === suite.id ? "bg-[var(--surface-secondary)]" : "hover:bg-[var(--surface-secondary)]"}`} onClick={() => setSuiteID(suite.id)}><div className="truncate text-[12.5px] font-medium">{suite.name}</div><div className="mt-0.5 truncate text-[10.5px] text-muted">{suite.description || "Static suite"}</div></button>)}
          </aside>
          <section>
            <div className="flex h-10 items-center justify-between border-b border-[var(--border)] px-3 text-[12px] font-semibold"><span>Test Cases · {cases.length}</span><Button size="sm" variant="ghost" isDisabled={!suiteID} onPress={() => setMode("case")}><Plus width={12} height={12} /> 添加用例</Button></div>
            {cases.length === 0 ? <div className="p-10 text-center text-[12px] text-muted">选择 Suite 或添加第一个 Test Case。</div> : cases.map((item) => (
              <article key={item.id} className="border-b border-[var(--separator)] px-4 py-3">
                <div className="flex items-start justify-between gap-3"><div><div className="text-[13px] font-medium">{item.title}</div><div className="mt-1 text-[11px] text-muted">{item.automation === "lightning" ? `⚡ ${item.automation_ref || "Lightning"}` : "手工执行"} · P{item.priority}</div></div>{item.last_run ? <Pill tone={RUN_TONE[item.last_run.status]}>{item.last_run.status}</Pill> : <Pill>未执行</Pill>}</div>
                <div className="mt-3 flex flex-wrap gap-1.5"><Button size="sm" variant="secondary" onPress={() => void record(item.id, "passed")}>Pass</Button><Button size="sm" variant="secondary" onPress={() => void record(item.id, "failed")}>Fail</Button><Button size="sm" variant="ghost" onPress={() => void record(item.id, "blocked")}>Blocked</Button><Button size="sm" variant="ghost" onPress={() => void record(item.id, "skipped")}>Skip</Button></div>
              </article>
            ))}
          </section>
        </div>
      )}
    </PageContainer>
  );
}
