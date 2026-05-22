import { Outlet, useParams } from "chen-the-dawnstreak";
import { useEffect, useState } from "react";

import { orgs as orgsApi } from "@/api/endpoints";
import { ApiError } from "@/api/errors";
import { ErrorBanner } from "@/components/error-banner";
import { Loading } from "@/components/loading";
import { PageContainer } from "@/components/page/primitives";
import { clearShellContext, setShellContext } from "@/components/shell/sidebar-store";
import { RequireAuth } from "@/auth/guards";
import { OrgContext } from "@/auth/org-context";
import type { Org } from "@/api/types";

export default function OrgLayout() {
  return (
    <RequireAuth>
      <OrgShell />
    </RequireAuth>
  );
}

function OrgShell() {
  const params = useParams();
  const orgSlug = params.org_slug ?? "";
  const [org, setOrg] = useState<Org | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setOrg(null);
    setError(null);
    if (!orgSlug) return;
    orgsApi.get(orgSlug).then(setOrg).catch((e) => setError(e as ApiError));
  }, [orgSlug]);

  // Decorate the AppShell sidebar with the resolved display name so it stops
  // showing the raw slug as soon as the data is in hand.
  useEffect(() => {
    setShellContext({ orgDisplayName: org?.display_name || org?.slug || null });
    return () => {
      clearShellContext();
    };
  }, [org]);

  if (error) {
    return (
      <PageContainer>
        <ErrorBanner error={error} />
      </PageContainer>
    );
  }
  if (!org) return <Loading />;

  return (
    <OrgContext.Provider value={org}>
      <Outlet />
    </OrgContext.Provider>
  );
}
