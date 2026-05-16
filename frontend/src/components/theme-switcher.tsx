import SunIcon from "@gravity-ui/icons/Sun";
import MoonIcon from "@gravity-ui/icons/Moon";

import { authStore, setMode, setTheme, type ThemeName } from "@/auth/store";

const THEMES: Array<{ id: ThemeName; label: string; swatch: string }> = [
  { id: "clean", label: "Clean", swatch: "#5a8fb0" },
  { id: "green", label: "Green", swatch: "#a3d150" },
  { id: "zixiaolabsvi", label: "ZX Violet", swatch: "#8b5cf6" },
];

export function ThemeSwitcher() {
  const { theme, mode } = authStore.useStore();
  return (
    <div style={{ display: "inline-flex", alignItems: "center", gap: "0.5rem" }}>
      <div
        role="group"
        aria-label="主题"
        style={{
          display: "inline-flex",
          background: "var(--surface-secondary)",
          borderRadius: "var(--field-radius)",
          padding: "0.15rem",
          gap: "0.15rem",
        }}
      >
        {THEMES.map((t) => (
          <button
            key={t.id}
            onClick={() => setTheme(t.id)}
            title={t.label}
            aria-pressed={theme === t.id}
            style={{
              border: "none",
              cursor: "pointer",
              padding: "0.2rem 0.5rem",
              borderRadius: "calc(var(--field-radius) - 2px)",
              background: theme === t.id ? "var(--surface)" : "transparent",
              color: "var(--foreground)",
              display: "inline-flex",
              alignItems: "center",
              gap: "0.3rem",
              fontSize: "0.8rem",
            }}
          >
            <span
              style={{
                display: "inline-block",
                width: "0.65rem",
                height: "0.65rem",
                borderRadius: "999px",
                background: t.swatch,
                border: "1px solid var(--border)",
              }}
            />
            {t.label}
          </button>
        ))}
      </div>
      <button
        onClick={() => setMode(mode === "dark" ? "light" : "dark")}
        title={mode === "dark" ? "切到浅色" : "切到深色"}
        aria-label="切换深浅模式"
        style={{
          border: "1px solid var(--border)",
          background: "var(--surface)",
          color: "var(--foreground)",
          padding: "0.3rem 0.5rem",
          borderRadius: "var(--field-radius)",
          cursor: "pointer",
          display: "inline-flex",
          alignItems: "center",
        }}
      >
        {mode === "dark" ? <SunIcon width={16} height={16} /> : <MoonIcon width={16} height={16} />}
      </button>
    </div>
  );
}
