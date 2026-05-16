/**
 * Global auth + theme store.
 *
 * Uses Chen's `createSimpleStore` (module singleton, no Provider) so any
 * component can subscribe via `authStore.useStore()` / `useTheme()`.
 */

import { createSimpleStore } from "chen-the-dawnstreak";
import type { User } from "@/api/types";
import { configureClient } from "@/api/client";

const TOKEN_KEY = "wuling.token";
const USER_KEY = "wuling.user";
const THEME_KEY = "wuling.theme";
const MODE_KEY = "wuling.mode";

export type ThemeName = "clean" | "green" | "zixiaolabsvi";
export type ThemeMode = "light" | "dark";

export interface AuthState {
  token: string | null;
  user: User | null;
  theme: ThemeName;
  mode: ThemeMode;
}

function readInitial(): AuthState {
  if (typeof window === "undefined") {
    return { token: null, user: null, theme: "clean", mode: "light" };
  }
  const userRaw = localStorage.getItem(USER_KEY);
  let user: User | null = null;
  try {
    user = userRaw ? (JSON.parse(userRaw) as User) : null;
  } catch {
    user = null;
  }
  const rawTheme = localStorage.getItem(THEME_KEY);
  const rawMode = localStorage.getItem(MODE_KEY);
  const theme: ThemeName =
    rawTheme === "clean" || rawTheme === "green" || rawTheme === "zixiaolabsvi"
      ? rawTheme
      : "clean";
  const mode: ThemeMode = rawMode === "dark" || rawMode === "light" ? rawMode : "light";
  return {
    token: localStorage.getItem(TOKEN_KEY),
    user,
    theme,
    mode,
  };
}

export const authStore = createSimpleStore<AuthState>(readInitial());

export function setSession(token: string, user: User): void {
  localStorage.setItem(TOKEN_KEY, token);
  localStorage.setItem(USER_KEY, JSON.stringify(user));
  authStore.setState({ token, user });
}

export function setUser(user: User | null): void {
  if (user) localStorage.setItem(USER_KEY, JSON.stringify(user));
  else localStorage.removeItem(USER_KEY);
  authStore.setState({ user });
}

export function clearSession(): void {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
  authStore.setState({ token: null, user: null });
}

export function setTheme(theme: ThemeName): void {
  localStorage.setItem(THEME_KEY, theme);
  if (typeof document !== "undefined") {
    document.documentElement.setAttribute("data-theme", theme);
  }
  authStore.setState({ theme });
}

export function setMode(mode: ThemeMode): void {
  localStorage.setItem(MODE_KEY, mode);
  if (typeof document !== "undefined") {
    document.documentElement.classList.toggle("dark", mode === "dark");
  }
  authStore.setState({ mode });
}

/**
 * Wire the HTTP client to read the latest token and react to 401s.
 * Called once from src/main.tsx.
 */
export function bindClientToAuthStore(): void {
  configureClient({
    getToken: () => authStore.getState().token,
    onUnauthorized: () => clearSession(),
  });
}
