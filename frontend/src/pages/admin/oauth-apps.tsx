import { Avatar, Button, Card, Chip, Table } from "@heroui/react";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { Navigate } from "chen-the-dawnstreak";
import { useCallback, useEffect, useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { OAuthAppView } from "@/api/types";
import { RequireAuth } from "@/auth/guards";
import { authStore } from "@/auth/store";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";

/**
 * /admin/oauth-apps — admin-only view of every OAuth App registered on this
 * instance. Lets ops toggle `is_first_party` (which affects the consent UI
 * badge, not the protocol — PKCE and consent stay mandatory either way) and
 * delete misbehaving apps. Server enforces is_admin via /api/v1/admin/*
 * middleware; this page double-gates on the client for nicer UX.
 */
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

  if (!user?.is_admin) {
    return <Navigate to="/orgs" replace />;
  }

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
    <Card>
      <Card.Header>
        <Card.Title>OAuth 应用（管理员视图）</Card.Title>
        <Card.Description>
          浏览本实例所有 OAuth 应用，标记 / 取消「官方」徽章；可删除滥用应用。
        </Card.Description>
      </Card.Header>
      <Card.Content>
        <ErrorBanner error={error} />
        {items === null ? (
          <Loading />
        ) : items.length === 0 ? (
          <div style={{ color: "var(--muted)", padding: "1rem 0" }}>该实例没有任何 OAuth 应用。</div>
        ) : (
          <Table>
            <Table.ScrollContainer>
              <Table.Content aria-label="OAuth 应用（管理员视图）">
                <Table.Header>
                  <Table.Column isRowHeader>应用</Table.Column>
                  <Table.Column>Client ID</Table.Column>
                  <Table.Column>所有者</Table.Column>
                  <Table.Column>类型</Table.Column>
                  <Table.Column>scope 默认</Table.Column>
                  <Table.Column>创建</Table.Column>
                  <Table.Column>操作</Table.Column>
                </Table.Header>
                <Table.Body>
                  {items.map((a) => (
                    <Table.Row key={a.id}>
                      <Table.Cell>
                        <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
                          <Avatar size="sm">
                            {a.logo_url && <Avatar.Image alt={a.name} src={a.logo_url} />}
                            <Avatar.Fallback>{a.name.slice(0, 2).toUpperCase()}</Avatar.Fallback>
                          </Avatar>
                          <span>
                            {a.name}
                            {a.is_first_party && (
                              <Chip color="accent" size="sm" style={{ marginLeft: "0.4rem" }}>
                                官方
                              </Chip>
                            )}
                          </span>
                        </div>
                      </Table.Cell>
                      <Table.Cell>
                        <code style={{ fontSize: "0.75rem" }}>{a.client_id}</code>
                      </Table.Cell>
                      <Table.Cell>{a.is_first_party ? "—" : "用户"}</Table.Cell>
                      <Table.Cell>{a.is_confidential ? "机密" : "公开"}</Table.Cell>
                      <Table.Cell>
                        <code style={{ fontSize: "0.75rem" }}>{a.default_scopes.join(", ")}</code>
                      </Table.Cell>
                      <Table.Cell>
                        <RelativeTime iso={a.created_at} />
                      </Table.Cell>
                      <Table.Cell>
                        <div style={{ display: "flex", gap: "0.4rem" }}>
                          <Button size="sm" variant="secondary" onPress={() => toggleFirstParty(a)}>
                            {a.is_first_party ? "取消官方" : "标为官方"}
                          </Button>
                          <Button size="sm" variant="danger-soft" onPress={() => onDelete(a)}>
                            <TrashIcon width={14} height={14} /> 删除
                          </Button>
                        </div>
                      </Table.Cell>
                    </Table.Row>
                  ))}
                </Table.Body>
              </Table.Content>
            </Table.ScrollContainer>
          </Table>
        )}
      </Card.Content>
    </Card>
  );
}
