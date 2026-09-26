import type { Schema } from "./schema.ts";
import { escapePointer } from "./schema.ts";

/** A single validation failure, locating both the data and the rule. */
export interface ValidationError {
  /** JSON Pointer into the document, e.g. "/user/age" ("" = root). */
  instancePath: string;
  /** JSON Pointer into the schema, e.g. "#/properties/user/properties/age/type". */
  schemaPath: string;
  keyword: string;
  message: string;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function typeOf(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  if (typeof value === "number" && Number.isInteger(value)) return "integer";
  return typeof value;
}

function matchesType(value: unknown, type: string): boolean {
  switch (type) {
    case "integer":
      return typeof value === "number" && Number.isInteger(value);
    case "number":
      return typeof value === "number";
    case "object":
      return isPlainObject(value);
    case "array":
      return Array.isArray(value);
    case "null":
      return value === null;
    default:
      return typeof value === type;
  }
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((item, i) => deepEqual(item, b[i]))
    );
  }
  if (typeof a === "object") {
    const ka = Object.keys(a);
    const kb = Object.keys(b as Record<string, unknown>);
    return (
      ka.length === kb.length &&
      ka.every((k) =>
        deepEqual(
          (a as Record<string, unknown>)[k],
          (b as Record<string, unknown>)[k],
        ),
      )
    );
  }
  return false;
}

/**
 * Hand-written validator for the implemented subset. No third-party validator.
 * Collects all errors (does not stop at the first).
 */
export function validate(
  schema: Schema,
  instance: unknown,
  instancePath = "",
  schemaPath = "#",
  errors: ValidationError[] = [],
): ValidationError[] {
  const fail = (keyword: string, message: string, subPath = ""): void => {
    errors.push({ instancePath, schemaPath: schemaPath + subPath, keyword, message });
  };

  if (schema.enum !== undefined) {
    if (!schema.enum.some((candidate) => deepEqual(candidate, instance))) {
      fail("enum", `value ${JSON.stringify(instance)} is not one of the allowed enum values`, "/enum");
    }
  }

  if (schema.type !== undefined && !matchesType(instance, schema.type)) {
    fail(
      "type",
      `expected type ${schema.type}, got ${typeOf(instance)}`,
      "/type",
    );
    return errors; // further keyword checks are meaningless on a type mismatch
  }

  if (isPlainObject(instance)) {
    if (schema.required !== undefined) {
      for (const name of schema.required) {
        if (!(name in instance)) {
          fail("required", `missing required property "${name}"`, "/required");
        }
      }
    }
    if (schema.properties !== undefined) {
      for (const [name, sub] of Object.entries(schema.properties)) {
        if (name in instance) {
          validate(
            sub,
            instance[name],
            `${instancePath}/${escapePointer(name)}`,
            `${schemaPath}/properties/${escapePointer(name)}`,
            errors,
          );
        }
      }
    }
    if (schema.additionalProperties === false) {
      const known = new Set(Object.keys(schema.properties ?? {}));
      for (const name of Object.keys(instance)) {
        if (!known.has(name)) {
          fail(
            "additionalProperties",
            `unexpected additional property "${name}"`,
            "/additionalProperties",
          );
        }
      }
    }
  }

  if (Array.isArray(instance)) {
    if (schema.minItems !== undefined && instance.length < schema.minItems) {
      fail("minItems", `array has ${instance.length} items, minimum is ${schema.minItems}`, "/minItems");
    }
    if (schema.maxItems !== undefined && instance.length > schema.maxItems) {
      fail("maxItems", `array has ${instance.length} items, maximum is ${schema.maxItems}`, "/maxItems");
    }
    if (schema.items !== undefined) {
      instance.forEach((item, i) => {
        validate(
          schema.items as Schema,
          item,
          `${instancePath}/${i}`,
          `${schemaPath}/items`,
          errors,
        );
      });
    }
  }

  if (typeof instance === "string") {
    if (schema.minLength !== undefined && instance.length < schema.minLength) {
      fail("minLength", `string length ${instance.length} < minLength ${schema.minLength}`, "/minLength");
    }
    if (schema.maxLength !== undefined && instance.length > schema.maxLength) {
      fail("maxLength", `string length ${instance.length} > maxLength ${schema.maxLength}`, "/maxLength");
    }
    if (schema.pattern !== undefined && !new RegExp(schema.pattern).test(instance)) {
      fail("pattern", `string does not match pattern ${JSON.stringify(schema.pattern)}`, "/pattern");
    }
  }

  if (typeof instance === "number") {
    if (schema.minimum !== undefined && instance < schema.minimum) {
      fail("minimum", `${instance} < minimum ${schema.minimum}`, "/minimum");
    }
    if (schema.maximum !== undefined && instance > schema.maximum) {
      fail("maximum", `${instance} > maximum ${schema.maximum}`, "/maximum");
    }
    if (schema.exclusiveMinimum !== undefined && instance <= schema.exclusiveMinimum) {
      fail("exclusiveMinimum", `${instance} <= exclusiveMinimum ${schema.exclusiveMinimum}`, "/exclusiveMinimum");
    }
    if (schema.exclusiveMaximum !== undefined && instance >= schema.exclusiveMaximum) {
      fail("exclusiveMaximum", `${instance} >= exclusiveMaximum ${schema.exclusiveMaximum}`, "/exclusiveMaximum");
    }
  }

  return errors;
}
