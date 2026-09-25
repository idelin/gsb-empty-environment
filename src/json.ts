export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export function deepClone<T extends JsonValue>(value: T): T {
  return structuredClone(value);
}

/**
 * Canonical serialization used ONLY for hashing / equality:
 * object keys are sorted (objects are unordered), array order is
 * preserved (arrays are ordered). Never used to rewrite user data.
 */
export function canonicalStringify(value: JsonValue): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return "[" + value.map(canonicalStringify).join(",") + "]";
  }
  const keys = Object.keys(value).sort();
  const body = keys
    .map((key) => JSON.stringify(key) + ":" + canonicalStringify(value[key]))
    .join(",");
  return "{" + body + "}";
}

export function stateHash(value: JsonValue): string {
  return canonicalStringify(value);
}

export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  return canonicalStringify(a) === canonicalStringify(b);
}

export function deepFreeze(value: JsonValue): void {
  if (value !== null && typeof value === "object") {
    if (Array.isArray(value)) {
      for (const item of value) deepFreeze(item);
    } else {
      for (const key of Object.keys(value)) deepFreeze(value[key]);
    }
    Object.freeze(value);
  }
}
