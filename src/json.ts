import { createHash } from "node:crypto";

export type JsonObject = { [key: string]: JsonValue };
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | JsonObject;

export type PathSegment = string | number;

/** Format a path like $.user.tags[2] */
export function formatPath(segments: PathSegment[]): string {
  let out = "$";
  for (const seg of segments) {
    if (typeof seg === "number") {
      out += `[${seg}]`;
    } else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(seg)) {
      out += `.${seg}`;
    } else {
      out += `[${JSON.stringify(seg)}]`;
    }
  }
  return out;
}

/**
 * Canonical serialization: object keys are sorted recursively (object key
 * order is insignificant), array order is preserved (arrays are ordered).
 */
export function canonicalize(value: JsonValue): string {
  if (value === null) return "null";
  switch (typeof value) {
    case "boolean":
    case "number":
      return JSON.stringify(value);
    case "string":
      return JSON.stringify(value);
    case "object": {
      if (Array.isArray(value)) {
        return "[" + value.map(canonicalize).join(",") + "]";
      }
      const keys = Object.keys(value).sort();
      const parts = keys.map(
        (k) => JSON.stringify(k) + ":" + canonicalize(value[k]),
      );
      return "{" + parts.join(",") + "}";
    }
  }
}

/** Stable hash of a JSON tree, insensitive to object key order. */
export function hashJson(value: JsonValue): string {
  return createHash("sha256").update(canonicalize(value)).digest("hex");
}

export function deepClone<T extends JsonValue>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

export interface JsonIssue {
  path: string;
  message: string;
}

/**
 * Verify a value is a JSON-compatible tree. Returns issues located at
 * concrete paths (non-finite numbers, undefined, functions, bigint, ...).
 */
export function checkJsonCompatible(
  value: unknown,
  segments: PathSegment[] = [],
): JsonIssue[] {
  const issues: JsonIssue[] = [];
  const here = formatPath(segments);
  if (value === null) return issues;
  switch (typeof value) {
    case "boolean":
    case "string":
      return issues;
    case "number":
      if (!Number.isFinite(value)) {
        issues.push({ path: here, message: "number is not finite" });
      }
      return issues;
    case "object": {
      if (Array.isArray(value)) {
        for (let i = 0; i < value.length; i++) {
          issues.push(...checkJsonCompatible(value[i], [...segments, i]));
        }
        return issues;
      }
      for (const key of Object.keys(value as JsonObject)) {
        issues.push(
          ...checkJsonCompatible(
            (value as JsonObject)[key],
            [...segments, key],
          ),
        );
      }
      return issues;
    }
    default:
      issues.push({
        path: here,
        message: `value of type ${typeof value} is not JSON-compatible`,
      });
      return issues;
  }
}
