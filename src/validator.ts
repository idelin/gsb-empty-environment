import type { Schema } from "./schema.ts";

/**
 * Independent validator for the supported subset. Written from scratch
 * (no ajv or any other library). Every error carries BOTH the instance
 * path and the rule (schema) path, as JSON Pointers.
 */
export interface ValidationError {
  instancePath: string;
  schemaPath: string;
  message: string;
}

const EPS = 1e-9;

export function typeOf(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  if (typeof value === "number") return Number.isInteger(value) ? "integer" : "number";
  return typeof value;
}

export function matchesType(value: unknown, type: string): boolean {
  const actual = typeOf(value);
  if (type === "number") return actual === "number" || actual === "integer";
  return actual === type;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keysA = Object.keys(a);
  const keysB = Object.keys(b);
  if (keysA.length !== keysB.length) return false;
  return keysA.every(
    (k) =>
      Object.prototype.hasOwnProperty.call(b, k) &&
      deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
  );
}

export function validate(value: unknown, schema: Schema): ValidationError[] {
  const errors: ValidationError[] = [];
  visit(value, schema, "", "", errors);
  return errors;
}

function visit(
  value: unknown,
  schema: Schema,
  instancePath: string,
  schemaPath: string,
  errors: ValidationError[],
): void {
  const fail = (keyword: string, message: string) =>
    errors.push({ instancePath, schemaPath: `${schemaPath}/${keyword}`, message });

  if (schema.const !== undefined && !deepEqual(value, schema.const))
    fail("const", `expected constant ${JSON.stringify(schema.const)}, got ${JSON.stringify(value)}`);

  if (schema.enum !== undefined && !schema.enum.some((e) => deepEqual(e, value)))
    fail("enum", `${JSON.stringify(value)} is not one of ${JSON.stringify(schema.enum)}`);

  if (schema.type !== undefined && !matchesType(value, schema.type))
    fail("type", `expected type ${schema.type}, got ${typeOf(value)}`);

  if (typeof value === "number") {
    if (schema.minimum !== undefined && value < schema.minimum)
      fail("minimum", `${value} is less than minimum ${schema.minimum}`);
    if (schema.maximum !== undefined && value > schema.maximum)
      fail("maximum", `${value} is greater than maximum ${schema.maximum}`);
    if (schema.exclusiveMinimum !== undefined && value <= schema.exclusiveMinimum)
      fail("exclusiveMinimum", `${value} is not greater than exclusiveMinimum ${schema.exclusiveMinimum}`);
    if (schema.exclusiveMaximum !== undefined && value >= schema.exclusiveMaximum)
      fail("exclusiveMaximum", `${value} is not less than exclusiveMaximum ${schema.exclusiveMaximum}`);
    if (schema.multipleOf !== undefined) {
      const q = value / schema.multipleOf;
      if (Math.abs(q - Math.round(q)) > EPS)
        fail("multipleOf", `${value} is not a multiple of ${schema.multipleOf}`);
    }
  }

  if (typeof value === "string") {
    if (schema.minLength !== undefined && value.length < schema.minLength)
      fail("minLength", `length ${value.length} is less than minLength ${schema.minLength}`);
    if (schema.maxLength !== undefined && value.length > schema.maxLength)
      fail("maxLength", `length ${value.length} is greater than maxLength ${schema.maxLength}`);
    if (schema.pattern !== undefined && !new RegExp(schema.pattern).test(value))
      fail("pattern", `${JSON.stringify(value)} does not match pattern ${schema.pattern}`);
  }

  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems)
      fail("minItems", `${value.length} items is less than minItems ${schema.minItems}`);
    if (schema.maxItems !== undefined && value.length > schema.maxItems)
      fail("maxItems", `${value.length} items is greater than maxItems ${schema.maxItems}`);
    if (schema.items !== undefined)
      value.forEach((item, i) =>
        visit(item, schema.items as Schema, `${instancePath}/${i}`, `${schemaPath}/items`, errors),
      );
  }

  if (typeof value === "object" && value !== null && !Array.isArray(value)) {
    const obj = value as Record<string, unknown>;
    if (schema.required !== undefined)
      for (const key of schema.required)
        if (!(key in obj)) fail("required", `missing required property "${key}"`);
    if (schema.properties !== undefined)
      for (const [key, sub] of Object.entries(schema.properties))
        if (key in obj)
          visit(obj[key], sub, `${instancePath}/${key}`, `${schemaPath}/properties/${key}`, errors);
    if (schema.additionalProperties === false && schema.properties !== undefined)
      for (const key of Object.keys(obj))
        if (!(key in schema.properties)) fail("additionalProperties", `unexpected property "${key}"`);
  }
}
