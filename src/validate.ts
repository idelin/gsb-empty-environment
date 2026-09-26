import type { JsonSchema } from "./schema.ts";

export interface ValidationError {
  /** JSON Pointer into the instance, e.g. "/12/name". "" is the root. */
  instancePath: string;
  /** JSON Pointer into the schema, e.g. "/items/properties/name/type". */
  schemaPath: string;
  message: string;
}

export function escapePointer(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

/** Structural, key-order independent serialization used for uniqueness. */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value) ?? "undefined";
  }
  if (Array.isArray(value)) {
    return "[" + value.map(canonicalize).join(",") + "]";
  }
  const rec = value as Record<string, unknown>;
  const keys = Object.keys(rec).sort();
  return (
    "{" +
    keys.map((k) => JSON.stringify(k) + ":" + canonicalize(rec[k])).join(",") +
    "}"
  );
}

function typeOf(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

function deepEqual(a: unknown, b: unknown): boolean {
  return canonicalize(a) === canonicalize(b);
}

/**
 * Validate a single value against the supported schema subset.
 * Errors are appended to `errors`; validation always continues so that
 * all violations of one value are reported together.
 */
export function validateValue(
  value: unknown,
  schema: JsonSchema,
  instancePath: string,
  schemaPath: string,
  errors: ValidationError[],
): void {
  const fail = (keyword: string, message: string) => {
    errors.push({
      instancePath,
      schemaPath: schemaPath + "/" + keyword,
      message,
    });
  };

  if ("const" in schema && schema.const !== undefined) {
    if (!deepEqual(value, schema.const)) {
      fail("const", `must be equal to constant ${JSON.stringify(schema.const)}`);
      return;
    }
  }
  if ("enum" in schema && schema.enum !== undefined) {
    if (!schema.enum.some((e) => deepEqual(value, e))) {
      fail("enum", `must be one of ${JSON.stringify(schema.enum)}`);
      return;
    }
  }
  if (!("type" in schema) || schema.type === undefined) return;

  const actual = typeOf(value);
  switch (schema.type) {
    case "null":
      if (actual !== "null") fail("type", `must be null, got ${actual}`);
      return;
    case "boolean":
      if (actual !== "boolean") fail("type", `must be boolean, got ${actual}`);
      return;
    case "number":
    case "integer": {
      if (actual !== "number") {
        fail("type", `must be ${schema.type}, got ${actual}`);
        return;
      }
      const num = value as number;
      if (schema.type === "integer" && !Number.isInteger(num)) {
        fail("type", `must be integer, got ${num}`);
      }
      if (schema.minimum !== undefined && num < schema.minimum) {
        fail("minimum", `must be >= ${schema.minimum}, got ${num}`);
      }
      if (schema.maximum !== undefined && num > schema.maximum) {
        fail("maximum", `must be <= ${schema.maximum}, got ${num}`);
      }
      return;
    }
    case "string": {
      if (actual !== "string") {
        fail("type", `must be string, got ${actual}`);
        return;
      }
      const str = value as string;
      if (schema.minLength !== undefined && str.length < schema.minLength) {
        fail("minLength", `must have length >= ${schema.minLength}`);
      }
      if (schema.maxLength !== undefined && str.length > schema.maxLength) {
        fail("maxLength", `must have length <= ${schema.maxLength}`);
      }
      if (schema.pattern !== undefined && !new RegExp(schema.pattern).test(str)) {
        fail("pattern", `must match pattern ${schema.pattern}`);
      }
      return;
    }
    case "object": {
      if (actual !== "object") {
        fail("type", `must be object, got ${actual}`);
        return;
      }
      const obj = value as Record<string, unknown>;
      if (schema.required) {
        for (const key of schema.required) {
          if (!(key in obj)) {
            errors.push({
              instancePath,
              schemaPath: schemaPath + "/required",
              message: `must have required property '${key}'`,
            });
          }
        }
      }
      if (schema.properties) {
        for (const [key, propSchema] of Object.entries(schema.properties)) {
          if (key in obj) {
            validateValue(
              obj[key],
              propSchema,
              instancePath + "/" + escapePointer(key),
              schemaPath + "/properties/" + escapePointer(key),
              errors,
            );
          }
        }
      }
      if (schema.additionalProperties === false) {
        const allowed = new Set(Object.keys(schema.properties ?? {}));
        for (const key of Object.keys(obj)) {
          if (!allowed.has(key)) {
            errors.push({
              instancePath: instancePath + "/" + escapePointer(key),
              schemaPath: schemaPath + "/additionalProperties",
              message: `must NOT have additional property '${key}'`,
            });
          }
        }
      }
      return;
    }
    case "array": {
      if (actual !== "array") {
        fail("type", `must be array, got ${actual}`);
        return;
      }
      const arr = value as unknown[];
      if (schema.minItems !== undefined && arr.length < schema.minItems) {
        fail("minItems", `must have >= ${schema.minItems} items, got ${arr.length}`);
      }
      if (schema.maxItems !== undefined && arr.length > schema.maxItems) {
        fail("maxItems", `must have <= ${schema.maxItems} items, got ${arr.length}`);
      }
      if (schema.uniqueItems === true) {
        const seen = new Set<string>();
        for (let i = 0; i < arr.length; i++) {
          const hash = canonicalize(arr[i]);
          if (seen.has(hash)) {
            errors.push({
              instancePath: instancePath + "/" + i,
              schemaPath: schemaPath + "/uniqueItems",
              message: `must be unique (duplicates an earlier item)`,
            });
            break;
          }
          seen.add(hash);
        }
      }
      if (schema.items) {
        for (let i = 0; i < arr.length; i++) {
          validateValue(
            arr[i],
            schema.items,
            instancePath + "/" + i,
            schemaPath + "/items",
            errors,
          );
        }
      }
      return;
    }
  }
}
