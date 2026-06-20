import { Avatar, Button } from "@heroui/react";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { useEffect, useRef, useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { AuthorizationView } from "@/api/types";
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

export default function AuthorizedAppsPage() {
  const [items, setItems] = useState<AuthorizationView[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const loadController = useRef<AbortController | null>(null);

  function load() {
    loadController.current?.abort();
    const ac = new AbortController();
    loadController.current = ac;
    setError(null);
    oauthProvider.authorizations
      .list()
      .then((res) => {
        if (!ac.signal.aborted) setItems(res);
      })
      .catch((e) => {
        if (ac.signal.aborted) return;
        setError(e as ApiError);
      });
  }

  useEffect(() => {
    load();
    return () => loadController.current?.abort();
     
  }, []);

  async function onRevoke(id: string, name: string) {
    if (!confirm(`撤销「${name}」的授权？该应用已签发的 token 会立刻失效。`)) return;
    try {
      await oauthProvider.authorizations.revoke(id);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <PageContainer>
      <PageHeader
        title="已授权应用"
        description="列出你曾允许访问账号数据的第三方与官方应用。"
      />
      <ErrorBanner error={error} />
      <Surface>
        <SurfaceHeader dense>
          <span className="text-[12px] font-medium text-fg">
            授权记录{items ? ` · ${items.length}` : ""}
          </span>
        </SurfaceHeader>
        <SurfaceBody noPad>
          {items === null ? (
            <Loading />
          ) : items.length === 0 ? (
            <EmptyState inset title="还没有授权过任何应用" />
          ) : (
            <ul className="list-none divide-y divide-[var(--separator)] m-0 p-0">
              {items.map((row) => (
                <li key={row.id} className="flex items-start gap-3 px-4 py-3">
                  <Avatar size="md">
                    {row.client_logo_url && (
                      <Avatar.Image alt={row.client_name} src={row.client_logo_url} />
                    )}
                    <Avatar.Fallback>{row.client_name.slice(0, 2).toUpperCase()}</Avatar.Fallback>
                  </Avatar>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="text-[13.5px] font-medium text-fg">{row.client_name}</span>
                      {row.is_first_party ? <Pill tone="success">官方</Pill> : null}
                    </div>
                    <div className="mt-0.5 text-[11.5px] text-muted">
                      授权 <RelativeTime iso={row.granted_at} /> ·{" "}
                      更新 <RelativeTime iso={row.updated_at} />
                    </div>
                    {row.scopes.length > 0 ? (
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        {row.scopes.map((s) => (
                          <Pill key={s} tone="neutral">{s}</Pill>
                        ))}
                      </div>
                    ) : null}
                  </div>
                  <Button
                    variant="danger-soft"
                    size="sm"
                    onPress={() => onRevoke(row.id, row.client_name)}
                  >
                    <TrashIcon width={13} height={13} /> 撤销
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </SurfaceBody>
      </Surface>
    </PageContainer>
  );
}
