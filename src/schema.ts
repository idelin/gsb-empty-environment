/**
 * The supported schema subset. Nothing beyond these keywords is promised:
 *
 * - type: "object" | "array" | "string" | "number" | "integer" | "boolean"
 * - object:  properties, required, rules (cross-field comparisons)
 * - array:   items, minItems, maxItems
 * - string:  minLength, maxLength, pattern
 * - number/integer: minimum, maximum, exclusiveMinimum, exclusiveMaximum
 * - any node: enum
 *
 * Cross-field rules compare two numeric descendants of the enclosing object
 * and only apply when both operands exist and are finite numbers.
 */
export type CrossOp = 'lt' | 'lte' | 'gt' | 'gte' | 'eq';

export interface CrossRule {
  op: CrossOp;
  /** Relative pointer from the enclosing object, e.g. "start" or "a/b". */
  left: string;
  right: string;
}

export interface SchemaNode {
  type?: 'object' | 'array' | 'string' | 'number' | 'integer' | 'boolean';
  properties?: Record<string, SchemaNode>;
  required?: string[];
  rules?: CrossRule[];
  items?: SchemaNode;
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

export function typeOk(type: NonNullable<SchemaNode['type']>, v: unknown): boolean {
  switch (type) {
    case 'string':
      return typeof v === 'string';
    case 'number':
      return typeof v === 'number' && Number.isFinite(v);
    case 'integer':
      return typeof v === 'number' && Number.isInteger(v);
    case 'boolean':
      return typeof v === 'boolean';
    case 'array':
      return Array.isArray(v);
    case 'object':
      return typeof v === 'object' && v !== null && !Array.isArray(v);
  }
}

export function compare(op: CrossOp, a: number, b: number): boolean {
  switch (op) {
    case 'lt':
      return a < b;
    case 'lte':
      return a <= b;
    case 'gt':
      return a > b;
    case 'gte':
      return a >= b;
    case 'eq':
      return a === b;
  }
}

export const OP_LABEL: Record<CrossOp, string> = {
  lt: '<',
  lte: '<=',
  gt: '>',
  gte: '>=',
  eq: '===',
};

export function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  return JSON.stringify(a) === JSON.stringify(b);
}
