/**
 * Hand-rolled validator for a deliberately small JSON-Schema-like subset.
 *
 * Supported (and only supported) keywords:
 *   - type: "object" | "string" | "number" | "integer" | "boolean" | "null"
 *   - properties           (object only)
 *   - required             (object only)
 *   - additionalProperties (object only, only `false` is meaningful)
 *
 * Anything outside this subset is rejected with a SchemaError so the library
 * never silently promises semantics it does not implement.
 */

export type ScalarTypeName = "string" | "number" | "integer" | "boolean" | "null";

export interface ObjectSchema {
  type: "object";
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean;
}

export interface ScalarSchema {
  type: ScalarTypeName;
}

export type Schema = ObjectSchema | ScalarSchema;

export interface ValidationError {
  /** Which schema keyword was violated. */
  keyword: string;
  /** JSON Pointer to the offending value in the data, e.g. "/user/id". */
  instancePath: string;
  /** JSON Pointer to the violated rule inside the schema, e.g. "/properties/user/properties/id/type". */
  rulePath: string;
  message: string;
}

export class SchemaError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SchemaError";
  }
}

const OBJECT_KEYWORDS = new Set(["type", "properties", "required", "additionalProperties"]);
const SCALAR_KEYWORDS = new Set(["type"]);
const SCALAR_TYPES = new Set<string>(["string", "number", "integer", "boolean", "null"]);

export function escapePointerSegment(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

function joinRulePath(base: string, ...segments: string[]): string {
  let out = base;
  for (const segment of segments) out += "/" + escapePointerSegment(segment);
  return out;
}

function assertSupportedSchema(schema: unknown, rulePath: string): void {
  const where = rulePath === "" ? "/" : rulePath;
  if (schema === null || typeof schema !== "object" || Array.isArray(schema)) {
    throw new SchemaError(`schema node at ${where} must be a non-array object`);
  }
  const node = schema as Record<string, unknown>;
  const type = node.type;
  if (type === "object") {
    for (const keyword of Object.keys(node)) {
      if (!OBJECT_KEYWORDS.has(keyword)) {
        throw new SchemaError(
          `unsupported schema keyword "${keyword}" at ${where}; supported: ${[...OBJECT_KEYWORDS].join(", ")}`,
        );
      }
    }
    if (node.required !== undefined) {
      if (!Array.isArray(node.required) || node.required.some((r) => typeof r !== "string")) {
        throw new SchemaError(`"required" at ${where} must be an array of property names`);
      }
    }
    if (node.properties !== undefined) {
      if (node.properties === null || typeof node.properties !== "object" || Array.isArray(node.properties)) {
        throw new SchemaError(`"properties" at ${where} must be an object mapping names to schemas`);
      }
      for (const [name, sub] of Object.entries(node.properties as Record<string, unknown>)) {
        assertSupportedSchema(sub, joinRulePath(rulePath, "properties", name));
      }
    }
    return;
  }
  if (typeof type === "string" && SCALAR_TYPES.has(type)) {
    for (const keyword of Object.keys(node)) {
      if (!SCALAR_KEYWORDS.has(keyword)) {
        throw new SchemaError(
          `unsupported schema keyword "${keyword}" at ${where}; supported: ${[...SCALAR_KEYWORDS].join(", ")}`,
        );
      }
    }
    return;
  }
  throw new SchemaError(
    `unsupported schema type ${JSON.stringify(type)} at ${where}; supported: "object", ${[...SCALAR_TYPES].join(", ")}`,
  );
}

function dataType(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

function validateNode(schema: Schema, data: unknown, instancePath: string, rulePath: string): ValidationError[] {
  const errors: ValidationError[] = [];
  if (schema.type === "object") {
    if (dataType(data) !== "object") {
      errors.push({
        keyword: "type",
        instancePath,
        rulePath: rulePath + "/type",
        message: `expected object, got ${dataType(data)}`,
      });
      return errors;
    }
    const record = data as Record<string, unknown>;
    const properties = schema.properties ?? {};
    if (schema.required !== undefined) {
      for (const name of schema.required) {
        if (!Object.prototype.hasOwnProperty.call(record, name)) {
          errors.push({
            keyword: "required",
            instancePath,
            rulePath: rulePath + "/required",
            message: `missing required property "${name}"`,
          });
        }
      }
    }
    for (const [name, value] of Object.entries(record)) {
      const sub = properties[name];
      if (sub !== undefined) {
        errors.push(
          ...validateNode(
            sub,
            value,
            instancePath + "/" + escapePointerSegment(name),
            joinRulePath(rulePath, "properties", name),
          ),
        );
      } else if (schema.additionalProperties === false) {
        errors.push({
          keyword: "additionalProperties",
          instancePath,
          rulePath: rulePath + "/additionalProperties",
          message: `unexpected additional property "${name}"`,
        });
      }
    }
    return errors;
  }
  const expected = schema.type;
  let ok: boolean;
  switch (expected) {
    case "string":
      ok = typeof data === "string";
      break;
    case "number":
      ok = typeof data === "number" && Number.isFinite(data);
      break;
    case "integer":
      ok = typeof data === "number" && Number.isInteger(data);
      break;
    case "boolean":
      ok = typeof data === "boolean";
      break;
    case "null":
      ok = data === null;
      break;
  }
  if (!ok) {
    const got =
      typeof data === "number" && !Number.isFinite(data) ? String(data) : dataType(data);
    errors.push({
      keyword: "type",
      instancePath,
      rulePath: rulePath + "/type",
      message: `expected ${expected}, got ${got}`,
    });
  }
  return errors;
}

export type Validator = (data: unknown) => ValidationError[];

/** Compiles a reusable validator; the schema subset is checked once up front. */
export function compileValidator(schema: Schema): Validator {
  assertSupportedSchema(schema, "");
  return (data: unknown) => validateNode(schema, data, "", "");
}

/** One-shot convenience wrapper around compileValidator. */
export function validate(schema: Schema, data: unknown): ValidationError[] {
  return compileValidator(schema)(data);
}
