export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

/**
 * A default is either a literal JSON value, or a reference expression
 * `{ $from: "<pointer>" }` that copies the (already resolved) value of
 * another field. The pointer is either absolute from the document root
 * ("/profile/name") or sibling-relative ("./firstName").
 */
export type DefaultExpr = JsonValue | { $from: string };

/**
 * The supported schema subset. Anything not listed here is not promised:
 * no $ref, no allOf/anyOf/oneOf/not, no format, no patternProperties,
 * no schema-valued additionalProperties.
 */
export interface Schema {
  type?: "object" | "array" | "string" | "number" | "integer" | "boolean" | "null" | string[];
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean;
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  enum?: JsonValue[];
  const?: JsonValue;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  exclusiveMaximum?: number;
  default?: DefaultExpr;
}

export interface ValidationError {
  /** JSON pointer into the instance, e.g. "/addresses/0/city" ("" = root). */
  instancePath: string;
  /** JSON-pointer-like path into the schema, e.g. "#/properties/addresses/items/properties/city/type". */
  schemaPath: string;
  message: string;
}

export type Result =
  | { ok: true; data: JsonValue }
  | { ok: false; errors: ValidationError[] };
