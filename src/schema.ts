/**
 * The implemented schema subset. Anything outside this list is NOT promised:
 *
 * - type: "object" | "array" | "string" | "number" | "integer" | "boolean" | "null"
 * - object:  properties, required, additionalProperties (boolean only)
 * - array:   items (single schema), minItems, maxItems
 * - string:  minLength, maxLength, pattern, enum
 * - number/integer: minimum, maximum, exclusiveMinimum, exclusiveMaximum
 * - any:     enum
 *
 * Explicitly NOT supported (rejected at registration time): $ref, oneOf/anyOf/allOf,
 * patternProperties, format, dependencies, if/then/else, contains, tuple items.
 */
export type SchemaType =
  | "object"
  | "array"
  | "string"
  | "number"
  | "integer"
  | "boolean"
  | "null";

export interface Schema {
  type?: SchemaType;
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean;
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  exclusiveMaximum?: number;
  enum?: unknown[];
}

const SUPPORTED_KEYS = new Set([
  "type",
  "properties",
  "required",
  "additionalProperties",
  "items",
  "minItems",
  "maxItems",
  "minLength",
  "maxLength",
  "pattern",
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "enum",
]);

const TYPES: ReadonlySet<string> = new Set([
  "object",
  "array",
  "string",
  "number",
  "integer",
  "boolean",
  "null",
]);

/** Reject schemas that use keywords outside the implemented subset. */
export function assertSupportedSchema(schema: Schema, path = "#"): void {
  if (schema === null || typeof schema !== "object" || Array.isArray(schema)) {
    throw new Error(`schema at ${path} must be a plain object`);
  }
  for (const key of Object.keys(schema)) {
    if (!SUPPORTED_KEYS.has(key)) {
      throw new Error(
        `unsupported schema keyword "${key}" at ${path} (outside the implemented subset)`,
      );
    }
  }
  if (schema.type !== undefined && !TYPES.has(schema.type)) {
    throw new Error(`unknown type "${schema.type}" at ${path}`);
  }
  if (schema.properties !== undefined) {
    for (const [name, sub] of Object.entries(schema.properties)) {
      assertSupportedSchema(sub, `${path}/properties/${escapePointer(name)}`);
    }
  }
  if (schema.items !== undefined) {
    assertSupportedSchema(schema.items, `${path}/items`);
  }
}

export function escapePointer(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}
