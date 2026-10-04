import * as fs from "fs";
import { DeclEntry, Manifest } from "./types";

export function loadManifest(file: string): Manifest {
  const raw = JSON.parse(fs.readFileSync(file, "utf8")) as Manifest;
  if (!raw || !Array.isArray(raw.declarations)) {
    throw new Error(`Invalid manifest ${file}: missing "declarations" array`);
  }
  for (const d of raw.declarations) {
    if (typeof d.path !== "string" || (d.type !== "file" && d.type !== "dir")) {
      throw new Error(
        `Invalid manifest entry in ${file}: ${JSON.stringify(d)}`
      );
    }
  }
  return raw;
}

export function loadJsonFile<T>(file: string): T {
  return JSON.parse(fs.readFileSync(file, "utf8")) as T;
}

/** Normalize declaration paths to absolute form against the base dir. */
export function normalizeDeclarations(
  decls: DeclEntry[],
  base: string,
  normalizeAbs: (p: string, base: string) => string
): DeclEntry[] {
  return decls.map((d) => ({
    ...d,
    path: normalizeAbs(d.path, base),
    recursive: d.type === "dir" ? d.recursive !== false : d.recursive,
  }));
}
