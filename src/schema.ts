/**
 * The supported schema subset. Only these keywords are implemented,
 * promised, and tested. Anything else is ignored by the validator and
 * unsupported by the generator.
 */
export interface Schema {
  type?: "object" | "array" | "string" | "number" | "integer" | "boolean" | "null";
  const?: unknown;
  enum?: unknown[];
  // numbers / integers
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  exclusiveMaximum?: number;
  multipleOf?: number;
  // strings
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  // arrays
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  // objects
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean;
}
