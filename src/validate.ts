import { ValidationError } from './wire/errors.ts';
import type { FieldDescriptor, MessageDescriptor } from './schema.ts';

export interface ValidationIssue {
  path: string;
  message: string;
}

export interface ValidateOptions {
  maxDepth?: number;
  allowUnknownKeys?: boolean;
}

const DEFAULT_MAX_DEPTH = 64;

function isIntegerLike(value: unknown): boolean {
  return (typeof value === 'number' && Number.isInteger(value)) || typeof value === 'bigint';
}

function toBigIntChecked(value: number | bigint): bigint {
  return typeof value === 'bigint' ? value : BigInt(value);
}

function checkIntegerRange(
  value: unknown,
  min: bigint,
  max: bigint,
  path: string,
  typeName: string,
  issues: ValidationIssue[],
): void {
  if (!isIntegerLike(value)) {
    issues.push({ path, message: `expected integer for ${typeName}, got ${typeof value}` });
    return;
  }
  const v = toBigIntChecked(value as number | bigint);
  if (v < min || v > max) {
    issues.push({ path, message: `value ${v} out of range for ${typeName} [${min}, ${max}]` });
  }
}

function validateScalar(
  field: FieldDescriptor,
  value: unknown,
  path: string,
  issues: ValidationIssue[],
): void {
  switch (field.type) {
    case 'uint32':
      checkIntegerRange(value, 0n, 0xffffffffn, path, 'uint32', issues);
      return;
    case 'uint64':
      checkIntegerRange(value, 0n, 0xffffffffffffffffn, path, 'uint64', issues);
      return;
    case 'int32':
      checkIntegerRange(value, -0x80000000n, 0x7fffffffn, path, 'int32', issues);
      return;
    case 'int64':
      checkIntegerRange(value, -(1n << 63n), (1n << 63n) - 1n, path, 'int64', issues);
      return;
    case 'sint32':
      checkIntegerRange(value, -0x80000000n, 0x7fffffffn, path, 'sint32', issues);
      return;
    case 'sint64':
      checkIntegerRange(value, -(1n << 63n), (1n << 63n) - 1n, path, 'sint64', issues);
      return;
    case 'fixed32':
      checkIntegerRange(value, 0n, 0xffffffffn, path, 'fixed32', issues);
      return;
    case 'fixed64':
      checkIntegerRange(value, 0n, 0xffffffffffffffffn, path, 'fixed64', issues);
      return;
    case 'sfixed32':
      checkIntegerRange(value, -0x80000000n, 0x7fffffffn, path, 'sfixed32', issues);
      return;
    case 'sfixed64':
      checkIntegerRange(value, -(1n << 63n), (1n << 63n) - 1n, path, 'sfixed64', issues);
      return;
    case 'bool':
      if (typeof value !== 'boolean') {
        issues.push({ path, message: `expected boolean, got ${typeof value}` });
      }
      return;
    case 'float':
    case 'double':
      if (typeof value !== 'number') {
        issues.push({ path, message: `expected number, got ${typeof value}` });
      }
      return;
    case 'string':
      if (typeof value !== 'string') {
        issues.push({ path, message: `expected string, got ${typeof value}` });
      }
      return;
    case 'bytes':
      if (!(value instanceof Uint8Array)) {
        issues.push({ path, message: 'expected Uint8Array' });
      }
      return;
    default:
      issues.push({ path, message: `unknown field type ${field.type}` });
  }
}

function validateMessageValue(
  desc: MessageDescriptor,
  value: unknown,
  path: string,
  opts: Required<ValidateOptions>,
  depth: number,
  issues: ValidationIssue[],
): void {
  if (depth > opts.maxDepth) {
    issues.push({ path, message: `nesting depth exceeds limit ${opts.maxDepth}` });
    return;
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    issues.push({ path, message: `expected object for message ${desc.name}` });
    return;
  }
  const record = value as Record<string, unknown>;
  if (!opts.allowUnknownKeys) {
    for (const key of Object.keys(record)) {
      if (!desc.byName.has(key)) {
        issues.push({ path: path ? `${path}.${key}` : key, message: `unknown field ${key}` });
      }
    }
  }
  for (const field of desc.fields) {
    const fieldPath = path ? `${path}.${field.name}` : field.name;
    const fieldValue = record[field.name];
    if (fieldValue === undefined || fieldValue === null) {
      if (field.required) {
        issues.push({ path: fieldPath, message: 'required field is missing' });
      }
      continue;
    }
    if (field.repeated) {
      if (!Array.isArray(fieldValue)) {
        issues.push({ path: fieldPath, message: 'expected array for repeated field' });
        continue;
      }
      fieldValue.forEach((item, index) =>
        validateSingle(field, item, `${fieldPath}[${index}]`, opts, depth, issues),
      );
    } else {
      validateSingle(field, fieldValue, fieldPath, opts, depth, issues);
    }
  }
}

function validateSingle(
  field: FieldDescriptor,
  value: unknown,
  path: string,
  opts: Required<ValidateOptions>,
  depth: number,
  issues: ValidationIssue[],
): void {
  if (field.type === 'message') {
    validateMessageValue(field.message as MessageDescriptor, value, path, opts, depth + 1, issues);
  } else {
    validateScalar(field, value, path, issues);
  }
}

export function validateValue(
  desc: MessageDescriptor,
  value: unknown,
  opts: ValidateOptions = {},
): ValidationIssue[] {
  const resolved: Required<ValidateOptions> = {
    maxDepth: opts.maxDepth ?? DEFAULT_MAX_DEPTH,
    allowUnknownKeys: opts.allowUnknownKeys ?? false,
  };
  const issues: ValidationIssue[] = [];
  validateMessageValue(desc, value, '', resolved, 0, issues);
  return issues;
}

export function assertValid(
  desc: MessageDescriptor,
  value: unknown,
  opts: ValidateOptions = {},
): void {
  const issues = validateValue(desc, value, opts);
  if (issues.length > 0) {
    throw new ValidationError(issues);
  }
}
