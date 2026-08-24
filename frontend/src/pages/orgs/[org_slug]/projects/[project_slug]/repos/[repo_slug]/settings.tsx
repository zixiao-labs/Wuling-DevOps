import {
  Alert,
  Button,
  Checkbox,
  Description,
  FieldError,
  Input,
  Label,
  Modal,
  Switch,
  TextField,
} from "@heroui/react";
import LogoGithub from "@gravity-ui/icons/LogoGithub";
import TrashBin from "@gravity-ui/icons/TrashBin";
import { useNavigate, useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { orgMembers, repositorySettings, repos as reposApi } from "@/api/endpoints";
import type { GitHubRepoLink, RepoSettings } from "@/api/types";
import { ApiError } from "@/api/errors";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { Pill } from "@/components/page/badges";
import {
  Breadcrumbs,
  PageContainer,
  PageHeader,
  Surface,
  SurfaceBody,
  SurfaceHeader,
} from "@/components/page/primitives";
import { parseGitHubRepository } from "@/features/repositories/github-link";

type Strategy = RepoSettings["merge_strategies"][number];

export default function RepositorySettingsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  const params = useParams();
  const navigate = useNavigate();
  const repo = params.repo_slug ?? "";

  const [settings, setSettings] = useState<RepoSettings | null>(null);
  const [topics, setTopics] = useState("");
  const [error, setError] = useState<ApiError | null>(null);
  const [saving, setSaving] = useState(false);

  const [canManage, setCanManage] = useState(false);
  const [githubLink, setGithubLink] = useState<GitHubRepoLink | null>(null);
  const [githubLoading, setGithubLoading] = useState(true);
  const [githubRepository, setGithubRepository] = useState("");
  const [installationID, setInstallationID] = useState("");
  const [githubError, setGithubError] = useState<ApiError | null>(null);
  const [linking, setLinking] = useState(false);
  const [linkSaved, setLinkSaved] = useState(false);

  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteConfirmation, setDeleteConfirmation] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<ApiError | null>(null);

  const base = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos/${encodeURIComponent(repo)}`;
  const reposPath = `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}/repos`;
  const parsedGitHubRepo = parseGitHubRepository(githubRepository);
  const numericInstallationID = Number(installationID);
  const installationIDIsValid =
    Number.isSafeInteger(numericInstallationID) && numericInstallationID > 0;

  useEffect(() => {
    let cancelled = false;
    setError(null);
    setSettings(null);
    repositorySettings
      .get(org.slug, project.slug, repo)
      .then((next) => {
        if (cancelled) return;
        setSettings(next);
        setTopics(next.topics.join(", "));
      })
      .catch((err) => {
        if (!cancelled) setError(err as ApiError);
      });
    return () => {
      cancelled = true;
    };
  }, [org.slug, project.slug, repo]);

  useEffect(() => {
    let cancelled = false;
    setGithubLoading(true);
    setGithubError(null);
    reposApi
      .githubLink(org.slug, project.slug, repo)
      .then((link) => {
        if (cancelled) return;
        setGithubLink(link);
        if (link.linked) {
          setGithubRepository(link.full_name ?? `${link.owner}/${link.name}`);
          setInstallationID(String(link.installation_id ?? ""));
        }
      })
      .catch((err) => {
        if (!cancelled) setGithubError(err as ApiError);
      })
      .finally(() => {
        if (!cancelled) setGithubLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [org.slug, project.slug, repo]);

  useEffect(() => {
    let cancelled = false;
    orgMembers
      .list(org.slug)
      .then((result) => {
        if (!cancelled) {
          setCanManage(result.role === "owner" || result.role === "maintainer");
        }
      })
      .catch(() => {
        if (!cancelled) setCanManage(false);
      });
    return () => {
      cancelled = true;
    };
  }, [org.slug]);

  function toggleStrategy(strategy: Strategy, enabled: boolean) {
    setSettings((current) => {
      if (!current) return current;
      const next = enabled
        ? [...current.merge_strategies, strategy]
        : current.merge_strategies.filter((item) => item !== strategy);
      return { ...current, merge_strategies: Array.from(new Set(next)) };
    });
  }

  async function save(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!settings) return;
    setSaving(true);
    setError(null);
    try {
      const next = await repositorySettings.update(org.slug, project.slug, repo, {
        ...settings,
        topics: topics
          .split(",")
          .map((item) => item.trim())
          .filter(Boolean),
      });
      setSettings(next);
      setTopics(next.topics.join(", "));
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setSaving(false);
    }
  }

  async function saveGitHubLink(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!parsedGitHubRepo || !installationIDIsValid || !canManage) return;
    setLinking(true);
    setLinkSaved(false);
    setGithubError(null);
    try {
      const link = await reposApi.putGithubLink(org.slug, project.slug, repo, {
        ...parsedGitHubRepo,
        installation_id: numericInstallationID,
      });
      setGithubLink(link);
      setGithubRepository(link.full_name ?? `${link.owner}/${link.name}`);
      setInstallationID(String(link.installation_id ?? ""));
      setLinkSaved(true);
    } catch (err) {
      setGithubError(err as ApiError);
    } finally {
      setLinking(false);
    }
  }

  function closeDeleteModal() {
    if (deleting) return;
    setDeleteOpen(false);
    setDeleteConfirmation("");
    setDeleteError(null);
  }

  async function deleteRepository(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (deleteConfirmation !== repo || !canManage) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await reposApi.remove(org.slug, project.slug, repo);
      navigate(reposPath, { replace: true });
    } catch (err) {
      setDeleteError(err as ApiError);
      setDeleting(false);
    }
  }

  if (!settings) {
    if (error) {
      return (
        <PageContainer>
          <ErrorBanner error={error} />
        </PageContainer>
      );
    }
    return <Loading />;
  }

  return (
    <PageContainer>
      <Breadcrumbs
        items={[
          {
            label: project.display_name || project.slug,
            to: `/orgs/${encodeURIComponent(org.slug)}/projects/${encodeURIComponent(project.slug)}`,
          },
          { label: repo, to: base },
          { label: "Settings" },
        ]}
      />
      <PageHeader
        eyebrow="REPOSITORY · SETUP"
        title={`${repo} 设置`}
        description="仓库功能、GitHub App 关联与生命周期管理。"
      />
      <ErrorBanner error={error} />

      <form onSubmit={save} className="grid gap-4">
        <Surface>
          <SurfaceHeader title="General" />
          <SurfaceBody className="grid max-w-2xl gap-4">
            <TextField
              value={settings.default_branch}
              onChange={(value) => setSettings({ ...settings, default_branch: value })}
            >
              <Label>默认分支</Label>
              <Input />
              <Description>需要指向仓库中已经存在的分支。</Description>
            </TextField>
            <TextField value={topics} onChange={setTopics}>
              <Label>Topics</Label>
              <Input placeholder="devops, go, react" />
              <Description>最多 20 个，用逗号分隔。</Description>
            </TextField>
            <Switch
              isSelected={settings.issues_enabled}
              onChange={(issues_enabled) => setSettings({ ...settings, issues_enabled })}
            >
              <Switch.Content>
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                启用 Issues
              </Switch.Content>
            </Switch>
            <Switch
              isSelected={settings.wiki_enabled}
              onChange={(wiki_enabled) => setSettings({ ...settings, wiki_enabled })}
            >
              <Switch.Content>
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                启用 Wiki
              </Switch.Content>
            </Switch>
          </SurfaceBody>
        </Surface>

        <Surface>
          <SurfaceHeader title="Pull / Merge Request" description="选择仓库允许使用的合并方式。" />
          <SurfaceBody className="grid gap-3">
            <StrategyCheckbox
              label="Create a merge commit"
              selected={settings.merge_strategies.includes("merge")}
              onChange={(value) => toggleStrategy("merge", value)}
            />
            <StrategyCheckbox
              label="Squash merging"
              selected={settings.merge_strategies.includes("squash")}
              onChange={(value) => toggleStrategy("squash", value)}
            />
            <StrategyCheckbox
              label="Rebase merging"
              selected={settings.merge_strategies.includes("rebase")}
              onChange={(value) => toggleStrategy("rebase", value)}
            />
            <Switch
              isSelected={settings.delete_branch_on_merge}
              onChange={(delete_branch_on_merge) =>
                setSettings({ ...settings, delete_branch_on_merge })
              }
            >
              <Switch.Content>
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                合并后自动删除源分支
              </Switch.Content>
            </Switch>
          </SurfaceBody>
        </Surface>

        <div>
          <Button
            type="submit"
            isPending={saving}
            isDisabled={settings.merge_strategies.length === 0}
          >
            {saving ? "保存中…" : "保存仓库设置"}
          </Button>
        </div>
      </form>

      <Surface className="mt-6">
        <SurfaceHeader
          title={
            <span className="inline-flex items-center gap-2">
              <LogoGithub width={16} height={16} /> GitHub 仓库
            </span>
          }
          description="将 GitHub App 的 webhook、同步与 Checks 事件映射到这个仓库。"
          actions={githubLink?.linked ? <Pill tone="success">已关联</Pill> : undefined}
        />
        <SurfaceBody>
          {githubLoading ? (
            <div className="py-2 text-[12.5px] text-muted">正在读取关联状态…</div>
          ) : (
            <form onSubmit={saveGitHubLink} className="grid max-w-2xl gap-4">
              {githubLink?.linked && githubLink.full_name ? (
                <div className="rounded-md border border-[var(--border)] bg-[var(--surface-secondary)] px-3 py-2 text-[12.5px] text-fg">
                  当前关联：
                  <a
                    href={`https://github.com/${githubLink.full_name}`}
                    target="_blank"
                    rel="noreferrer"
                    className="font-medium text-[var(--accent)] hover:underline"
                  >
                    {githubLink.full_name}
                  </a>
                </div>
              ) : null}
              <TextField
                name="github_repository"
                value={githubRepository}
                onChange={(value) => {
                  setGithubRepository(value);
                  setLinkSaved(false);
                }}
                isRequired
                isDisabled={!canManage}
                isInvalid={githubRepository.length > 0 && !parsedGitHubRepo}
              >
                <Label>GitHub 仓库</Label>
                <Input placeholder="owner/repository 或 GitHub URL" />
                <Description>支持 owner/name、HTTPS URL 或 SSH clone URL。</Description>
                <FieldError>请输入有效的 GitHub owner/name 或仓库 URL。</FieldError>
              </TextField>
              <TextField
                name="installation_id"
                type="number"
                value={installationID}
                onChange={(value) => {
                  setInstallationID(value);
                  setLinkSaved(false);
                }}
                isRequired
                isDisabled={!canManage}
                isInvalid={installationID.length > 0 && !installationIDIsValid}
              >
                <Label>GitHub App Installation ID</Label>
                <Input placeholder="12345678" inputMode="numeric" min={1} />
                <Description>
                  可从 GitHub App 安装设置页 URL 或 installation webhook payload 获取。
                </Description>
                <FieldError>Installation ID 必须是正整数。</FieldError>
              </TextField>
              {!canManage ? (
                <p className="text-[12.5px] text-muted">需要 Maintainer 或 Owner 权限才能修改关联。</p>
              ) : null}
              {linkSaved ? (
                <Alert status="success">
                  <Alert.Indicator />
                  <Alert.Content>
                    <Alert.Title>GitHub 仓库已关联</Alert.Title>
                    <Alert.Description>新的 webhook 和 Checks 事件会使用此映射。</Alert.Description>
                  </Alert.Content>
                </Alert>
              ) : null}
              <ErrorBanner error={githubError} />
              <div>
                <Button
                  type="submit"
                  isPending={linking}
                  isDisabled={!canManage || !parsedGitHubRepo || !installationIDIsValid}
                >
                  {linking ? "关联中…" : githubLink?.linked ? "更新关联" : "关联 GitHub 仓库"}
                </Button>
              </div>
            </form>
          )}
        </SurfaceBody>
      </Surface>

      <Surface className="mt-6 border-[color-mix(in_oklch,var(--danger)_55%,var(--border))]">
        <SurfaceHeader
          title="Danger Zone"
          description="删除后，仓库记录、Git 数据及其关联数据都无法恢复。"
        />
        <SurfaceBody className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-[13px] font-medium text-fg">永久删除此仓库</p>
            <p className="mt-0.5 text-[12px] text-muted">
              此操作需要 Maintainer 或 Owner 权限，并要求再次输入仓库 slug。
            </p>
          </div>
          <Button
            variant="danger-soft"
            isDisabled={!canManage}
            onPress={() => setDeleteOpen(true)}
          >
            <TrashBin width={14} height={14} /> 删除仓库
          </Button>
        </SurfaceBody>
      </Surface>

      <Modal>
        <Modal.Backdrop isOpen={deleteOpen} onOpenChange={(open) => !open && closeDeleteModal()}>
          <Modal.Container>
            <Modal.Dialog className="sm:max-w-[440px]">
              <Modal.Header>
                <Modal.Heading>删除仓库 {repo}？</Modal.Heading>
              </Modal.Header>
              <form onSubmit={deleteRepository}>
                <Modal.Body>
                  <div className="grid gap-4">
                    <p className="text-[13px] leading-relaxed text-fg">
                      这会永久删除仓库、Git 数据、Merge Requests、流水线记录以及 GitHub 关联。
                      <strong className="text-[var(--danger)]">此操作无法撤销。</strong>
                    </p>
                    <TextField
                      name="repo_confirmation"
                      value={deleteConfirmation}
                      onChange={setDeleteConfirmation}
                      isRequired
                      isDisabled={deleting}
                      isInvalid={deleteConfirmation.length > 0 && deleteConfirmation !== repo}
                    >
                      <Label>
                        输入 <code className="font-mono">{repo}</code> 以确认
                      </Label>
                      <Input autoComplete="off" />
                      <FieldError>仓库 slug 不匹配。</FieldError>
                    </TextField>
                    <ErrorBanner error={deleteError} />
                  </div>
                </Modal.Body>
                <Modal.Footer>
                  <Button
                    variant="outline"
                    type="button"
                    onPress={closeDeleteModal}
                    isDisabled={deleting}
                  >
                    取消
                  </Button>
                  <Button
                    variant="danger"
                    type="submit"
                    isPending={deleting}
                    isDisabled={deleteConfirmation !== repo}
                  >
                    {deleting ? "删除中…" : "永久删除仓库"}
                  </Button>
                </Modal.Footer>
              </form>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </PageContainer>
  );
}

function StrategyCheckbox({
  label,
  selected,
  onChange,
}: {
  label: string;
  selected: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <Checkbox isSelected={selected} onChange={onChange}>
      <Checkbox.Content>
        <Checkbox.Control>
          <Checkbox.Indicator />
        </Checkbox.Control>
        {label}
      </Checkbox.Content>
    </Checkbox>
  );
}
