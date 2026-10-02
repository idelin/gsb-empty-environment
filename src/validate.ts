/**
 * Instance validation against the supported schema subset.
 * No third-party validator is used; every error carries both the instance
 * location (JSON Pointer) and the schema location (#-prefixed pointer).
 */

import type { Decimal, DecimalOptions } from "./decimal.ts";
import {
  DecimalFormatError,
  ExponentBudgetError,
  canonical,
  isIntegerValue,
  parseDecimal,
} from "./decimal.ts";
import type { Constraints, RawNumberSchema } from "./constraints.ts";
import {
  SchemaError,
  checkConstraints,
  normalizeConstraints,
} from "./constraints.ts";

export interface ValidationError {
  /** JSON Pointer to the offending instance location, e.g. "/items/0". */
  readonly instancePath: string;
  /** Pointer to the violated rule, e.g. "#/properties/items/precision". */
  readonly schemaPath: string;
  readonly message: string;
}

export interface ValidationResult {
  readonly valid: boolean;
  readonly errors: ValidationError[];
}

const SUPPORTED_KEYWORDS = [
  "type",
  "properties",
  "required",
  "items",
  "minItems",
  "maxItems",
  "minLength",
  "maxLength",
  "enum",
  "const",
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "multipleOf",
  "precision",
  "scale",
] as const;

const TYPES = ["number", "integer", "string", "boolean", "object", "array", "null"] as const;

function escapePointer(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

function jsonTypeOf(v: unknown): string {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  return typeof v; // "number" | "string" | "boolean" | "object"
}

function checkUnsupported(schema: object, schemaPath: string): void {
  for (const key of Object.keys(schema)) {
    if (key === "$schema" || key === "$id" || key === "title" || key === "description") continue;
    if (!(SUPPORTED_KEYWORDS as readonly string[]).includes(key)) {
      throw new SchemaError(
        `unsupported keyword ${JSON.stringify(key)}; this library only implements: ${SUPPORTED_KEYWORDS.join(", ")}`,
        `${schemaPath}/${escapePointer(key)}`,
      );
    }
  }
}

function typeMatches(instance: unknown, t: string): boolean {
  const jt = jsonTypeOf(instance);
  if (t === "number" || t === "integer") {
    if (jt === "number") return Number.isFinite(instance);
    if (jt === "string") {
      // Numeric strings are validated exactly as decimal literals.
      try {
        parseDecimal(instance as string);
        return true;
      } catch {
        return false;
      }
    }
    return false;
  }
  return jt === t;
}

/** Validate a JSON instance against the supported schema subset. */
export function validate(
  instance: unknown,
  schema: object,
  opts: DecimalOptions = {},
): ValidationResult {
  const errors: ValidationError[] = [];
  walk(instance, schema, "", "#", errors, opts);
  return { valid: errors.length === 0, errors };
}

function walk(
  instance: unknown,
  schema: object,
  instancePath: string,
  schemaPath: string,
  errors: ValidationError[],
  opts: DecimalOptions,
): void {
  checkUnsupported(schema, schemaPath);
  const s = schema as Record<string, unknown>;

  if (s.const !== undefined) {
    if (!deepEqual(instance, s.const)) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/const`,
        message: `must equal ${JSON.stringify(s.const)}`,
      });
    }
  }
  if (s.enum !== undefined) {
    if (!Array.isArray(s.enum)) {
      throw new SchemaError("enum must be an array", `${schemaPath}/enum`);
    }
    if (!s.enum.some((e) => deepEqual(e, instance))) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/enum`,
        message: `must be one of ${JSON.stringify(s.enum)}`,
      });
    }
  }

  if (s.type !== undefined) {
    const t = s.type;
    if (typeof t !== "string" || !(TYPES as readonly string[]).includes(t)) {
      throw new SchemaError(`type must be one of ${TYPES.join(", ")}`, `${schemaPath}/type`);
    }
    if (!typeMatches(instance, t)) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/type`,
        message: `expected type ${t}, got ${jsonTypeOf(instance)}`,
      });
      return; // Further keyword checks would be meaningless.
    }
    if (t === "integer") {
      let d: Decimal | null = null;
      try {
        d = parseDecimal(instance as string | number, opts);
      } catch {
        d = null;
      }
      if (d !== null && !isIntegerValue(d)) {
        errors.push({
          instancePath,
          schemaPath: `${schemaPath}/type`,
          message: `${canonical(d)} is not an integer value`,
        });
      }
    }
  }

  const jt = jsonTypeOf(instance);

  if ((jt === "number" || jt === "string") && hasNumberKeywords(s)) {
    validateNumber(instance as number | string, s, instancePath, schemaPath, errors, opts);
  }

  if (jt === "string") {
    const str = instance as string;
    if (s.minLength !== undefined && str.length < (s.minLength as number)) {
      errors.push({ instancePath, schemaPath: `${schemaPath}/minLength`, message: `string shorter than ${s.minLength}` });
    }
    if (s.maxLength !== undefined && str.length > (s.maxLength as number)) {
      errors.push({ instancePath, schemaPath: `${schemaPath}/maxLength`, message: `string longer than ${s.maxLength}` });
    }
  }

  if (jt === "object" && instance !== null) {
    const obj = instance as Record<string, unknown>;
    if (s.required !== undefined) {
      if (!Array.isArray(s.required)) {
        throw new SchemaError("required must be an array", `${schemaPath}/required`);
      }
      for (const name of s.required as string[]) {
        if (!(name in obj)) {
          errors.push({
            instancePath,
            schemaPath: `${schemaPath}/required`,
            message: `missing required property ${JSON.stringify(name)}`,
          });
        }
      }
    }
    if (s.properties !== undefined) {
      const props = s.properties as Record<string, object>;
      for (const [name, sub] of Object.entries(props)) {
        if (name in obj) {
          walk(
            obj[name],
            sub,
            `${instancePath}/${escapePointer(name)}`,
            `${schemaPath}/properties/${escapePointer(name)}`,
            errors,
            opts,
          );
        }
      }
    }
  }

  if (jt === "array") {
    const arr = instance as unknown[];
    if (s.minItems !== undefined && arr.length < (s.minItems as number)) {
      errors.push({ instancePath, schemaPath: `${schemaPath}/minItems`, message: `fewer than ${s.minItems} items` });
    }
    if (s.maxItems !== undefined && arr.length > (s.maxItems as number)) {
      errors.push({ instancePath, schemaPath: `${schemaPath}/maxItems`, message: `more than ${s.maxItems} items` });
    }
    if (s.items !== undefined) {
      arr.forEach((item, i) => {
        walk(item, s.items as object, `${instancePath}/${i}`, `${schemaPath}/items`, errors, opts);
      });
    }
  }
}

function hasNumberKeywords(s: Record<string, unknown>): boolean {
  return (
    s.minimum !== undefined ||
    s.maximum !== undefined ||
    s.exclusiveMinimum !== undefined ||
    s.exclusiveMaximum !== undefined ||
    s.multipleOf !== undefined ||
    s.precision !== undefined ||
    s.scale !== undefined
  );
}

function validateNumber(
  instance: number | string,
  s: Record<string, unknown>,
  instancePath: string,
  schemaPath: string,
  errors: ValidationError[],
  opts: DecimalOptions,
): void {
  let d: Decimal;
  try {
    d = parseDecimal(instance, opts);
  } catch (err) {
    if (err instanceof DecimalFormatError || err instanceof ExponentBudgetError) {
      errors.push({ instancePath, schemaPath, message: err.message });
      return;
    }
    throw err;
  }
  const numberSchema: RawNumberSchema = {};
  for (const key of [
    "minimum",
    "maximum",
    "exclusiveMinimum",
    "exclusiveMaximum",
    "multipleOf",
    "precision",
    "scale",
  ] as const) {
    if (s[key] !== undefined) (numberSchema as Record<string, unknown>)[key] = s[key];
  }
  const constraints: Constraints = normalizeConstraints(numberSchema, opts, schemaPath);
  for (const v of checkConstraints(d, constraints)) {
    errors.push({ instancePath, schemaPath: `${schemaPath}/${v.rule}`, message: v.message });
  }
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (typeof a === "number" && typeof b === "number") {
    // Numeric equality is exact decimal equality: -0 equals 0, 1e3 equals 1000.
    try {
      const da = parseDecimal(a);
      const db = parseDecimal(b);
      return canonical(da) === canonical(db);
    } catch {
      return false;
    }
  }
  if (typeof a !== typeof b) return false;
  if (a === null || b === null) return a === b;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((x, i) => deepEqual(x, b[i]));
  }
  if (typeof a === "object") {
    const ka = Object.keys(a as object);
    const kb = Object.keys(b as object);
    return (
      ka.length === kb.length &&
      ka.every((k) =>
        deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
      )
    );
  }
  return a === b;
}
