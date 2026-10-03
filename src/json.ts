export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type PathSeg = string | number;

export function deepClone(v: JsonValue): JsonValue {
  if (v === null || typeof v !== "object") return v;
  if (Array.isArray(v)) return v.map(deepClone);
  const out: { [k: string]: JsonValue } = {};
  for (const k of Object.keys(v)) out[k] = deepClone(v[k]);
  return out;
}

export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (a === null || b === null || typeof a !== "object" || typeof b !== "object") return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!deepEqual(a[i], b[i])) return false;
    return true;
  }
  const ao = a as { [k: string]: JsonValue };
  const bo = b as { [k: string]: JsonValue };
  const ka = Object.keys(ao);
  const kb = Object.keys(bo);
  if (ka.length !== kb.length) return false;
  for (const k of ka) {
    if (!Object.prototype.hasOwnProperty.call(bo, k)) return false;
    if (!deepEqual(ao[k], bo[k])) return false;
  }
  return true;
}

/** Parses "a.b[0].c" or "/a/0/c" into segments. */
export function parsePath(path: string | PathSeg[]): PathSeg[] {
  if (Array.isArray(path)) return path.slice();
  const segs: PathSeg[] = [];
  if (path.startsWith("/")) {
    for (const part of path.slice(1).split("/")) {
      if (part === "") continue;
      segs.push(/^\d+$/.test(part) ? Number(part) : part);
    }
    return segs;
  }
  let i = 0;
  let cur = "";
  while (i < path.length) {
    const ch = path[i];
    if (ch === ".") {
      if (cur) {
        segs.push(cur);
        cur = "";
      }
      i++;
    } else if (ch === "[") {
      if (cur) {
        segs.push(cur);
        cur = "";
      }
      const j = path.indexOf("]", i);
      if (j < 0) throw new Error(`bad path: ${path}`);
      segs.push(Number(path.slice(i + 1, j)));
      i = j + 1;
    } else {
      cur += ch;
      i++;
    }
  }
  if (cur) segs.push(cur);
  return segs;
}

export function formatPath(segs: PathSeg[]): string {
  if (segs.length === 0) return "/";
  return "/" + segs.map(String).join("/");
}

export function typeName(v: JsonValue): string {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  return typeof v;
}
