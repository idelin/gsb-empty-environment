// Hand-written validator for the implemented subset in types.ts.
// No third-party or generated validator is used. Every error carries BOTH
// an instance path ($.a.b[0]) and a rule path (#/properties/a/...).

import type { PrimitiveType, Schema, ValidationError } from "./types.ts";

export function joinInstancePath(parent: string, key: string): string {
  return /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(key)
    ? `${parent}.${key}`
    : `${parent}[${JSON.stringify(key)}]`;
}

function escapePointer(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

function matchesType(value: unknown, t: PrimitiveType): boolean {
  switch (t) {
    case "null":
      return value === null;
    case "array":
      return Array.isArray(value);
    case "object":
      return typeof value === "object" && value !== null && !Array.isArray(value);
    case "integer":
      return typeof value === "number" && Number.isInteger(value);
    case "number":
      return typeof value === "number";
    case "string":
      return typeof value === "string";
    case "boolean":
      return typeof value === "boolean";
  }
}

function typeName(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  if (typeof value === "number") return Number.isInteger(value) ? "integer" : "number";
  return typeof value;
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const ka = Object.keys(a as object);
  const kb = Object.keys(b as object);
  if (ka.length !== kb.length) return false;
  return ka.every((k) =>
    deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
  );
}

export function validate(
  value: unknown,
  schema: Schema,
  instancePath = "$",
  rulePath = "#",
): ValidationError[] {
  const errors: ValidationError[] = [];

  if (schema.type !== undefined) {
    const allowed = Array.isArray(schema.type) ? schema.type : [schema.type];
    if (!allowed.some((t) => matchesType(value, t))) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/type`,
        message: `expected type ${allowed.join(" | ")}, got ${typeName(value)}`,
      });
      return errors; // further keyword checks are meaningless after a type failure
    }
  }

  if (schema.enum !== undefined && !schema.enum.some((e) => deepEqual(e, value))) {
    errors.push({
      instancePath,
      rulePath: `${rulePath}/enum`,
      message: `value ${JSON.stringify(value)} is not one of the allowed enum values`,
    });
  }

  if (typeof value === "number") {
    if (schema.minimum !== undefined && value < schema.minimum) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/minimum`,
        message: `value ${value} is below minimum ${schema.minimum}`,
      });
    }
    if (schema.maximum !== undefined && value > schema.maximum) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/maximum`,
        message: `value ${value} exceeds maximum ${schema.maximum}`,
      });
    }
  }

  if (typeof value === "string") {
    if (schema.minLength !== undefined && value.length < schema.minLength) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/minLength`,
        message: `string length ${value.length} is below minLength ${schema.minLength}`,
      });
    }
    if (schema.maxLength !== undefined && value.length > schema.maxLength) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/maxLength`,
        message: `string length ${value.length} exceeds maxLength ${schema.maxLength}`,
      });
    }
  }

  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/minItems`,
        message: `array length ${value.length} is below minItems ${schema.minItems}`,
      });
    }
    if (schema.maxItems !== undefined && value.length > schema.maxItems) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/maxItems`,
        message: `array length ${value.length} exceeds maxItems ${schema.maxItems}`,
      });
    }
    if (schema.items !== undefined) {
      value.forEach((item, i) => {
        errors.push(...validate(item, schema.items as Schema, `${instancePath}[${i}]`, `${rulePath}/items`));
      });
    }
  }

  if (typeof value === "object" && value !== null && !Array.isArray(value)) {
    const obj = value as Record<string, unknown>;
    for (const key of schema.required ?? []) {
      if (!(key in obj)) {
        errors.push({
          instancePath,
          rulePath: `${rulePath}/required`,
          message: `missing required property ${JSON.stringify(key)}`,
        });
      }
    }
    // NOTE: additionalProperties is intentionally unsupported. Properties we
    // have never observed are UNKNOWN, not forbidden.
    for (const [key, sub] of Object.entries(schema.properties ?? {})) {
      if (key in obj) {
        errors.push(
          ...validate(
            obj[key],
            sub,
            joinInstancePath(instancePath, key),
            `${rulePath}/properties/${escapePointer(key)}`,
          ),
        );
      }
    }
  }

  return errors;
}
