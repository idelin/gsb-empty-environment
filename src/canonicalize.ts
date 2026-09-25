import type { JsonValue } from "./types.ts";

export function canonicalize(value: JsonValue): string {
  if (value === null) return "null";
  switch (typeof value) {
    case "boolean":
      return value ? "true" : "false";
    case "number":
      return Object.is(value, -0) ? "0" : String(value);
    case "string":
      return JSON.stringify(value);
    case "object": {
      if (Array.isArray(value)) {
        return "[" + value.map((item) => canonicalize(item)).join(",") + "]";
      }
      const keys = Object.keys(value).sort();
      const parts = keys.map(
        (k) => JSON.stringify(k) + ":" + canonicalize(value[k] as JsonValue),
      );
      return "{" + parts.join(",") + "}";
    }
    default:
      throw new Error(`Unsupported JSON value type: ${typeof value}`);
  }
}
