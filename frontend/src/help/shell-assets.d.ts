export interface ShellAssets {
  cssHref: string;
  themeSrc: string | null;
  enhanceSrc: string;
  baseUrl: string;
}

export declare function readShellAssetsFromHtml(
  html: string,
  overrides?: Partial<ShellAssets>,
): ShellAssets;

export declare function readShellAssetsFromDist(
  distDir: string,
  overrides?: Partial<ShellAssets>,
): ShellAssets;
