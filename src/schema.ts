/**
 * Supported schema subset (a deliberate, documented subset of JSON Schema).
 * Anything outside this subset is NOT validated and NOT promised:
 *
 * - type: "object" | "array" | "string" | "number" | "integer" | "boolean" | "null"
 * - const, enum
 * - number/integer: minimum, maximum
 * - string: minLength, maxLength, pattern
 * - object: properties, required, additionalProperties (boolean only)
 * - array (nested, non-streaming): items, minItems, maxItems, uniqueItems
 *
 * The streaming layer (StreamSchema) adds top-level-only keywords:
 * minItems, maxItems, uniqueItems, uniqueKeys, aggregates.
 */

export type JsonSchema =
  | ObjectSchema
  | ArraySchema
  | StringSchema
  | NumberSchema
  | BooleanSchema
  | NullSchema
  | ConstOnlySchema
  | EnumOnlySchema;

export interface BaseSchema {
  const?: unknown;
  enum?: unknown[];
}

export interface ObjectSchema extends BaseSchema {
  type: "object";
  properties?: Record<string, JsonSchema>;
  required?: string[];
  additionalProperties?: boolean;
}

export interface ArraySchema extends BaseSchema {
  type: "array";
  items?: JsonSchema;
  minItems?: number;
  maxItems?: number;
  uniqueItems?: boolean;
}

export interface StringSchema extends BaseSchema {
  type: "string";
  minLength?: number;
  maxLength?: number;
  pattern?: string;
}

export interface NumberSchema extends BaseSchema {
  type: "number" | "integer";
  minimum?: number;
  maximum?: number;
}

export interface BooleanSchema extends BaseSchema {
  type: "boolean";
}

export interface NullSchema extends BaseSchema {
  type: "null";
}

export interface ConstOnlySchema extends BaseSchema {
  const: unknown;
}

export interface EnumOnlySchema extends BaseSchema {
  enum: unknown[];
}

/** Aggregate rule evaluated incrementally over the streamed items. */
export interface AggregateRule {
  kind: "sum";
  /** Top-level property name of each item to sum. Must be a number. */
  field: string;
  /** Checked only at finish() — a partial stream can never prove it. */
  min?: number;
  /** Checked on every push() — may fail early. */
  max?: number;
}

/** Top-level schema for a streamed array. */
export interface StreamSchema {
  /** Schema applied to every item. */
  items: JsonSchema;
  /** Confirmed only at finish(). */
  minItems?: number;
  /** Checked on every push() — may fail early. */
  maxItems?: number;
  /** Whole-item uniqueness by canonical (key-order independent) value. */
  uniqueItems?: boolean;
  /** Top-level property names whose values must be unique across items. */
  uniqueKeys?: string[];
  aggregates?: AggregateRule[];
}
