import type { JsonValue } from "./types.ts";
import { PseudonymizationError } from "./errors.ts";

/**
 * Canonical serialization: object keys sorted recursively (key order insignificant),
 * array order preserved. Throws NON_JSON_VALUE with the concrete path on anything
 * that is not JSON-compatible (undefined, functions, symbols, bigint, cycles,
 * NaN/Infinity, non-plain objects).
 */
export function canonicalize(value: unknown, path = "$"): string {
  return serialize(value, path, new Set());
}

function serialize(value: unknown, path: string, seen: Set<unknown>): string {
  if (value === null) return "null";
  switch (typeof value) {
    case "string":
      return JSON.stringify(value);
    case "boolean":
      return value ? "true" : "false";
    case "number":
      if (!Number.isFinite(value)) {
        throw new PseudonymizationError("NON_JSON_VALUE", path, "non-finite number is not JSON-compatible");
      }
      return JSON.stringify(value);
    case "undefined":
    case "function":
    case "symbol":
    case "bigint":
      throw new PseudonymizationError("NON_JSON_VALUE", path, `type "${typeof value}" is not JSON-compatible`);
    case "object":
      break;
  }
  if (seen.has(value)) {
    throw new PseudonymizationError("NON_JSON_VALUE", path, "cyclic structure is not JSON-compatible");
  }
  seen.add(value);
  try {
    if (Array.isArray(value)) {
      const items = value.map((item, i) => serialize(item, `${path}[${i}]`, seen));
      return `[${items.join(",")}]`;
    }
    const proto = Object.getPrototypeOf(value);
    if (proto !== Object.prototype && proto !== null) {
      throw new PseudonymizationError("NON_JSON_VALUE", path, "non-plain object is not JSON-compatible");
    }
    const keys = Object.keys(value as Record<string, unknown>).sort();
    const parts = keys.map(
      (k) => `${JSON.stringify(k)}:${serialize((value as Record<string, unknown>)[k], `${path}.${k}`, seen)}`,
    );
    return `{${parts.join(",")}}`;
  } finally {
    seen.delete(value);
  }
}

/** Type guard mirroring canonicalize's acceptance rules, for traversal-time checks. */
export function isJsonLeaf(value: unknown): value is string | number | boolean | null {
  return value === null || ["string", "number", "boolean"].includes(typeof value);
}
