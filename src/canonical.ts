import { createHash } from "node:crypto";

export function isPlainObject(value: unknown): value is Record<string, unknown> {
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    Object.getPrototypeOf(value) !== null &&
    (Object.getPrototypeOf(value) === Object.prototype ||
      Object.getPrototypeOf(value).constructor?.name === "Object")
  );
}

/**
 * JSON with recursively sorted object keys.  Undefined values are omitted and
 * array holes become null, matching JSON semantics without relying on the
 * insertion order of JavaScript objects.
 */
export function canonicalJson(value: unknown): string {
  if (value === null) return "null";
  if (value === undefined) return "null";
  const type = typeof value;
  if (type === "number") {
    if (!Number.isFinite(value as number)) return "null";
    return JSON.stringify(value);
  }
  if (type === "boolean" || type === "string") return JSON.stringify(value);
  if (type === "bigint") return JSON.stringify(String(value));
  if (value instanceof Map) {
    const object: Record<string, unknown> = {};
    for (const [key, entry] of value.entries()) {
      object[String(key)] = entry;
    }
    return canonicalJson(object);
  }
  if (value instanceof Set) {
    return canonicalJson(
      [...value].sort((a, b) =>
        canonicalJson(a).localeCompare(canonicalJson(b)),
      ),
    );
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalJson(item)).join(",")}]`;
  }
  if (type === "object") {
    const object = value as Record<string, unknown>;
    const keys = Object.keys(object)
      .filter((key) => object[key] !== undefined)
      .sort();
    return `{${keys
      .map((key) => `${JSON.stringify(key)}:${canonicalJson(object[key])}`)
      .join(",")}}`;
  }
  return JSON.stringify(String(value));
}

export function sha256Hex(value: string | Uint8Array): string {
  return createHash("sha256").update(value).digest("hex");
}

export function sha256Base64(value: string | Uint8Array): string {
  return createHash("sha256").update(value).digest("base64");
}

export function deepClone<T>(value: T): T {
  if (value === null || typeof value !== "object") return value;
  if (value instanceof Map) {
    return new Map(
      [...value.entries()].map(([key, entry]) => [
        deepClone(key),
        deepClone(entry),
      ]),
    ) as T;
  }
  if (value instanceof Set) {
    return new Set([...value].map((entry) => deepClone(entry))) as T;
  }
  if (Array.isArray(value)) {
    return value.map((entry) => deepClone(entry)) as T;
  }
  const clone: Record<string, unknown> = {};
  for (const [key, entry] of Object.entries(value as Record<string, unknown>)) {
    clone[key] = deepClone(entry);
  }
  return clone as T;
}

export function firstDefined<T>(...values: Array<T | undefined | null>): T | undefined {
  for (const value of values) {
    if (value !== undefined && value !== null) return value;
  }
  return undefined;
}

export function asString(value: unknown): string | undefined {
  if (typeof value === "string" && value.length > 0) return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return undefined;
}

export function asNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

export function asBoolean(value: unknown): boolean | undefined {
  if (typeof value === "boolean") return value;
  if (typeof value === "number") return value !== 0;
  if (isPlainObject(value)) {
    return asBoolean(value.enabled ?? value.value ?? value.flag);
  }
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (["true", "1", "yes", "on", "enabled"].includes(normalized)) return true;
    if (["false", "0", "no", "off", "disabled"].includes(normalized)) return false;
  }
  return undefined;
}

export function asStringArray(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value
      .map((item) => asString(item))
      .filter((item): item is string => item !== undefined);
  }
  const single = asString(value);
  return single === undefined ? [] : [single];
}
