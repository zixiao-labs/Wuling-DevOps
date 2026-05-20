import { Avatar, Button, Card, Chip, Table } from "@heroui/react";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { useEffect, useRef, useState } from "react";

import { oauthProvider } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import type { AuthorizationView } from "@/api/types";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";

/**
 * /settings/authorized-apps — the user's view of every (user, client)
 * consent record they hold. Revoking a row revokes the durable consent AND
 * every live token minted under it (`oauthstore.RevokeAuthorization`).
 */
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
    <Card>
      <Card.Header>
        <Card.Title>已授权应用</Card.Title>
        <Card.Description>列出你曾允许访问账号数据的第三方与官方应用。</Card.Description>
      </Card.Header>
      <Card.Content>
        <ErrorBanner error={error} />
        {items === null ? (
          <Loading />
        ) : items.length === 0 ? (
          <div style={{ color: "var(--muted)", padding: "1rem 0" }}>还没有授权过任何应用。</div>
        ) : (
          <Table>
            <Table.ScrollContainer>
              <Table.Content aria-label="已授权应用">
                <Table.Header>
                  <Table.Column isRowHeader>应用</Table.Column>
                  <Table.Column>scope</Table.Column>
                  <Table.Column>授权</Table.Column>
                  <Table.Column>更新</Table.Column>
                  <Table.Column>操作</Table.Column>
                </Table.Header>
                <Table.Body>
                  {items.map((row) => (
                    <Table.Row key={row.id}>
                      <Table.Cell>
                        <div style={{ display: "flex", alignItems: "center", gap: "0.6rem" }}>
                          <Avatar size="sm">
                            {row.client_logo_url && (
                              <Avatar.Image alt={row.client_name} src={row.client_logo_url} />
                            )}
                            <Avatar.Fallback>{row.client_name.slice(0, 2).toUpperCase()}</Avatar.Fallback>
                          </Avatar>
                          <span>
                            {row.client_name}
                            {row.is_first_party && (
                              <Chip
                                color="accent"
                                size="sm"
                                style={{ marginLeft: "0.4rem" }}
                              >
                                官方
                              </Chip>
                            )}
                          </span>
                        </div>
                      </Table.Cell>
                      <Table.Cell>
                        <code style={{ fontSize: "0.75rem" }}>
                          {row.scopes.join(", ") || "—"}
                        </code>
                      </Table.Cell>
                      <Table.Cell>
                        <RelativeTime iso={row.granted_at} />
                      </Table.Cell>
                      <Table.Cell>
                        <RelativeTime iso={row.updated_at} />
                      </Table.Cell>
                      <Table.Cell>
                        <Button
                          variant="danger-soft"
                          size="sm"
                          onPress={() => onRevoke(row.id, row.client_name)}
                        >
                          <TrashIcon width={14} height={14} /> 撤销
                        </Button>
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
