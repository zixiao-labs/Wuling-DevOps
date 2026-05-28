// ESLint flat config (ESLint 9.x).
//
// Scope:
//   - TypeScript + React 19 + Vite app under src/
//   - Light type-aware-free preset (we already get strict TS via tsc --noEmit;
//     the typed lint passes add ~5-10x runtime and would mostly duplicate what
//     the compiler already enforces in this codebase).
//
// Run with: `npm run lint` (or `lint:fix`).

import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactPlugin from "eslint-plugin-react";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";

export default tseslint.config(
  // 1. Files we never want to lint.
  //    - dist:     build output
  //    - node_modules: dep code
  //    - schema.gen.ts: auto-generated from openapi.yaml (run `npm run api:types`
  //      to refresh; manual edits would be wiped, so don't bother linting)
  //    - public/:  static assets
  //    - .nasti:   nasti dev/build caches
  {
    ignores: [
      "dist/**",
      "build/**",
      "node_modules/**",
      "public/**",
      ".nasti/**",
      "src/api/schema.gen.ts",
      "src/theme-init.js",
    ],
  },

  // 2. Base presets.
  js.configs.recommended,
  ...tseslint.configs.recommended,

  // 3. React + hooks + refresh, scoped to the SPA source.
  {
    files: ["src/**/*.{ts,tsx,js,jsx}"],
    plugins: {
      react: reactPlugin,
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: {
        ...globals.browser,
      },
      parserOptions: {
        ecmaFeatures: { jsx: true },
      },
    },
    settings: {
      react: { version: "19.2" },
    },
    rules: {
      // React 17+ JSX transform — no need to `import React`.
      ...reactPlugin.configs.recommended.rules,
      ...reactPlugin.configs["jsx-runtime"].rules,
      ...reactHooks.configs.recommended.rules,

      // react-hooks v7 added `set-state-in-effect` which flags every
      // `useEffect(() => { load(); }, [])` data-fetch pattern as an error.
      // That pattern is used throughout this codebase and is a valid way to
      // bridge React state to async work; restructuring every page just to
      // satisfy the rule isn't worth it. Keep it as guidance, not gate.
      "react-hooks/set-state-in-effect": "off",
      // exhaustive-deps mis-fires on intentionally captured deps (e.g. a
      // `load()` closure that we want to redefine each render). Several pages
      // already use eslint-disable comments; warn instead of error so newer
      // additions don't require those.
      "react-hooks/exhaustive-deps": "warn",

      // We use TS for prop types, never PropTypes.
      "react/prop-types": "off",
      // React 19 + Vite have native typing for these.
      "react/react-in-jsx-scope": "off",

      // Vite/Nasti HMR boundary check — warns when a non-component export is
      // mixed in a module that also exports a component.
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],

      // Allow `_unused` placeholder params (idiomatic for callback signatures
      // we don't fully consume).
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
        },
      ],
      // The base rule mis-fires on TS type imports / overloads.
      "no-unused-vars": "off",

      // We sometimes need `any` at API boundaries (FormData, error objects from
      // unknown sources). The rule is useful as a warn but error is too loud.
      "@typescript-eslint/no-explicit-any": "warn",

      // Banned by core/recommended but harmless in our codebase. Suppress so
      // people don't write `// eslint-disable` everywhere.
      "@typescript-eslint/no-empty-object-type": "off",

      // Filename / path validators legitimately want to reject the C0 range
      // (e.g. wiki page-name checks). The rule almost never catches a real
      // bug, so disable it rather than annotate each site.
      "no-control-regex": "off",
    },
  },

  // 4. Node-flavored config files (nasti.config.ts, this file itself).
  {
    files: ["*.{js,ts,mjs,cjs}", "nasti.config.ts"],
    languageOptions: {
      globals: {
        ...globals.node,
      },
    },
    rules: {
      // Config files commonly use require()-style globs, ts-ignore for node
      // builtins missing from the SPA tsconfig, etc.
      "@typescript-eslint/ban-ts-comment": "off",
      "@typescript-eslint/no-explicit-any": "off",
    },
  },
);
