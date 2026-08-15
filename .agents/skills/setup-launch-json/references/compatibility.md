# Logos launch.json compatibility

## File precedence

Logos loads the first existing file:

1. `<workspace>/.logos/launch.json`
2. `<workspace>/.vscode/launch.json`

An invalid `.logos/launch.json` prevents fallback. Both files accept JSONC comments and trailing commas.

## Built-in JavaScript types

| Configuration type | Adapter type | Notes |
| --- | --- | --- |
| `node`, `pwa-node` | `pwa-node` | Launch or attach Node.js. |
| `chrome`, `pwa-chrome` | `pwa-chrome` | Launch or attach Chromium. |
| `electron` | `pwa-node` | Logos alias for Electron main; optionally adds a renderer child session. |

For Electron main plus renderer, put a matching remote-debugging switch in the Electron runtime arguments and add:

```jsonc
"renderer": {
  "port": 9222,
  "webRoot": "${workspaceFolder}",
  "urlFilter": "*"
}
```

The `renderer.port` must match Electron's `--remote-debugging-port` value.

## Custom adapter descriptors

Use one of these Logos-only shapes:

```jsonc
"adapter": {
  "type": "executable",
  "command": "confirmed-adapter-command",
  "args": ["--stdio"],
  "cwd": "${workspaceFolder}",
  "env": { "LOG_LEVEL": "debug" }
}
```

```jsonc
"adapter": {
  "type": "server",
  "host": "127.0.0.1",
  "port": 4711
}
```

```jsonc
"adapter": {
  "type": "executable-server",
  "command": "confirmed-adapter-command",
  "args": ["--listen", "${host}:${port}"],
  "cwd": "${workspaceFolder}",
  "host": "127.0.0.1"
}
```

For `server`, `port` is required. For `executable-server`, Logos can allocate the port; it substitutes `${host}` and `${port}` in adapter arguments and appends the allocated port when no argument contains `${port}`.

## Shareability decision

Use `.vscode/launch.json` when the configuration:

- uses `node`, `pwa-node`, `chrome`, or `pwa-chrome`;
- uses only variables supported by both products;
- contains no Logos `adapter` or `renderer` object; and
- does not depend on unsupported editor orchestration.

Prefer `.vscode/launch.json` for the shared subset. Use `.logos/launch.json` for the `electron` alias, custom adapter descriptors, or any Logos-specific behavior.
