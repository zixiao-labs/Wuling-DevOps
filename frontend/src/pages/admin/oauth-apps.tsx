import { Avatar, Button } from "@heroui/react";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { Navigate } from "chen-the-dawnstreak";
import { useCallback, useEffect, useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { OAuthAppView } from "@/api/types";
import { RequireAuth } from "@/auth/guards";
import { authStore } from "@/auth/store";
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
import { Pill } from "@/components/page/badges";

export default function AdminOAuthAppsPage() {
  return (
    <RequireAuth>
      <AdminInner />
    </RequireAuth>
  );
}

function AdminInner() {
  const { user } = authStore.useStore();
  const [items, setItems] = useState<OAuthAppView[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      const list = await oauthProvider.admin.listApps();
      setItems(list);
    } catch (err) {
      setError(err as ApiError);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!user?.is_admin) return <Navigate to="/orgs" replace />;

  async function toggleFirstParty(app: OAuthAppView) {
    try {
      await oauthProvider.admin.updateApp(app.id, { is_first_party: !app.is_first_party });
      refresh();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  async function onDelete(app: OAuthAppView) {
    if (
      !confirm(
        `删除 OAuth 应用「${app.name}」？所有用户对它的授权与 token 都会立刻失效。该操作不可逆。`,
      )
    ) {
      return;
    }
    try {
      await oauthProvider.admin.deleteApp(app.id);
      refresh();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <PageContainer wide>
      <PageHeader
        title="OAuth 应用（管理员视图）"
        description="浏览本实例所有 OAuth 应用，标记 / 取消「官方」徽章；可删除滥用应用。"
      />
      <ErrorBanner error={error} />
      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            全部应用{items ? ` · ${items.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <Loading />
          ) : items.length === 0 ? (
            <EmptyState inset title="该实例没有任何 OAuth 应用" />
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {items.map((a) => (
                <li key={a.id} className="flex items-start gap-3 px-4 py-3">
                  <Avatar size="md">
                    {a.logo_url && <Avatar.Image alt={a.name} src={a.logo_url} />}
                    <Avatar.Fallback>{a.name.slice(0, 2).toUpperCase()}</Avatar.Fallback>
                  </Avatar>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-[13.5px] font-medium text-fg">{a.name}</span>
                      <Pill tone={a.is_confidential ? "info" : "neutral"}>
                        {a.is_confidential ? "机密" : "公开"}
                      </Pill>
                      {a.is_first_party ? <Pill tone="success">官方</Pill> : null}
                    </div>
                    <div className="mt-0.5 truncate text-[11.5px] text-muted">
                      <code className="font-mono">{a.client_id}</code> · <RelativeTime iso={a.created_at} /> 创建
                    </div>
                    {a.default_scopes.length > 0 ? (
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        {a.default_scopes.map((s) => (
                          <Pill key={s} tone="neutral">{s}</Pill>
                        ))}
                      </div>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <Button size="sm" variant="outline" onPress={() => toggleFirstParty(a)}>
                      {a.is_first_party ? "取消官方" : "标为官方"}
                    </Button>
                    <Button size="sm" variant="danger-soft" onPress={() => onDelete(a)}>
                      <TrashIcon width={13} height={13} /> 删除
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
