import { secrets as secretsApi } from "@/api/endpoints";
import { SecretsManager } from "@/components/secrets-manager";
import { useOrgCtx } from "@/auth/org-context";

export default function OrgSecretsPage() {
  const org = useOrgCtx();
  return (
    <SecretsManager
      title="组织机密"
      description="本组织所有项目的流水线可用，也用于 runner-config.yaml 里引用的云凭证（credentials_secret）。需要维护者及以上权限。"
      scopeKey={org.slug}
      list={() => secretsApi.listOrg(org.slug)}
      set={(n, v) => secretsApi.setOrg(org.slug, n, { value: v })}
      remove={(n) => secretsApi.deleteOrg(org.slug, n)}
    />
  );
}
