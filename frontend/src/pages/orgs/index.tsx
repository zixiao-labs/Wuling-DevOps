import { Button, Card, Description, Input, Label, TextField } from "@heroui/react";
import PlusIcon from "@gravity-ui/icons/Plus";
import HouseIcon from "@gravity-ui/icons/House";
import { Link } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { orgs as orgsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { EmptyState } from "@/components/empty-state";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { RelativeTime } from "@/components/relative-time";
import { RequireAuth } from "@/auth/guards";
import type { Org } from "@/api/types";

export default function OrgsIndex() {
  return (
    <RequireAuth>
      <OrgsList />
    </RequireAuth>
  );
}

function OrgsList() {
  const [items, setItems] = useState<Org[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  // Create-form state
  const [slug, setSlug] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [description, setDescription] = useState("");
  const [showForm, setShowForm] = useState(false);
  const [creating, setCreating] = useState(false);

  function load() {
    setError(null);
    orgsApi.list().then(setItems).catch((e) => setError(e as ApiError));
  }

  useEffect(load, []);

  async function onCreate(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setCreating(true);
    try {
      await orgsApi.create({
        slug,
        display_name: displayName || undefined,
        description: description || undefined,
      });
      setSlug("");
      setDisplayName("");
      setDescription("");
      setShowForm(false);
      load();
    } catch (err) {
      setError(err as ApiError);
    } finally {
      setCreating(false);
    }
  }

  return (
    <div>
      <header
        style={{
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          marginBottom: "1rem",
        }}
      >
        <h1 style={{ margin: 0, fontSize: "1.5rem" }}>组织</h1>
        <Button variant="primary" onPress={() => setShowForm((v) => !v)}>
          <PlusIcon width={16} height={16} /> {showForm ? "取消" : "新建组织"}
        </Button>
      </header>

      {showForm ? (
        <Card style={{ marginBottom: "1rem" }}>
          <Card.Content>
            <form
              onSubmit={onCreate}
              style={{ display: "flex", flexDirection: "column", gap: "0.9rem" }}
            >
              <TextField name="slug" value={slug} onChange={setSlug} isRequired>
                <Label>Slug</Label>
                <Input placeholder="zixiao-labs" />
                <Description>2–64 字符，作为 URL 路径段。</Description>
              </TextField>
              <TextField name="display_name" value={displayName} onChange={setDisplayName}>
                <Label>显示名</Label>
                <Input placeholder="紫霄实验室" />
              </TextField>
              <TextField name="description" value={description} onChange={setDescription}>
                <Label>简介</Label>
                <Input />
              </TextField>
              <ErrorBanner error={error} />
              <div>
                <Button type="submit" isDisabled={creating || !slug}>
                  {creating ? "创建中…" : "创建"}
                </Button>
              </div>
            </form>
          </Card.Content>
        </Card>
      ) : (
        <ErrorBanner error={error} />
      )}

      {items === null ? (
        <Loading />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<HouseIcon width={32} height={32} />}
          title="还没有任何组织"
          description="新建你的第一个组织来托管项目。"
          action={
            <Button onPress={() => setShowForm(true)}>
              <PlusIcon width={16} height={16} /> 新建组织
            </Button>
          }
        />
      ) : (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))",
            gap: "1rem",
          }}
        >
          {items.map((o) => (
            <Link
              key={o.id}
              to={`/orgs/${encodeURIComponent(o.slug)}`}
              style={{ color: "var(--foreground)", textDecoration: "none" }}
            >
              <Card>
                <Card.Header>
                  <Card.Title>
                    {o.display_name || o.slug}
                    {o.is_personal ? (
                      <span
                        style={{
                          marginLeft: "0.5rem",
                          fontSize: "0.7rem",
                          color: "var(--muted)",
                          background: "var(--surface-secondary)",
                          padding: "0.05rem 0.4rem",
                          borderRadius: "999px",
                        }}
                      >
                        个人
                      </span>
                    ) : null}
                  </Card.Title>
                  <Card.Description>@{o.slug}</Card.Description>
                </Card.Header>
                <Card.Content>
                  <div style={{ color: "var(--muted)", minHeight: "1.2em" }}>
                    {o.description || "—"}
                  </div>
                  <div style={{ fontSize: "0.75rem", color: "var(--muted)", marginTop: "0.5rem" }}>
                    <RelativeTime iso={o.created_at} /> 创建
                  </div>
                </Card.Content>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
