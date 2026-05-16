import { Navigate } from "chen-the-dawnstreak";

import { authStore } from "@/auth/store";

export default function HomeRedirect() {
  const { token } = authStore.useStore();
  return <Navigate to={token ? "/orgs" : "/login"} replace />;
}
