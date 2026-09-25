import {
  formatPath,
  type JsonIssue,
  type JsonObject,
  type JsonValue,
  type PathSegment,
} from "./json.ts";

/** Minimal structural schema for JSON trees. */
export type Schema =
  | { readonly type: "any" }
  | { readonly type: "null" }
  | { readonly type: "boolean" }
  | { readonly type: "number" }
  | { readonly type: "string" }
  | {
      readonly type: "array";
      readonly items?: Schema;
      readonly minItems?: number;
    }
  | {
      readonly type: "object";
      readonly properties?: Readonly<Record<string, Schema>>;
      readonly required?: readonly string[];
      readonly additionalProperties?: boolean;
    };

export const ANY: Schema = { type: "any" };

/**
 * Validate a JSON tree against a schema. Every issue is reported with a
 * concrete path (e.g. "$.items[0].name"). Object key order is ignored;
 * array indices are significant and appear in paths.
 */
export function validate(
  value: JsonValue,
  schema: Schema,
  segments: PathSegment[] = [],
): JsonIssue[] {
  const issues: JsonIssue[] = [];
  const here = formatPath(segments);

  switch (schema.type) {
    case "any":
      return issues;
    case "null":
      if (value !== null) {
        issues.push({ path: here, message: `expected null, got ${kindOf(value)}` });
      }
      return issues;
    case "boolean":
    case "number":
    case "string":
      if (typeof value !== schema.type) {
        issues.push({
          path: here,
          message: `expected ${schema.type}, got ${kindOf(value)}`,
        });
      }
      return issues;
    case "array": {
      if (!Array.isArray(value)) {
        issues.push({ path: here, message: `expected array, got ${kindOf(value)}` });
        return issues;
      }
      if (schema.minItems !== undefined && value.length < schema.minItems) {
        issues.push({
          path: here,
          message: `expected at least ${schema.minItems} items, got ${value.length}`,
        });
      }
      if (schema.items) {
        for (let i = 0; i < value.length; i++) {
          issues.push(...validate(value[i], schema.items, [...segments, i]));
        }
      }
      return issues;
    }
    case "object": {
      if (typeof value !== "object" || value === null || Array.isArray(value)) {
        issues.push({ path: here, message: `expected object, got ${kindOf(value)}` });
        return issues;
      }
      const obj = value as JsonObject;
      const properties = schema.properties ?? {};
      for (const key of schema.required ?? []) {
        if (!(key in obj)) {
          issues.push({
            path: formatPath([...segments, key]),
            message: "missing required property",
          });
        }
      }
      for (const key of Object.keys(obj)) {
        const sub = properties[key];
        if (sub) {
          issues.push(...validate(obj[key], sub, [...segments, key]));
        } else if (schema.additionalProperties === false) {
          issues.push({
            path: formatPath([...segments, key]),
            message: "unexpected additional property",
          });
        }
      }
      return issues;
    }
  }
}

function kindOf(value: JsonValue): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}
