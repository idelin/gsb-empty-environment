import type { PathSegment, ValidationError } from "./errors.ts";

/**
 * Hand-rolled validator for a deliberately small JSON-Schema-like subset.
 * Supported keywords (and nothing else):
 *   type, properties, required, items, additionalProperties (boolean),
 *   enum, const, minimum, maximum, minLength, maxLength, minItems, maxItems
 * Every error carries both the instance path and the schema (rule) path.
 */
export interface Schema {
  type?: string | string[];
  properties?: Record<string, Schema>;
  required?: string[];
  items?: Schema;
  additionalProperties?: boolean;
  enum?: unknown[];
  const?: unknown;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
}

export interface ValidateOptions {
  schemaVersion?: string;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function typeMatches(type: string, value: unknown): boolean {
  switch (type) {
    case "string":
      return typeof value === "string";
    case "number":
      return typeof value === "number";
    case "integer":
      return Number.isInteger(value);
    case "boolean":
      return typeof value === "boolean";
    case "null":
      return value === null;
    case "array":
      return Array.isArray(value);
    case "object":
      return isPlainObject(value);
    default:
      return false;
  }
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((v, i) => deepEqual(v, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    return (
      ka.length === kb.length && ka.every((k) => deepEqual(a[k], b[k]))
    );
  }
  return false;
}

export function validate(
  schema: Schema,
  data: unknown,
  options: ValidateOptions = {},
): ValidationError[] {
  const errors: ValidationError[] = [];
  const schemaVersion = options.schemaVersion ?? "unversioned";

  const push = (
    rule: string,
    instancePath: PathSegment[],
    schemaPath: string,
    message: string,
    params: Record<string, unknown>,
  ): void => {
    errors.push({ schemaVersion, rule, instancePath, schemaPath, message, params });
  };

  const walk = (
    node: Schema,
    value: unknown,
    instancePath: PathSegment[],
    schemaPath: string,
  ): void => {
    if (node.type !== undefined) {
      const types = Array.isArray(node.type) ? node.type : [node.type];
      if (!types.some((t) => typeMatches(t, value))) {
        push(
          "type",
          instancePath,
          `${schemaPath}/type`,
          `must be ${types.join(" or ")}`,
          { type: node.type },
        );
        return; // type mismatch: deeper keywords are meaningless
      }
    }

    if (node.enum !== undefined && !node.enum.some((v) => deepEqual(v, value))) {
      push("enum", instancePath, `${schemaPath}/enum`, "must be one of the allowed values", {
        allowed: node.enum,
      });
    }

    if (node.const !== undefined && !deepEqual(node.const, value)) {
      push("const", instancePath, `${schemaPath}/const`, "must equal the constant value", {
        const: node.const,
      });
    }

    if (typeof value === "number") {
      if (node.minimum !== undefined && value < node.minimum) {
        push("minimum", instancePath, `${schemaPath}/minimum`, `must be >= ${node.minimum}`, {
          minimum: node.minimum,
        });
      }
      if (node.maximum !== undefined && value > node.maximum) {
        push("maximum", instancePath, `${schemaPath}/maximum`, `must be <= ${node.maximum}`, {
          maximum: node.maximum,
        });
      }
    }

    if (typeof value === "string") {
      if (node.minLength !== undefined && value.length < node.minLength) {
        push(
          "minLength",
          instancePath,
          `${schemaPath}/minLength`,
          `must have length >= ${node.minLength}`,
          { minLength: node.minLength },
        );
      }
      if (node.maxLength !== undefined && value.length > node.maxLength) {
        push(
          "maxLength",
          instancePath,
          `${schemaPath}/maxLength`,
          `must have length <= ${node.maxLength}`,
          { maxLength: node.maxLength },
        );
      }
    }

    if (Array.isArray(value)) {
      if (node.minItems !== undefined && value.length < node.minItems) {
        push(
          "minItems",
          instancePath,
          `${schemaPath}/minItems`,
          `must have at least ${node.minItems} items`,
          { minItems: node.minItems },
        );
      }
      if (node.maxItems !== undefined && value.length > node.maxItems) {
        push(
          "maxItems",
          instancePath,
          `${schemaPath}/maxItems`,
          `must have at most ${node.maxItems} items`,
          { maxItems: node.maxItems },
        );
      }
      if (node.items !== undefined) {
        value.forEach((item, i) => {
          walk(node.items as Schema, item, [...instancePath, i], `${schemaPath}/items`);
        });
      }
    }

    if (isPlainObject(value)) {
      if (node.required !== undefined) {
        for (const key of node.required) {
          if (!(key in value)) {
            push(
              "required",
              instancePath,
              `${schemaPath}/required`,
              `must have required property '${key}'`,
              { missingProperty: key },
            );
          }
        }
      }
      if (node.properties !== undefined) {
        for (const [key, sub] of Object.entries(node.properties)) {
          if (key in value) {
            walk(sub, value[key], [...instancePath, key], `${schemaPath}/properties/${key}`);
          }
        }
      }
      if (node.additionalProperties === false) {
        const known = new Set(Object.keys(node.properties ?? {}));
        for (const key of Object.keys(value)) {
          if (!known.has(key)) {
            push(
              "additionalProperties",
              [...instancePath, key],
              `${schemaPath}/additionalProperties`,
              `must NOT have additional property '${key}'`,
              { additionalProperty: key },
            );
          }
        }
      }
    }
  };

  walk(schema, data, [], "#");
  return errors;
}
