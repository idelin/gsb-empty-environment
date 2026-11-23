import { ValidationError } from './errors.js';

export type PrimitiveType = 'string' | 'number' | 'integer' | 'boolean' | 'bigint' | 'null';

export interface PrimitiveSchema {
  type: PrimitiveType;
  optional?: boolean;
  enum?: readonly (string | number | bigint | boolean | null)[];
}

export interface ArraySchema {
  type: 'array';
  optional?: boolean;
  items: Schema;
  minItems?: number;
  maxItems?: number;
}

export interface ObjectSchema {
  type: 'object';
  optional?: boolean;
  properties: Record<string, Schema>;
  additionalProperties?: boolean | Schema;
}

export interface UnionSchema {
  type: 'union';
  optional?: boolean;
  variants: Schema[];
}

export interface AnySchema {
  type: 'any';
  optional?: boolean;
}

export type Schema =
  | PrimitiveSchema
  | ArraySchema
  | ObjectSchema
  | UnionSchema
  | AnySchema;

type PlainRecord = Record<string, unknown>;

function typeLabel(value: unknown): string {
  if (value === null) {
    return 'null';
  }
  if (Array.isArray(value)) {
    return 'array';
  }
  return typeof value;
}

function checkPrimitive(value: unknown, schema: PrimitiveSchema, path: string): void {
  const actual = typeLabel(value);
  const expected = schema.type;
  let ok = false;

  if (expected === 'null') {
    ok = value === null;
  } else if (expected === 'integer') {
    ok = typeof value === 'number' && Number.isInteger(value);
  } else {
    ok = typeof value === expected;
  }

  if (!ok) {
    throw new ValidationError(path, `expected ${expected}, got ${actual}`);
  }

  if (schema.enum !== undefined && !schema.enum.includes(value as never)) {
    throw new ValidationError(path, 'value is not in the allowed enum');
  }
}

function checkObject(value: unknown, schema: ObjectSchema, path: string): void {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new ValidationError(path, `expected object, got ${typeLabel(value)}`);
  }

  const record = value as PlainRecord;
  const known = new Set(Object.keys(schema.properties));

  for (const [key, child] of Object.entries(schema.properties)) {
    const childPath = path === '$' ? `$.${key}` : `${path}.${key}`;
    if (!(key in record)) {
      if (!child.optional) {
        throw new ValidationError(childPath, 'missing required property');
      }
      continue;
    }
    validate(record[key], child, childPath);
  }

  for (const key of Object.keys(record)) {
    if (known.has(key)) {
      continue;
    }
    const childPath = path === '$' ? `$.${key}` : `${path}.${key}`;
    if (schema.additionalProperties === undefined || schema.additionalProperties === false) {
      throw new ValidationError(childPath, 'unknown property is not allowed');
    }
    if (schema.additionalProperties !== true) {
      validate(record[key], schema.additionalProperties, childPath);
    }
  }
}

function checkArray(value: unknown, schema: ArraySchema, path: string): void {
  if (!Array.isArray(value)) {
    throw new ValidationError(path, `expected array, got ${typeLabel(value)}`);
  }
  if (schema.minItems !== undefined && value.length < schema.minItems) {
    throw new ValidationError(path, `expected at least ${schema.minItems} items, got ${value.length}`);
  }
  if (schema.maxItems !== undefined && value.length > schema.maxItems) {
    throw new ValidationError(path, `expected at most ${schema.maxItems} items, got ${value.length}`);
  }
  value.forEach((item, index) => {
    validate(item, schema.items, `${path}[${index}]`);
  });
}

function checkUnion(value: unknown, schema: UnionSchema, path: string): void {
  const failures: string[] = [];
  for (const variant of schema.variants) {
    try {
      validate(value, variant, path);
      return;
    } catch (error) {
      if (error instanceof ValidationError) {
        failures.push(error.message);
      } else {
        throw error;
      }
    }
  }
  throw new ValidationError(path, `no union variant matched; attempts: ${failures.join(' | ')}`);
}

export function validate(value: unknown, schema: Schema, path = '$'): void {
  if (value === undefined) {
    if (schema.optional) {
      return;
    }
    throw new ValidationError(path, 'expected a value, got undefined');
  }

  switch (schema.type) {
    case 'any':
      return;
    case 'union':
      checkUnion(value, schema, path);
      return;
    case 'array':
      checkArray(value, schema, path);
      return;
    case 'object':
      checkObject(value, schema, path);
      return;
    default:
      checkPrimitive(value, schema, path);
      return;
  }
}
