import type { JsonObject, JsonValue, PathSegment } from './types.ts';

export function isPlainObject(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** JSON Pointer (RFC 6901) for an array of path segments; root is the empty string. */
export function toPointer(segments: PathSegment[]): string {
  if (segments.length === 0) return '';
  return (
    '/' +
    segments
      .map((seg) => String(seg).replace(/~/g, '~0').replace(/\//g, '~1'))
      .join('/')
  );
}

/** Deep equality: object key order ignored, array order significant. */
export function jsonEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((item, i) => jsonEqual(item, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const keysA = Object.keys(a);
    const keysB = Object.keys(b);
    return (
      keysA.length === keysB.length &&
      keysA.every((k) => k in b && jsonEqual(a[k], b[k]))
    );
  }
  return false;
}

/** Deep copy with object keys sorted, so serialized output is key-order independent. */
export function canonicalize(value: JsonValue): JsonValue {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isPlainObject(value)) {
    const out: JsonObject = {};
    for (const key of Object.keys(value).sort()) {
      out[key] = canonicalize(value[key]);
    }
    return out;
  }
  return value;
}

export function deepCopy<T extends JsonValue>(value: T): T {
  return structuredClone(value);
}
