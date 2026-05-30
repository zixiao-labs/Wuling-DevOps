import { secrets as secretsApi } from "@/api/endpoints";
import { SecretsManager } from "@/components/secrets-manager";
import { useOrgCtx, useProjectCtx } from "@/auth/org-context";

export default function ProjectSecretsPage() {
  const org = useOrgCtx();
  const project = useProjectCtx();
  return (
    <SecretsManager
      title="项目机密"
      description="仅本项目的流水线可用。与同名的组织机密冲突时，项目机密优先。需要维护者及以上权限。"
      scopeKey={`${org.slug}/${project.slug}`}
      list={() => secretsApi.listProject(org.slug, project.slug)}
      set={(n, v) => secretsApi.setProject(org.slug, project.slug, n, { value: v })}
      remove={(n) => secretsApi.deleteProject(org.slug, project.slug, n)}
    />
  );
}
