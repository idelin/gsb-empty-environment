import type { JsonValue, Schema, ValidationError } from "./schema.ts";
import { escapeSegment, isPlainObject } from "./pointer.ts";

/** Thrown for schema-authoring mistakes (as opposed to instance errors). */
export class SchemaError extends Error {}

function jsonTypeOf(value: JsonValue): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

function typeMatches(value: JsonValue, type: string): boolean {
  switch (type) {
    case "null":
      return value === null;
    case "boolean":
      return typeof value === "boolean";
    case "string":
      return typeof value === "string";
    case "number":
      return typeof value === "number";
    case "integer":
      return typeof value === "number" && Number.isInteger(value);
    case "array":
      return Array.isArray(value);
    case "object":
      return isPlainObject(value);
    default:
      throw new SchemaError(`unsupported type: ${JSON.stringify(type)}`);
  }
}

export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((item, i) => deepEqual(item, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const keysA = Object.keys(a);
    const keysB = Object.keys(b);
    return keysA.length === keysB.length && keysA.every((k) => k in b && deepEqual(a[k], b[k]));
  }
  return false;
}

export function validateSchema(
  schema: Schema,
  node: JsonValue,
  instancePath: string,
  schemaPath: string,
  errors: ValidationError[],
): void {
  if (schema.type !== undefined) {
    const types = Array.isArray(schema.type) ? schema.type : [schema.type];
    if (!types.some((t) => typeMatches(node, t))) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/type`,
        message: `expected type ${types.join(" | ")}, got ${jsonTypeOf(node)}`,
      });
      return; // further keyword checks are meaningless on a type mismatch
    }
  }

  if ("const" in schema && !deepEqual(node, schema.const as JsonValue)) {
    errors.push({
      instancePath,
      schemaPath: `${schemaPath}/const`,
      message: `expected constant ${JSON.stringify(schema.const)}`,
    });
  }

  if (schema.enum !== undefined && !schema.enum.some((e) => deepEqual(e, node))) {
    errors.push({
      instancePath,
      schemaPath: `${schemaPath}/enum`,
      message: `value is not one of ${JSON.stringify(schema.enum)}`,
    });
  }

  if (typeof node === "string") {
    const length = Array.from(node).length; // count code points, not UTF-16 units
    if (schema.minLength !== undefined && length < schema.minLength) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/minLength`,
        message: `string length ${length} < minLength ${schema.minLength}`,
      });
    }
    if (schema.maxLength !== undefined && length > schema.maxLength) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/maxLength`,
        message: `string length ${length} > maxLength ${schema.maxLength}`,
      });
    }
    if (schema.pattern !== undefined) {
      let regex: RegExp;
      try {
        regex = new RegExp(schema.pattern);
      } catch {
        throw new SchemaError(`invalid pattern: ${JSON.stringify(schema.pattern)}`);
      }
      if (!regex.test(node)) {
        errors.push({
          instancePath,
          schemaPath: `${schemaPath}/pattern`,
          message: `string does not match pattern ${JSON.stringify(schema.pattern)}`,
        });
      }
    }
  }

  if (typeof node === "number") {
    if (schema.minimum !== undefined && node < schema.minimum) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/minimum`,
        message: `${node} < minimum ${schema.minimum}`,
      });
    }
    if (schema.maximum !== undefined && node > schema.maximum) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/maximum`,
        message: `${node} > maximum ${schema.maximum}`,
      });
    }
    if (schema.exclusiveMinimum !== undefined && node <= schema.exclusiveMinimum) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/exclusiveMinimum`,
        message: `${node} <= exclusiveMinimum ${schema.exclusiveMinimum}`,
      });
    }
    if (schema.exclusiveMaximum !== undefined && node >= schema.exclusiveMaximum) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/exclusiveMaximum`,
        message: `${node} >= exclusiveMaximum ${schema.exclusiveMaximum}`,
      });
    }
  }

  if (Array.isArray(node)) {
    if (schema.minItems !== undefined && node.length < schema.minItems) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/minItems`,
        message: `array length ${node.length} < minItems ${schema.minItems}`,
      });
    }
    if (schema.maxItems !== undefined && node.length > schema.maxItems) {
      errors.push({
        instancePath,
        schemaPath: `${schemaPath}/maxItems`,
        message: `array length ${node.length} > maxItems ${schema.maxItems}`,
      });
    }
    if (schema.items !== undefined) {
      node.forEach((item, index) => {
        validateSchema(schema.items!, item, `${instancePath}/${index}`, `${schemaPath}/items`, errors);
      });
    }
  }

  if (isPlainObject(node)) {
    if (schema.required !== undefined) {
      for (const key of schema.required) {
        if (!(key in node)) {
          errors.push({
            instancePath,
            schemaPath: `${schemaPath}/required`,
            message: `missing required property ${JSON.stringify(key)}`,
          });
        }
      }
    }
    if (schema.properties !== undefined) {
      for (const [key, subschema] of Object.entries(schema.properties)) {
        if (key in node) {
          validateSchema(
            subschema,
            node[key],
            `${instancePath}/${escapeSegment(key)}`,
            `${schemaPath}/properties/${escapeSegment(key)}`,
            errors,
          );
        }
      }
    }
    if (schema.additionalProperties === false && schema.properties !== undefined) {
      for (const key of Object.keys(node)) {
        if (!(key in schema.properties)) {
          errors.push({
            instancePath: `${instancePath}/${escapeSegment(key)}`,
            schemaPath: `${schemaPath}/additionalProperties`,
            message: `unexpected property ${JSON.stringify(key)}`,
          });
        }
      }
    }
  }
}
