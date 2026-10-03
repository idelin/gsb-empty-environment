/**
 * Reflection-mode schema descriptors and validation engine.
 * Schemas describe message shape at runtime; validateMessage checks a decoded
 * (or to-be-encoded) struct against the descriptor, collecting every issue.
 */
import { ValidationError, type ValidationIssue } from './errors.ts';
import { WireType } from './wire.ts';

export type ScalarType =
  | 'int32'
  | 'int64'
  | 'uint32'
  | 'uint64'
  | 'sint32'
  | 'sint64'
  | 'bool'
  | 'fixed32'
  | 'fixed64'
  | 'sfixed32'
  | 'sfixed64'
  | 'string'
  | 'bytes';

export type FieldType = ScalarType | 'message';

export interface FieldDescriptor {
  name: string;
  number: number;
  type: FieldType;
  repeated?: boolean;
  /** packed encoding (only legal for repeated numeric scalars). */
  packed?: boolean;
  required?: boolean;
  /** nested schema, required when type === 'message'. */
  message?: MessageSchema;
}

export interface MessageSchema {
  name: string;
  fields: FieldDescriptor[];
}

export const NUMERIC_SCALAR_TYPES: ReadonlySet<ScalarType> = new Set([
  'int32',
  'int64',
  'uint32',
  'uint64',
  'sint32',
  'sint64',
  'bool',
  'fixed32',
  'fixed64',
  'sfixed32',
  'sfixed64',
]);

const INT32_MIN = -(2 ** 31);
const INT32_MAX = 2 ** 31 - 1;
const UINT32_MAX = 2 ** 32 - 1;
const INT64_MIN = -(1n << 63n);
const INT64_MAX = (1n << 63n) - 1n;
const UINT64_MAX = (1n << 64n) - 1n;

/** Wire type a field uses on the wire (packed repeated handled by caller). */
export function wireTypeOf(field: FieldDescriptor): WireType {
  switch (field.type) {
    case 'int32':
    case 'int64':
    case 'uint32':
    case 'uint64':
    case 'sint32':
    case 'sint64':
    case 'bool':
      return WireType.Varint;
    case 'fixed64':
    case 'sfixed64':
      return WireType.Fixed64;
    case 'fixed32':
    case 'sfixed32':
      return WireType.Fixed32;
    case 'string':
    case 'bytes':
    case 'message':
      return WireType.LengthDelimited;
  }
}

/** Validate a schema descriptor itself (reflection over the reflection). */
export function validateSchema(schema: MessageSchema): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  const seenNumbers = new Set<number>();
  const seenNames = new Set<string>();
  for (const field of schema.fields) {
    const path = `${schema.name}.${field.name}`;
    if (!Number.isInteger(field.number) || field.number < 1 || field.number > 0x1fffffff) {
      issues.push({ path, message: `illegal field number ${field.number}` });
    }
    if (seenNumbers.has(field.number)) {
      issues.push({ path, message: `duplicate field number ${field.number}` });
    }
    if (seenNames.has(field.name)) {
      issues.push({ path, message: `duplicate field name "${field.name}"` });
    }
    seenNumbers.add(field.number);
    seenNames.add(field.name);
    if (field.packed && !(field.repeated && NUMERIC_SCALAR_TYPES.has(field.type as ScalarType))) {
      issues.push({ path, message: 'packed is only legal on repeated numeric scalar fields' });
    }
    if (field.type === 'message') {
      if (!field.message) {
        issues.push({ path, message: 'message-typed field requires a nested schema' });
      } else {
        issues.push(...validateSchema(field.message));
      }
    }
  }
  return issues;
}

function checkScalar(
  type: ScalarType,
  value: unknown,
  path: string,
  issues: ValidationIssue[],
): void {
  const fail = (message: string) => issues.push({ path, message });
  switch (type) {
    case 'bool':
      if (typeof value !== 'boolean') fail(`expected boolean, got ${typeof value}`);
      return;
    case 'string':
      if (typeof value !== 'string') fail(`expected string, got ${typeof value}`);
      return;
    case 'bytes':
      if (!(value instanceof Uint8Array)) fail('expected Uint8Array');
      return;
    case 'int32':
    case 'sint32':
    case 'sfixed32':
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        fail('expected integer number');
      } else if (value < INT32_MIN || value > INT32_MAX) {
        fail(`value ${value} out of int32 range`);
      }
      return;
    case 'uint32':
    case 'fixed32':
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        fail('expected integer number');
      } else if (value < 0 || value > UINT32_MAX) {
        fail(`value ${value} out of uint32 range`);
      }
      return;
    case 'int64':
    case 'sint64':
    case 'sfixed64':
      if (typeof value !== 'bigint') {
        fail('expected bigint');
      } else if (value < INT64_MIN || value > INT64_MAX) {
        fail(`value ${value} out of int64 range`);
      }
      return;
    case 'uint64':
    case 'fixed64':
      if (typeof value !== 'bigint') {
        fail('expected bigint');
      } else if (value < 0n || value > UINT64_MAX) {
        fail(`value ${value} out of uint64 range`);
      }
      return;
  }
}

/** Recursively validate a struct against its schema; returns all issues found. */
export function validateMessage(
  schema: MessageSchema,
  value: unknown,
  path = schema.name,
): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return [{ path, message: 'expected a message object' }];
  }
  const record = value as Record<string, unknown>;
  const known = new Set(schema.fields.map((f) => f.name));
  for (const key of Object.keys(record)) {
    if (!known.has(key)) {
      issues.push({ path: `${path}.${key}`, message: 'unknown field not present in schema' });
    }
  }
  for (const field of schema.fields) {
    const fieldPath = `${path}.${field.name}`;
    const fieldValue = record[field.name];
    if (fieldValue === undefined || fieldValue === null) {
      if (field.required) {
        issues.push({ path: fieldPath, message: 'required field is missing' });
      }
      continue;
    }
    if (field.repeated) {
      if (!Array.isArray(fieldValue)) {
        issues.push({ path: fieldPath, message: 'expected an array for repeated field' });
        continue;
      }
      fieldValue.forEach((item, index) =>
        checkFieldValue(field, item, `${fieldPath}[${index}]`, issues),
      );
    } else {
      if (Array.isArray(fieldValue)) {
        issues.push({ path: fieldPath, message: 'singular field must not be an array' });
        continue;
      }
      checkFieldValue(field, fieldValue, fieldPath, issues);
    }
  }
  return issues;
}

function checkFieldValue(
  field: FieldDescriptor,
  value: unknown,
  path: string,
  issues: ValidationIssue[],
): void {
  if (field.type === 'message') {
    if (!field.message) {
      issues.push({ path, message: 'field schema is missing nested message descriptor' });
      return;
    }
    issues.push(...validateMessage(field.message, value, path));
  } else {
    checkScalar(field.type as ScalarType, value, path, issues);
  }
}

/** Throw a ValidationError if the struct does not conform to the schema. */
export function assertValid(schema: MessageSchema, value: unknown): void {
  const issues = [...validateSchema(schema), ...validateMessage(schema, value)];
  if (issues.length > 0) {
    throw new ValidationError(issues);
  }
}
