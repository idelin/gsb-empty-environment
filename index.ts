import { realpathSync, statSync } from "node:fs";
import path from "node:path";

export interface PermissionKindLists {
  read?: string[];
  write?: string[];
  net?: string[];
  env?: string[];
  run?: string[];
}

export interface PermissionLists {
  allow?: PermissionKindLists;
  deny?: PermissionKindLists;
}

export type PermissionRequest =
  | { kind: "read" | "write"; path: string; directory?: boolean }
  | { kind: "net"; host: string; port?: number }
  | { kind: "env"; name: string }
  | { kind: "run"; command: string };

export interface Decision {
  granted: boolean;
  reason: string;
}

const GRANT: Decision = { granted: true, reason: "allowed" };
const reject = (reason: string): Decision => ({ granted: false, reason });

function normalizePath(p: string): string {
  return path.resolve(p);
}

function isWithin(parent: string, child: string): boolean {
  if (child === parent) return true;
  const prefix = parent.endsWith(path.sep) ? parent : parent + path.sep;
  return child.startsWith(prefix);
}

function resolveReal(p: string): string {
  const norm = normalizePath(p);
  let current = norm;
  const tail: string[] = [];
  while (true) {
    try {
      const real = realpathSync(current);
      return tail.length === 0 ? real : path.join(real, ...tail.reverse());
    } catch {
      const parent = path.dirname(current);
      if (parent === current) return norm;
      tail.push(path.basename(current));
      current = parent;
    }
  }
}

function isDirectoryQuery(reqPath: string, flag: boolean | undefined): boolean {
  if (flag !== undefined) return flag;
  try {
    return statSync(normalizePath(reqPath)).isDirectory();
  } catch {
    return false;
  }
}

function decidePath(
  kind: "read" | "write",
  allow: string[],
  denyList: string[],
  reqPath: string,
  directoryFlag: boolean | undefined,
): Decision {
  const norm = normalizePath(reqPath);
  const real = resolveReal(norm);

  const denyEntries = denyList.map((entry) => ({
    norm: normalizePath(entry),
    real: resolveReal(entry),
  }));

  for (const entry of denyEntries) {
    if (
      isWithin(entry.norm, norm) ||
      isWithin(entry.norm, real) ||
      isWithin(entry.real, norm) ||
      isWithin(entry.real, real)
    ) {
      return reject(`${kind} on ${reqPath} rejected by a deny entry`);
    }
  }

  if (isDirectoryQuery(norm, directoryFlag)) {
    for (const entry of denyEntries) {
      if (
        (isWithin(norm, entry.norm) && entry.norm !== norm) ||
        (isWithin(real, entry.real) && entry.real !== real)
      ) {
        return reject(
          `${kind} on directory ${reqPath} not granted: a deny entry exists inside it`,
        );
      }
    }
  }

  const covered = allow.some((entry) => {
    const entryNorm = normalizePath(entry);
    const entryReal = resolveReal(entry);
    return (
      (isWithin(entryNorm, norm) || isWithin(entryReal, norm)) &&
      (isWithin(entryNorm, real) || isWithin(entryReal, real))
    );
  });

  return covered
    ? GRANT
    : reject(`${kind} on ${reqPath} not covered by any allow entry`);
}

function matchesEnv(entry: string, name: string): boolean {
  if (entry.endsWith("*")) return name.startsWith(entry.slice(0, -1));
  return entry === name;
}

function decideEnv(
  allow: string[],
  denyList: string[],
  name: string,
): Decision {
  if (denyList.some((entry) => matchesEnv(entry, name))) {
    return reject(`env ${name} matched by a deny entry`);
  }
  return allow.some((entry) => matchesEnv(entry, name))
    ? GRANT
    : reject(`env ${name} not covered by any allow entry`);
}

interface NetEntry {
  host: string;
  port?: number;
}

function normalizeHost(host: string): string {
  const trimmed = host.trim();
  if (trimmed.startsWith("[") && trimmed.endsWith("]")) {
    return trimmed.slice(1, -1).toLowerCase();
  }
  return trimmed.toLowerCase();
}

function parseNetEntry(entry: string): NetEntry {
  const trimmed = entry.trim();
  if (trimmed.startsWith("[")) {
    const close = trimmed.indexOf("]");
    if (close === -1) throw new Error(`invalid net entry: ${entry}`);
    const host = trimmed.slice(1, close).toLowerCase();
    const rest = trimmed.slice(close + 1);
    if (rest === "") return { host };
    if (/^:\d+$/.test(rest)) return { host, port: Number(rest.slice(1)) };
    throw new Error(`invalid net entry: ${entry}`);
  }
  const idx = trimmed.lastIndexOf(":");
  if (idx > 0 && /^\d+$/.test(trimmed.slice(idx + 1))) {
    return {
      host: trimmed.slice(0, idx).toLowerCase(),
      port: Number(trimmed.slice(idx + 1)),
    };
  }
  return { host: trimmed.toLowerCase() };
}

function matchesNet(entry: NetEntry, host: string, port?: number): boolean {
  if (entry.host !== host) return false;
  if (entry.port === undefined) return true;
  return entry.port === port;
}

function decideNet(
  allow: string[],
  denyList: string[],
  host: string,
  port?: number,
): Decision {
  const normHost = normalizeHost(host);
  if (
    denyList.some((entry) => matchesNet(parseNetEntry(entry), normHost, port))
  ) {
    return reject(`net ${host} rejected by a deny entry`);
  }
  return allow.some((entry) => matchesNet(parseNetEntry(entry), normHost, port))
    ? GRANT
    : reject(`net ${host} not covered by any allow entry`);
}

function isPathForm(command: string): boolean {
  return command.includes("/");
}

function lookupOnPath(name: string): string | null {
  const pathEnv = process.env.PATH ?? "";
  for (const dir of pathEnv.split(path.delimiter)) {
    if (dir === "") continue;
    const candidate = path.join(dir, name);
    try {
      return realpathSync(candidate);
    } catch {
      continue;
    }
  }
  return null;
}

function resolveCommand(command: string): string | null {
  if (isPathForm(command)) {
    try {
      return realpathSync(normalizePath(command));
    } catch {
      return null;
    }
  }
  return lookupOnPath(command);
}

function matchesRunEntry(entry: string, command: string): boolean {
  if (isPathForm(entry)) {
    const entryReal = resolveCommand(entry);
    const reqReal = resolveCommand(command);
    return entryReal !== null && reqReal !== null && entryReal === reqReal;
  }
  if (!isPathForm(command)) return entry === command;
  const entryReal = lookupOnPath(entry);
  const reqReal = resolveCommand(command);
  return entryReal !== null && reqReal !== null && entryReal === reqReal;
}

function decideRun(
  allow: string[],
  denyList: string[],
  command: string,
): Decision {
  if (denyList.some((entry) => matchesRunEntry(entry, command))) {
    return reject(`run ${command} matched by a deny entry`);
  }
  return allow.some((entry) => matchesRunEntry(entry, command))
    ? GRANT
    : reject(`run ${command} not covered by any allow entry`);
}

export function decidePermission(
  lists: PermissionLists,
  request: PermissionRequest,
): Decision {
  const allow = lists.allow ?? {};
  const denyLists = lists.deny ?? {};
  switch (request.kind) {
    case "read":
    case "write":
      return decidePath(
        request.kind,
        allow[request.kind] ?? [],
        denyLists[request.kind] ?? [],
        request.path,
        request.directory,
      );
    case "net":
      return decideNet(
        allow.net ?? [],
        denyLists.net ?? [],
        request.host,
        request.port,
      );
    case "env":
      return decideEnv(allow.env ?? [], denyLists.env ?? [], request.name);
    case "run":
      return decideRun(
        allow.run ?? [],
        denyLists.run ?? [],
        request.command,
      );
  }
}

export function checkPermission(
  lists: PermissionLists,
  request: PermissionRequest,
): boolean {
  return decidePermission(lists, request).granted;
}
