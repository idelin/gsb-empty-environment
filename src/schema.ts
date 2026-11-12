import { BridgeError } from './errors.ts';

export type JsonScalar = string | number | boolean | null;

export type Schema =
  | { kind: 'string' }
  | { kind: 'number' }
  | { kind: 'boolean' }
  | { kind: 'null' }
  | { kind: 'unknown' }
  | { kind: 'literal'; value: JsonScalar }
  | { kind: 'array'; item: Schema }
  | {
      kind: 'object';
      fields: Record<string, FieldSchema>;
      additional?: Schema | false;
    }
  | { kind: 'union'; options: [Schema, Schema, ...Schema[]] };

export interface FieldSchema {
  readonly schema: Schema;
  readonly optional?: boolean;
}

export const BLOCKED_KEYS: ReadonlySet<string> = new Set([
  '__proto__',
  'constructor',
  'prototype',
]);

function typeName(value: unknown): string {
  if (value === null) {
    return 'null';
  }
  if (Array.isArray(value)) {
    return 'array';
  }
  return typeof value;
}

function mismatch(path: string, expected: string, value: unknown): never {
  throw new BridgeError(
    'SCHEMA_MISMATCH',
    `${path}: expected ${expected}, got ${typeName(value)}`,
  );
}

function validateObject(
  value: unknown,
  schema: Extract<Schema, { kind: 'object' }>,
  path: string,
): void {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    mismatch(path, 'object', value);
  }

  const record = value as Record<string, unknown>;
  const descriptors = Object.getOwnPropertyDescriptors(value);

  for (const key of Reflect.ownKeys(value)) {
    if (typeof key === 'symbol') {
      throw new BridgeError(
        'SYMBOL_KEY',
        `${path}: symbol-keyed properties are not allowed`,
      );
    }
    if (BLOCKED_KEYS.has(key)) {
      throw new BridgeError(
        'FORBIDDEN_KEY',
        `${path}.${key}: prototype pollution keys are not allowed`,
      );
    }
    const descriptor = descriptors[key];
    if (descriptor && ('get' in descriptor || 'set' in descriptor)) {
      throw new BridgeError(
        'ACCESSOR_PROPERTY',
        `${path}.${key}: getter/setter properties are not allowed`,
      );
    }
  }

  const allowedFields = new Set(Object.keys(schema.fields));

  for (const key of Object.keys(record)) {
    if (!allowedFields.has(key)) {
      if (schema.additional === undefined || schema.additional === false) {
        throw new BridgeError(
          'SCHEMA_MISMATCH',
          `${path}.${key}: unknown field is not allowed`,
        );
      }
      validate(record[key], schema.additional, `${path}.${key}`);
    }
  }

  for (const [key, field] of Object.entries(schema.fields)) {
    if (!Object.prototype.hasOwnProperty.call(record, key)) {
      if (!field.optional) {
        throw new BridgeError(
          'SCHEMA_MISMATCH',
          `${path}.${key}: required field is missing`,
        );
      }
      continue;
    }
    validate(record[key], field.schema, `${path}.${key}`);
  }
}

export function validate(value: unknown, schema: Schema, path = '$'): void {
  switch (schema.kind) {
    case 'unknown':
      return;
    case 'string':
      if (typeof value !== 'string') {
        mismatch(path, 'string', value);
      }
      return;
    case 'number':
      if (typeof value !== 'number' || Number.isNaN(value)) {
        mismatch(path, 'number', value);
      }
      return;
    case 'boolean':
      if (typeof value !== 'boolean') {
        mismatch(path, 'boolean', value);
      }
      return;
    case 'null':
      if (value !== null) {
        mismatch(path, 'null', value);
      }
      return;
    case 'literal':
      if (value !== schema.value) {
        mismatch(path, JSON.stringify(schema.value), value);
      }
      return;
    case 'array':
      if (!Array.isArray(value)) {
        mismatch(path, 'array', value);
      }
      value.forEach((item, index) => {
        validate(item, schema.item, `${path}[${index}]`);
      });
      return;
    case 'union': {
      let lastError: unknown = null;
      for (const option of schema.options) {
        try {
          validate(value, option, path);
          return;
        } catch (error) {
          lastError = error;
        }
      }
      const detail =
        lastError instanceof Error ? ` (${lastError.message})` : '';
      throw new BridgeError(
        'SCHEMA_MISMATCH',
        `${path}: no union variant matched${detail}`,
      );
    }
    case 'object':
      validateObject(value, schema, path);
      return;
  }
}

export const s = {
  string: (): Schema => ({ kind: 'string' }),
  number: (): Schema => ({ kind: 'number' }),
  boolean: (): Schema => ({ kind: 'boolean' }),
  null: (): Schema => ({ kind: 'null' }),
  unknown: (): Schema => ({ kind: 'unknown' }),
  literal: (value: JsonScalar): Schema => ({ kind: 'literal', value }),
  array: (item: Schema): Schema => ({ kind: 'array', item }),
  object: (
    fields: Record<string, FieldSchema>,
    additional: Schema | false = false,
  ): Schema => ({ kind: 'object', fields, additional }),
  union: (
    ...options: [Schema, Schema, ...Schema[]]
  ): Schema => ({ kind: 'union', options }),
  field: (schema: Schema): FieldSchema => ({ schema }),
  optional: (schema: Schema): FieldSchema => ({ schema, optional: true }),
};
