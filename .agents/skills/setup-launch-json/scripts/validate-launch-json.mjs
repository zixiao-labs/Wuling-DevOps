#!/usr/bin/env node
import { promises as fs } from "node:fs";
import path from "node:path";

function argument(name) {
  const index = process.argv.indexOf(name);
  return index >= 0 ? process.argv[index + 1] : undefined;
}

function stripJsonComments(source) {
  let result = "";
  let inString = false;
  let escaped = false;
  let lineComment = false;
  let blockComment = false;
  for (let index = 0; index < source.length; index++) {
    const char = source[index];
    const next = source[index + 1];
    if (lineComment) {
      if (char === "\n" || char === "\r") {
        lineComment = false;
        result += char;
      } else result += " ";
    } else if (blockComment) {
      if (char === "*" && next === "/") {
        result += "  ";
        index++;
        blockComment = false;
      } else result += char === "\n" || char === "\r" ? char : " ";
    } else if (inString) {
      result += char;
      if (escaped) escaped = false;
      else if (char === "\\") escaped = true;
      else if (char === '"') inString = false;
    } else if (char === '"') {
      inString = true;
      result += char;
    } else if (char === "/" && next === "/") {
      result += "  ";
      index++;
      lineComment = true;
    } else if (char === "/" && next === "*") {
      result += "  ";
      index++;
      blockComment = true;
    } else result += char;
  }
  return result;
}

function removeTrailingCommas(source) {
  let result = "";
  let inString = false;
  let escaped = false;
  for (let index = 0; index < source.length; index++) {
    const char = source[index];
    if (inString) {
      result += char;
      if (escaped) escaped = false;
      else if (char === "\\") escaped = true;
      else if (char === '"') inString = false;
      continue;
    }
    if (char === '"') {
      inString = true;
      result += char;
      continue;
    }
    if (char === ",") {
      let next = index + 1;
      while (/\s/.test(source[next] ?? "")) next++;
      if (source[next] === "}" || source[next] === "]") continue;
    }
    result += char;
  }
  return result;
}

function object(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function strings(value, visit, keyPath = "") {
  if (typeof value === "string") visit(value, keyPath);
  else if (Array.isArray(value)) value.forEach((item, index) => strings(item, visit, `${keyPath}[${index}]`));
  else if (object(value)) {
    Object.entries(value).forEach(([key, item]) => strings(item, visit, keyPath ? `${keyPath}.${key}` : key));
  }
}

const workspace = path.resolve(argument("--workspace") || process.cwd());
const explicit = argument("--file");
let target = explicit ? path.resolve(workspace, explicit) : undefined;
if (!target) {
  for (const candidate of [".logos/launch.json", ".vscode/launch.json"]) {
    const resolved = path.join(workspace, candidate);
    if (await fs.stat(resolved).then(item => item.isFile()).catch(() => false)) {
      target = resolved;
      break;
    }
  }
}
if (!target) {
  console.error("ERROR: No .logos/launch.json or .vscode/launch.json found.");
  process.exit(1);
}

const errors = [];
const warnings = [];
let document;
try {
  document = JSON.parse(removeTrailingCommas(stripJsonComments(await fs.readFile(target, "utf8"))));
} catch (error) {
  console.error(`ERROR: ${target}: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}

if (!object(document)) errors.push("The document must be a JSON object.");
const root = object(document) ? document : {};
if (root.version !== "0.2.0") warnings.push('Use top-level "version": "0.2.0".');
const configurations = Array.isArray(root.configurations) ? root.configurations : [];
if (!Array.isArray(root.configurations)) errors.push("The document must contain a configurations array.");
for (const field of ["inputs", "compounds"]) {
  if (field in root) errors.push(`Top-level '${field}' is not supported by Logos.`);
}

const names = new Set();
const builtIns = new Set(["node", "pwa-node", "chrome", "pwa-chrome", "electron"]);
const unsupportedFields = [
  "preLaunchTask",
  "postDebugTask",
  "windows",
  "linux",
  "osx",
  "presentation",
  "serverReadyAction",
];
for (const [index, configuration] of configurations.entries()) {
  const label = `configurations[${index}]`;
  if (!object(configuration)) {
    errors.push(`${label} must be an object.`);
    continue;
  }
  if (typeof configuration.name !== "string" || !configuration.name.trim()) {
    errors.push(`${label}.name must be a non-empty string.`);
  } else if (names.has(configuration.name)) {
    errors.push(`${label}.name duplicates '${configuration.name}'.`);
  } else names.add(configuration.name);
  if (typeof configuration.type !== "string" || !configuration.type.trim()) {
    errors.push(`${label}.type must be a non-empty string.`);
  }
  if (configuration.request !== "launch" && configuration.request !== "attach") {
    errors.push(`${label}.request must be 'launch' or 'attach'.`);
  }
  if (configuration.type === "pwa-extensionHost") {
    errors.push(`${label}: pwa-extensionHost is not supported end to end.`);
  } else if (!builtIns.has(configuration.type) && !object(configuration.adapter)) {
    errors.push(`${label}: custom debugger type '${configuration.type}' requires an adapter descriptor.`);
  }
  if (configuration.console === "externalTerminal") {
    errors.push(`${label}.console cannot be externalTerminal.`);
  }
  for (const field of unsupportedFields) {
    if (field in configuration) errors.push(`${label}.${field} is not supported by Logos.`);
  }
  if (object(configuration.adapter)) {
    const adapter = configuration.adapter;
    if (!new Set(["executable", "server", "executable-server"]).has(adapter.type)) {
      errors.push(`${label}.adapter.type is invalid.`);
    }
    if ((adapter.type === "executable" || adapter.type === "executable-server") &&
        (typeof adapter.command !== "string" || !adapter.command.trim())) {
      errors.push(`${label}.adapter.command is required.`);
    }
    if (adapter.type === "server" && !Number.isInteger(adapter.port)) {
      errors.push(`${label}.adapter.port is required for a server adapter.`);
    }
    if (adapter.port !== undefined &&
        (!Number.isInteger(adapter.port) || adapter.port < 1 || adapter.port > 65535)) {
      errors.push(`${label}.adapter.port must be an integer from 1 through 65535.`);
    }
  }
  if (configuration.renderer !== undefined) {
    if (!object(configuration.renderer)) {
      errors.push(`${label}.renderer must be an object.`);
    } else {
      const renderer = configuration.renderer;
      if (!Number.isInteger(renderer.port)) {
        errors.push(`${label}.renderer.port is required for renderer debugging.`);
      }
      if (renderer.port !== undefined &&
          (!Number.isInteger(renderer.port) || renderer.port < 1 || renderer.port > 65535)) {
        errors.push(`${label}.renderer.port must be an integer from 1 through 65535.`);
      }
    }
  }
  strings(configuration, (value, keyPath) => {
    for (const match of value.matchAll(/\$\{([^}]+)\}/g)) {
      const variable = match[1];
      const general = new Set([
        "workspaceFolder",
        "workspaceFolderBasename",
        "file",
        "fileBasename",
        "fileDirname",
        "relativeFile",
        "pathSeparator",
      ]);
      const allowed = general.has(variable) ||
        variable.startsWith("workspaceFolder:") ||
        variable.startsWith("env:") ||
        ((variable === "host" || variable === "port") &&
          configuration.adapter?.type === "executable-server" &&
          keyPath.startsWith("adapter.args"));
      if (!allowed) errors.push(`${label}.${keyPath} uses unsupported variable \${${variable}}.`);
    }
  });
}

errors.forEach(message => console.error(`ERROR: ${message}`));
warnings.forEach(message => console.warn(`WARNING: ${message}`));
if (errors.length) process.exit(1);
console.log(`OK: ${path.relative(workspace, target) || target} contains ${configurations.length} valid configuration(s).`);
