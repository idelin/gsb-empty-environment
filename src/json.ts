export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

export type Path = ReadonlyArray<string | number>;

/** Reference to a subtree stored in a chunk store (content not loaded inline). */
export class ChunkRef {
  readonly id: string;
  constructor(id: string) {
    this.id = id;
  }
}

/** Read side of a chunk store. `get` counts as a content read; `nodeOf` does not. */
export interface ChunkReader {
  get(id: string): Json;
  nodeOf(id: string): import('./digest.ts').DigestNode;
}

export class JsonDagError extends Error {
  readonly path: Path;
  constructor(message: string, path: Path) {
    super(`${message} (at ${formatPath(path)})`);
    this.name = 'JsonDagError';
    this.path = path;
  }
}

export class DigestMismatchError extends JsonDagError {
  constructor(message: string, path: Path) {
    super(message, path);
    this.name = 'DigestMismatchError';
  }
}

export class PatchApplyError extends JsonDagError {
  readonly opIndex: number;
  restored: boolean;
  constructor(message: string, path: Path, opIndex: number) {
    super(message, path);
    this.name = 'PatchApplyError';
    this.opIndex = opIndex;
    this.restored = false;
  }
}

export function formatPath(path: Path): string {
  let out = '$';
  for (const seg of path) {
    if (typeof seg === 'number') out += `[${seg}]`;
    else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(seg)) out += `.${seg}`;
    else out += `[${JSON.stringify(seg)}]`;
  }
  return out;
}

export function isObject(value: unknown): value is Record<string, Json> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Canonical form: object keys sorted recursively, array order preserved. Idempotent. */
export function normalize(value: Json): Json {
  if (Array.isArray(value)) return value.map(normalize) as Json;
  if (isObject(value)) {
    const out: Record<string, Json> = {};
    for (const key of Object.keys(value).sort()) out[key] = normalize(value[key]);
    return out;
  }
  return value;
}

/** Structural equality; object key order is insignificant. */
export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((v, i) => deepEqual(v, (b as Json[])[i]))
    );
  }
  const ka = Object.keys(a).sort();
  const kb = Object.keys(b).sort();
  return (
    ka.length === kb.length &&
    ka.every((k, i) => k === kb[i] && deepEqual((a as Record<string, Json>)[k], (b as Record<string, Json>)[k]))
  );
}

export function getAt(root: Json, path: Path): Json {
  let cur: unknown = root;
  for (const seg of path) {
    if (cur === null || typeof cur !== 'object' || !(seg in (cur as object))) {
      throw new JsonDagError('path not found', path);
    }
    cur = (cur as Record<string | number, Json>)[seg as string];
  }
  return cur as Json;
}
