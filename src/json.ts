/** JSON-compatible values. Object keys are unordered; arrays are ordered. */
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Deep equality with unordered object keys and ordered arrays. */
export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (typeof a !== "object" || a === null || b === null) return false;
  const aArr = Array.isArray(a);
  const bArr = Array.isArray(b);
  if (aArr !== bArr) return false;
  if (aArr && bArr) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (!deepEqual(a[i], (b as unknown[])[i])) return false;
    }
    return true;
  }
  const aObj = a as Record<string, unknown>;
  const bObj = b as Record<string, unknown>;
  const aKeys = Object.keys(aObj);
  const bKeys = Object.keys(bObj);
  if (aKeys.length !== bKeys.length) return false;
  for (const k of aKeys) {
    if (!Object.prototype.hasOwnProperty.call(bObj, k)) return false;
    if (!deepEqual(aObj[k], bObj[k])) return false;
  }
  return true;
}

/** Canonicalize: sort object keys recursively, keep array order. */
export function normalize(v: JsonValue): JsonValue {
  if (Array.isArray(v)) return v.map(normalize);
  if (isPlainObject(v)) {
    const out: Record<string, JsonValue> = {};
    for (const k of Object.keys(v).sort()) {
      out[k] = normalize(v[k] as JsonValue);
    }
    return out;
  }
  return v;
}

/** Escape one JSON Pointer segment (RFC 6901). */
export function escapeSegment(seg: string): string {
  return seg.replace(/~/g, "~0").replace(/\//g, "~1");
}

/** Join a JSON Pointer base with one segment. */
export function pointerJoin(base: string, seg: string | number): string {
  return base + "/" + escapeSegment(String(seg));
}
