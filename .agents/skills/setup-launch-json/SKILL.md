---
name: setup-launch-json
description: Inspect a project and create, update, migrate, or repair a standards-aligned DAP launch.json for Logos or the VS Code-compatible subset. Use when an Agent needs to set up launch or attach debugging, choose a debug adapter, add Node/Chrome/Electron/custom runtime configurations, validate existing .logos/launch.json or .vscode/launch.json, or diagnose why Logos cannot load a launch configuration.
---

# Set up launch.json

Create a minimal, evidence-based debug configuration that Logos can load and the selected Debug Adapter Protocol implementation accepts.

## Workflow

1. Inspect the workspace before editing.
   - Read package/build scripts, entry points, runtime manifests, and relevant documentation.
   - Read both `.logos/launch.json` and `.vscode/launch.json` when present.
   - Identify the runtime executable, launch target or attach endpoint, working directory, and whether a build/dev server must already be running.
2. Choose the target file without changing precedence accidentally.
   - Update `.logos/launch.json` when it exists; Logos always loads it first.
   - Otherwise update `.vscode/launch.json` only when every new field belongs to the compatible subset.
   - When a Logos-only feature is required while `.vscode/launch.json` exists, explain that a new `.logos/launch.json` will shadow it and preserve every configuration the user still needs.
   - Prefer `.vscode/launch.json` when the configuration uses the shared VS Code subset, so mainstream editors and coding agents can discover it. Use `.logos/launch.json` when Logos-specific fields are required.
3. Choose `request` and `type` from project evidence.
   - Use `launch` when Logos should start the debuggee and `attach` when another process owns its lifecycle.
   - Use `node`/`pwa-node` for Node.js and `chrome`/`pwa-chrome` for Chromium.
   - Use `electron` only for the Logos Electron alias; use a valid Node configuration when the file must also work in VS Code.
   - For every other type, confirm the adapter command or endpoint and add a Logos `adapter` descriptor. Never invent an adapter executable or port.
4. Write one complete JSONC document with `"version": "0.2.0"` and a `configurations` array.
   - Give every configuration a unique `name`, a non-empty `type`, and `request` equal to `launch` or `attach`.
   - Preserve unrelated existing configurations and comments.
   - Keep credentials out of the file. Refer to environment variables instead.
   - Do not add editor orchestration that Logos does not implement.
5. Validate the result from the skill directory:

   ```bash
   node scripts/validate-launch-json.mjs --workspace /absolute/workspace/path
   ```

   Fix every error. Review warnings against the chosen adapter instead of suppressing them.
6. Report the target path, the chosen runtime/request, prerequisites, and whether the result is shareable with VS Code.
   - If `${file}` is used, tell the user to open a regular file before starting.
   - If a Logos `DAP` tool or the `logos-debug` MCP server is available, list configurations to verify discovery. Start one only when the task authorizes launching a process.

## Compatibility rules

- Never use `${command:...}` or `${input:...}`.
- Do not rely on `inputs`, `compounds`, `preLaunchTask`, `postDebugTask`, platform-specific `windows`/`linux`/`osx` merging, `presentation`, or `serverReadyAction`.
- Never set `console` to `externalTerminal`; use `internalConsole` or `integratedTerminal` when the adapter supports it.
- Do not generate `pwa-extensionHost`; Logos does not implement its reverse launch flow end to end.
- Treat `program`, `args`, `cwd`, `env`, `envFile`, `url`, `webRoot`, `runtimeExecutable`, `runtimeArgs`, `skipFiles`, and `outFiles` as adapter-specific. Include only fields supported by that adapter.
- Use only `${workspaceFolder}`, `${workspaceFolderBasename}`, `${workspaceFolder:<current-name>}`, `${file}`, `${fileBasename}`, `${fileDirname}`, `${relativeFile}`, `${pathSeparator}`, and `${env:NAME}`. Use `${host}` and `${port}` only in `executable-server.adapter.args`.

Read [references/compatibility.md](references/compatibility.md) when choosing an adapter descriptor, configuring Electron main plus renderer debugging, or deciding whether `.vscode/launch.json` can be shared.
