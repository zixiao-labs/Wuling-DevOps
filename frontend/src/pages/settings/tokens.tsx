import {
  Button,
  Card,
  Description,
  Input,
  Label,
  Modal,
  Table,
  TextField,
} from "@heroui/react";
import CopyIcon from "@gravity-ui/icons/Copy";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { useEffect, useRef, useState } from "react";

import { tokens as patApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import type { AccessTokenView, PatScope } from "@/api/types";

const SCOPES: PatScope[] = ["repo:read", "repo:write"];

export default function TokensPage() {
  const [items, setItems] = useState<AccessTokenView[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<PatScope[]>(["repo:read"]);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<AccessTokenView | null>(null);
  const [copied, setCopied] = useState(false);

  // Component-scoped controller so refreshes triggered by onCreate/onRevoke
  // can also be aborted when the component unmounts.
  const loadController = useRef<AbortController | null>(null);
  const copyResetTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  function load() {
    loadController.current?.abort();
    const ac = new AbortController();
    loadController.current = ac;
    setError(null);
    patApi
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
    return () => {
      loadController.current?.abort();
      if (copyResetTimer.current !== null) clearTimeout(copyResetTimer.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!scopes || scopes.length === 0) {
      setError(new ApiError(0, "unknown", "请至少选择一个范围（scope）。"));
      return;
    }
    setCreating(true);
    setError(null);
    try {
      const tok = await patApi.create({ name, scopes });
      setCreated(tok);
      setName("");
      setScopes(["repo:read"]);
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  async function onRevoke(id: string) {
    if (!confirm("吊销这个令牌？已使用它的客户端会立即失效。")) return;
    try {
      await patApi.revoke(id);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <Card>
        <Card.Header>
          <Card.Title>访问令牌（PAT）</Card.Title>
          <Card.Description>
            创建后<strong>原始令牌只显示一次</strong>。CLI 推/拉时填到密码栏（用户名随意）。
          </Card.Description>
        </Card.Header>
        <Card.Content>
          <form
            onSubmit={onCreate}
            style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}
          >
            <TextField name="name" value={name} onChange={setName} isRequired>
              <Label>名称</Label>
              <Input placeholder="laptop / ci-runner / …" />
              <Description>仅自己看，长度不超过 64。</Description>
            </TextField>
            <div>
              <div style={{ fontSize: "0.85rem", marginBottom: "0.4rem" }}>范围</div>
              <div style={{ display: "flex", gap: "0.5rem" }}>
                {SCOPES.map((s) => (
                  <label
                    key={s}
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      gap: "0.3rem",
                      padding: "0.25rem 0.6rem",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--field-radius)",
                      cursor: "pointer",
                      background: scopes.includes(s) ? "var(--surface-secondary)" : "var(--surface)",
                    }}
                  >
                    <input
                      type="checkbox"
                      checked={scopes.includes(s)}
                      onChange={(e) =>
                        setScopes((cur) =>
                          e.target.checked ? [...cur, s] : cur.filter((x) => x !== s),
                        )
                      }
                    />
                    <code>{s}</code>
                  </label>
                ))}
              </div>
            </div>
            <ErrorBanner error={error} />
            <div>
              <Button type="submit" isDisabled={creating || !name || scopes.length === 0}>
                {creating ? "创建中…" : "创建令牌"}
              </Button>
            </div>
          </form>
        </Card.Content>
      </Card>

      {items === null ? (
        <Loading />
      ) : items.length === 0 ? (
        <Card>
          <Card.Content>
            <div style={{ color: "var(--muted)", padding: "1rem 0" }}>还没有令牌。</div>
          </Card.Content>
        </Card>
      ) : (
        <Card>
          <Card.Header>
            <Card.Title>现有令牌</Card.Title>
          </Card.Header>
          <Card.Content>
            <Table>
              <Table.ScrollContainer>
                <Table.Content aria-label="访问令牌">
                  <Table.Header>
                    <Table.Column isRowHeader>名称</Table.Column>
                    <Table.Column>范围</Table.Column>
                    <Table.Column>创建</Table.Column>
                    <Table.Column>到期</Table.Column>
                    <Table.Column>操作</Table.Column>
                  </Table.Header>
                  <Table.Body>
                    {items.map((it) => (
                      <Table.Row key={it.id}>
                        <Table.Cell>{it.name}</Table.Cell>
                        <Table.Cell>
                          <code style={{ fontSize: "0.8rem" }}>{it.scopes.join(", ") || "—"}</code>
                        </Table.Cell>
                        <Table.Cell>
                          <RelativeTime iso={it.created_at} />
                        </Table.Cell>
                        <Table.Cell>
                          {it.expires_at ? <RelativeTime iso={it.expires_at} /> : "永久"}
                        </Table.Cell>
                        <Table.Cell>
                          <Button variant="danger-soft" size="sm" onPress={() => onRevoke(it.id)}>
                            <TrashIcon width={14} height={14} /> 吊销
                          </Button>
                        </Table.Cell>
                      </Table.Row>
                    ))}
                  </Table.Body>
                </Table.Content>
              </Table.ScrollContainer>
            </Table>
          </Card.Content>
        </Card>
      )}

      <Modal>
        <Modal.Backdrop isOpen={created !== null} onOpenChange={(o) => !o && setCreated(null)}>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>令牌已创建</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p>
                  <strong>这是你最后一次看到原始令牌。</strong>关闭窗口后将无法再次获取，只能吊销重建。
                </p>
                <pre
                  style={{
                    background: "var(--surface-secondary)",
                    padding: "0.75rem",
                    borderRadius: "var(--radius)",
                    overflowX: "auto",
                    fontFamily: "ui-monospace, monospace",
                    fontSize: "0.85rem",
                    userSelect: "all",
                  }}
                >
                  {created?.token}
                </pre>
              </Modal.Body>
              <Modal.Footer>
                <Button
                  variant="secondary"
                  onPress={async () => {
                    if (!created?.token) return;
                    if (!navigator.clipboard) {
                      alert("当前浏览器不支持自动复制；请手动选中令牌文本。");
                      return;
                    }
                    try {
                      await navigator.clipboard.writeText(created.token);
                      setCopied(true);
                      if (copyResetTimer.current !== null) clearTimeout(copyResetTimer.current);
                      copyResetTimer.current = setTimeout(() => setCopied(false), 1500);
                    } catch {
                      alert("复制失败，请手动选中令牌文本。");
                    }
                  }}
                >
                  <CopyIcon width={16} height={16} /> {copied ? "已复制" : "复制"}
                </Button>
                <Button onPress={() => setCreated(null)}>关闭</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}
