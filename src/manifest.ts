import fs from "node:fs";
import path from "node:path";
import { resolvePath } from "./resolver.ts";
import type {
  AccessEvent,
  AccessKind,
  DeclKind,
  DeclaredManifest,
  PreparedDecl,
} from "./types.ts";

const ACCESS_KINDS: ReadonlySet<string> = new Set(["read", "write", "exec"]);

export function loadManifest(file: string): DeclaredManifest {
  const raw = JSON.parse(fs.readFileSync(file, "utf8")) as DeclaredManifest;
  if (typeof raw !== "object" || raw === null) {
    throw new Error(`manifest ${file} must contain a JSON object`);
  }
  return raw;
}

export function loadTrace(file: string): AccessEvent[] {
  const raw = JSON.parse(fs.readFileSync(file, "utf8")) as unknown;
  if (!Array.isArray(raw)) {
    throw new Error(`trace ${file} must contain a JSON array of access events`);
  }
  return raw.map((entry, i) => {
    if (typeof entry === "string") return { path: entry };
    if (
      typeof entry !== "object" ||
      entry === null ||
      typeof (entry as AccessEvent).path !== "string"
    ) {
      throw new Error(
        `trace ${file}: event #${i} must be a string or { "path": string, "kind"?: "read"|"write"|"exec" }`,
      );
    }
    const kind = (entry as AccessEvent).kind;
    if (kind !== undefined && !ACCESS_KINDS.has(kind)) {
      throw new Error(`trace ${file}: event #${i} has invalid kind "${kind}"`);
    }
    return { path: (entry as AccessEvent).path, ...(kind ? { kind } : {}) };
  });
}

/**
 * Pre-resolve every declared path once, so accesses can be matched
 * against both lexical and canonical forms.
 */
export function prepareDeclarations(
  manifest: DeclaredManifest,
  baseDir: string,
): PreparedDecl[] {
  const out: PreparedDecl[] = [];
  const add = (kind: DeclKind, entries: string[] | undefined): void => {
    for (const raw of entries ?? []) {
      const resolved = resolvePath(raw, baseDir);
      out.push({
        raw,
        kind,
        absolute: path.resolve(baseDir, raw),
        canonical: resolved.canonical,
        exists: resolved.exists,
      });
    }
  };
  add("file", manifest.files);
  add("directory", manifest.directories);
  add("executable", manifest.executables);
  return out;
}
