import { ValidationError } from './errors.ts';
import { WireType } from './wire.ts';

export type ScalarType =
  | 'int32' | 'int64' | 'uint32' | 'uint64'
  | 'sint32' | 'sint64'
  | 'bool'
  | 'fixed32' | 'sfixed32' | 'fixed64' | 'sfixed64'
  | 'float' | 'double'
  | 'string' | 'bytes';

export type FieldType = ScalarType | 'message';

export interface FieldDescriptor {
  name: string;
  number: number;
  type: FieldType;
  /** Nested schema, required iff type === 'message'. */
  message?: MessageSchema;
  repeated?: boolean;
  /** Pack repeated scalars into a single length-delimited field (proto3 default). */
  packed?: boolean;
}

export interface MessageSchema {
  name: string;
  fields: FieldDescriptor[];
}

export function defineMessage(
  name: string,
  fields: FieldDescriptor[],
): MessageSchema {
  const seen = new Set<number>();
  for (const f of fields) {
    if (!Number.isInteger(f.number) || f.number < 1 || f.number > 0x1fff_ffff) {
      throw new ValidationError(`invalid field number ${f.number}`, [name, f.name]);
    }
    if (seen.has(f.number)) {
      throw new ValidationError(`duplicate field number ${f.number}`, [name, f.name]);
    }
    seen.add(f.number);
    if (f.type === 'message' && !f.message) {
      throw new ValidationError('message field requires a nested schema', [name, f.name]);
    }
    if (f.packed && (f.type === 'message' || f.type === 'string' || f.type === 'bytes')) {
      throw new ValidationError(`type ${f.type} cannot be packed`, [name, f.name]);
    }
  }
  return { name, fields };
}

/** The wire type a field uses on the wire (before packed/repeated adjustment). */
export function wireTypeOf(field: FieldDescriptor): WireType {
  switch (field.type) {
    case 'int32': case 'int64': case 'uint32': case 'uint64':
    case 'sint32': case 'sint64': case 'bool':
      return WireType.Varint;
    case 'fixed64': case 'sfixed64': case 'double':
      return WireType.Fixed64;
    case 'fixed32': case 'sfixed32': case 'float':
      return WireType.Fixed32;
    case 'string': case 'bytes': case 'message':
      return WireType.LengthDelimited;
  }
}

/** True if the scalar type can appear inside a packed repeated field. */
export function isPackable(type: FieldType): boolean {
  return type !== 'string' && type !== 'bytes' && type !== 'message';
}

export type MessageValue = Record<string, unknown>;

const INT32_MIN = -(2 ** 31), INT32_MAX = 2 ** 31 - 1;
const UINT32_MAX = 2 ** 32 - 1;
const INT64_MIN = -(1n << 63n), INT64_MAX = (1n << 63n) - 1n;
const UINT64_MAX = (1n << 64n) - 1n;

function checkInt(path: string[], v: unknown, min: number, max: number): void {
  if (typeof v !== 'number' || !Number.isInteger(v) || v < min || v > max) {
    throw new ValidationError(`expected integer in [${min}, ${max}], got ${String(v)}`, path);
  }
}

function checkBig(path: string[], v: unknown, min: bigint, max: bigint): void {
  const n = typeof v === 'number' ? BigInt(v) : typeof v === 'bigint' ? v : null;
  if (n === null || n < min || n > max) {
    throw new ValidationError(`expected int64-range value, got ${String(v)}`, path);
  }
}

function validateScalar(type: ScalarType, v: unknown, path: string[]): void {
  switch (type) {
    case 'int32': case 'sint32': case 'sfixed32':
      return checkInt(path, v, INT32_MIN, INT32_MAX);
    case 'uint32': case 'fixed32':
      return checkInt(path, v, 0, UINT32_MAX);
    case 'int64': case 'sint64': case 'sfixed64':
      return checkBig(path, v, INT64_MIN, INT64_MAX);
    case 'uint64': case 'fixed64':
      return checkBig(path, v, 0n, UINT64_MAX);
    case 'bool':
      if (typeof v !== 'boolean') throw new ValidationError(`expected boolean, got ${typeof v}`, path);
      return;
    case 'float': case 'double':
      if (typeof v !== 'number') throw new ValidationError(`expected number, got ${typeof v}`, path);
      return;
    case 'string':
      if (typeof v !== 'string') throw new ValidationError(`expected string, got ${typeof v}`, path);
      return;
    case 'bytes':
      if (!(v instanceof Uint8Array)) throw new ValidationError('expected Uint8Array', path);
      return;
  }
}

function validateField(field: FieldDescriptor, v: unknown, path: string[]): void {
  const p = [...path, field.name];
  if (field.repeated) {
    if (!Array.isArray(v)) throw new ValidationError('expected array for repeated field', p);
    for (let i = 0; i < v.length; i++) {
      validateSingle(field, v[i], [...p, String(i)]);
    }
    return;
  }
  validateSingle(field, v, p);
}

function validateSingle(field: FieldDescriptor, v: unknown, path: string[]): void {
  if (field.type === 'message') {
    if (typeof v !== 'object' || v === null || Array.isArray(v)) {
      throw new ValidationError('expected nested message object', path);
    }
    validateMessage(field.message!, v as MessageValue, path);
    return;
  }
  validateScalar(field.type, v, path);
}

/**
 * Reflection-mode validation engine: recursively checks a plain-object value
 * against a MessageSchema. Unknown keys are rejected; every violation carries
 * the full field path.
 */
export function validateMessage(
  schema: MessageSchema,
  value: MessageValue,
  path: string[] = [schema.name],
): void {
  const byName = new Map(schema.fields.map((f) => [f.name, f]));
  for (const key of Object.keys(value)) {
    if (!byName.has(key)) {
      throw new ValidationError(`unknown field "${key}"`, [...path, key]);
    }
  }
  for (const field of schema.fields) {
    const v = value[field.name];
    if (v === undefined) continue; // absent fields are allowed (proto3 semantics)
    validateField(field, v, path);
  }
}
