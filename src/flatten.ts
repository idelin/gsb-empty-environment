import { formatPath, type PathSegment } from "./path.ts";

export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

function isPlainObject(value: JsonValue): value is { [key: string]: JsonValue } {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Flattens a parsed JSON value into leaf-path -> scalar entries.
 * Objects become dotted segments, array elements become [index] segments.
 * Empty objects/arrays are kept as leaves so their existence is not lost.
 */
export function flattenJson(
  value: JsonValue,
  basePath: readonly PathSegment[] = [],
  out: Map<string, JsonValue> = new Map(),
): Map<string, JsonValue> {
  if (isPlainObject(value)) {
    const keys = Object.keys(value);
    if (keys.length === 0 && basePath.length > 0) {
      out.set(formatPath(basePath), value);
    }
    for (const key of keys) {
      flattenJson(value[key] as JsonValue, [...basePath, key], out);
    }
    return out;
  }
  if (Array.isArray(value)) {
    if (value.length === 0 && basePath.length > 0) {
      out.set(formatPath(basePath), value);
    }
    for (let index = 0; index < value.length; index += 1) {
      flattenJson(value[index] as JsonValue, [...basePath, index], out);
    }
    return out;
  }
  out.set(formatPath(basePath), value);
  return out;
}

/**
 * Infers the set of leaf field paths of a JSON document, sorted for
 * deterministic output. This is the "schema" of the virtual columns.
 */
export function inferPaths(sample: JsonValue): string[] {
  return [...flattenJson(sample).keys()].sort();
}
