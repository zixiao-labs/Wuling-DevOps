import {
  Button,
  Card,
  Description,
  Input,
  Label,
  Table,
  TextArea,
  TextField,
} from "@heroui/react";
import TrashIcon from "@gravity-ui/icons/TrashBin";
import { useEffect, useState } from "react";

import { sshKeys as keysApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import type { SSHKey } from "@/api/types";

export default function SshKeysPage() {
  const [items, setItems] = useState<SSHKey[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [title, setTitle] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [creating, setCreating] = useState(false);

  function load() {
    setError(null);
    keysApi.list().then(setItems).catch((e) => setError(e as ApiError));
  }

  useEffect(load, []);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    setError(null);
    try {
      await keysApi.create({ title, public_key: publicKey.trim() });
      setTitle("");
      setPublicKey("");
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  async function onRevoke(id: string) {
    if (!confirm("吊销这把公钥？依赖它推/拉的客户端会立即失败。")) return;
    try {
      await keysApi.revoke(id);
      load();
    } catch (err) {
      setError(err as ApiError);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <Card>
        <Card.Header>
          <Card.Title>SSH 公钥</Card.Title>
          <Card.Description>
            注册公钥后即可用 <code>ssh://git@&lt;host&gt;:2222/&lt;org&gt;/&lt;project&gt;/&lt;repo&gt;.git</code> 推/拉。
          </Card.Description>
        </Card.Header>
        <Card.Content>
          <form onSubmit={onCreate} style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}>
            <TextField name="title" value={title} onChange={setTitle} isRequired>
              <Label>标题</Label>
              <Input placeholder="laptop / ci-runner / …" />
            </TextField>
            <TextField name="public_key" value={publicKey} onChange={setPublicKey} isRequired>
              <Label>公钥</Label>
              <TextArea
                rows={4}
                placeholder="ssh-ed25519 AAAA…"
                style={{
                  fontFamily: "ui-monospace, monospace",
                  fontSize: "0.85rem",
                }}
              />
              <Description>
                粘贴 <code>~/.ssh/id_*.pub</code> 全部内容。支持 ssh-rsa / ssh-ed25519 / ecdsa-sha2-*。
              </Description>
            </TextField>
            <ErrorBanner error={error} />
            <div>
              <Button type="submit" isDisabled={creating || !title || !publicKey}>
                {creating ? "添加中…" : "添加公钥"}
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
            <div style={{ color: "var(--muted)", padding: "1rem 0" }}>还没有公钥。</div>
          </Card.Content>
        </Card>
      ) : (
        <Card>
          <Card.Header>
            <Card.Title>已注册公钥</Card.Title>
          </Card.Header>
          <Card.Content>
            <Table>
              <Table.ScrollContainer>
                <Table.Content aria-label="SSH 公钥">
                  <Table.Header>
                    <Table.Column isRowHeader>标题</Table.Column>
                    <Table.Column>指纹</Table.Column>
                    <Table.Column>添加</Table.Column>
                    <Table.Column>最近使用</Table.Column>
                    <Table.Column>操作</Table.Column>
                  </Table.Header>
                  <Table.Body>
                    {items.map((k) => (
                      <Table.Row key={k.id}>
                        <Table.Cell>{k.title}</Table.Cell>
                        <Table.Cell>
                          <code style={{ fontSize: "0.75rem" }}>{k.fingerprint}</code>
                        </Table.Cell>
                        <Table.Cell>
                          <RelativeTime iso={k.created_at} />
                        </Table.Cell>
                        <Table.Cell>
                          {k.last_used_at ? <RelativeTime iso={k.last_used_at} /> : "—"}
                        </Table.Cell>
                        <Table.Cell>
                          <Button variant="danger-soft" size="sm" onPress={() => onRevoke(k.id)}>
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
    </div>
  );
}
