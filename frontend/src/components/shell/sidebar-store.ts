/**
 * shell/sidebar-store.ts — tiny atom for shell decoration.
 *
 * The root layout (AppShell) needs to render the contextual sidebar BEFORE
 * the org/project `_layout.tsx` files have fetched their data. So the URL
 * provides slugs, and child layouts decorate the sidebar with richer display
 * names + visibility badges by writing into this store.
 *
 * Child layouts call `setShellContext({...})` in an effect; the AppShell
 * reads it via `useShellContext()`. Clearing happens by writing nulls when
 * the layout unmounts.
 */

import { createSimpleStore } from "chen-the-dawnstreak";

import type { Visibility } from "@/api/types";

export interface ShellContext {
  orgDisplayName: string | null;
  projectDisplayName: string | null;
  projectVisibility: Visibility | null;
  repoSlug: string | null;
}

const empty: ShellContext = {
  orgDisplayName: null,
  projectDisplayName: null,
  projectVisibility: null,
  repoSlug: null,
};

export const shellStore = createSimpleStore<ShellContext>(empty);

export function setShellContext(patch: Partial<ShellContext>): void {
  shellStore.setState({ ...shellStore.getState(), ...patch });
}

export function clearShellContext(): void {
  shellStore.setState(empty);
}

export function useShellContext(): ShellContext {
  return shellStore.useStore();
}
