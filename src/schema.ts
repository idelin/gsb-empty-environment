/**
 * Reflection-mode schema engine: declarative field descriptors drive
 * decoding, encoding and structural validation of Protobuf messages.
 * Supports nested length-delimited messages and packed repeated fields.
 */

import { WireReader, WireType, WireWriter, type WireTypeValue } from './wire.ts';
import { toSigned32, toSigned64, zigzagEncode } from './varint.ts';

export const FieldType = {
  Int32: 'int32',
  Int64: 'int64',
  Uint32: 'uint32',
  Uint64: 'uint64',
  Sint32: 'sint32',
  Sint64: 'sint64',
  Bool: 'bool',
  Fixed32: 'fixed32',
  Fixed64: 'fixed64',
  Sfixed32: 'sfixed32',
  Sfixed64: 'sfixed64',
  String: 'string',
  Bytes: 'bytes',
  Message: 'message',
} as const;
export type FieldTypeName = (typeof FieldType)[keyof typeof FieldType];

export interface FieldDescriptor {
  fieldNumber: number;
  name: string;
  type: FieldTypeName;
  repeated?: boolean;
  /** packed encoding for repeated scalar numerics (proto3 default). */
  packed?: boolean;
  required?: boolean;
  /** schema of the nested message when type === 'message'. */
  messageType?: MessageSchema;
}

export interface MessageSchema {
  name: string;
  fields: FieldDescriptor[];
}

export class SchemaError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SchemaError';
  }
}

export class ValidationError extends Error {
  readonly violations: string[];
  constructor(violations: string[]) {
    super(`validation failed:\n  - ${violations.join('\n  - ')}`);
    this.name = 'ValidationError';
    this.violations = violations;
  }
}

export function wireTypeOf(field: FieldDescriptor): WireTypeValue {
  switch (field.type) {
    case FieldType.Fixed32:
    case FieldType.Sfixed32:
      return WireType.Fixed32;
    case FieldType.Fixed64:
    case FieldType.Sfixed64:
      return WireType.Fixed64;
    case FieldType.String:
    case FieldType.Bytes:
    case FieldType.Message:
      return WireType.LengthDelimited;
    default:
      return WireType.Varint;
  }
}

function isPackable(field: FieldDescriptor): boolean {
  return (
    field.type !== FieldType.String &&
    field.type !== FieldType.Bytes &&
    field.type !== FieldType.Message
  );
}

function readScalar(reader: WireReader, field: FieldDescriptor): unknown {
  switch (field.type) {
    case FieldType.Int32:
      return toSigned32(Number(reader.readVarint() & 0xffffffffn));
    case FieldType.Int64:
      return toSigned64(reader.readVarint());
    case FieldType.Uint32:
      return Number(reader.readVarint() & 0xffffffffn);
    case FieldType.Uint64:
      return reader.readVarint();
    case FieldType.Sint32:
      return toSigned32(Number(reader.readSVarint() & 0xffffffffn));
    case FieldType.Sint64:
      return reader.readSVarint();
    case FieldType.Bool:
      return reader.readVarint() !== 0n;
    case FieldType.Fixed32:
      return reader.readFixed32();
    case FieldType.Sfixed32:
      return toSigned32(reader.readFixed32());
    case FieldType.Fixed64:
      return reader.readFixed64();
    case FieldType.Sfixed64:
      return toSigned64(reader.readFixed64());
    case FieldType.String:
      return reader.readString();
    case FieldType.Bytes:
      return reader.readBytes();
    case FieldType.Message: {
      if (!field.messageType) {
        throw new SchemaError(`field "${field.name}" is a message without messageType`);
      }
      const sub = reader.readBytes();
      return decodeMessage(field.messageType, sub);
    }
    default:
      throw new SchemaError(`unknown field type: ${field.type}`);
  }
}

/** Decode a packed repeated payload (a length-delimited run of scalars). */
function readPacked(reader: WireReader, field: FieldDescriptor): unknown[] {
  const payload = reader.readBytes();
  const sub = new WireReader(payload);
  const out: unknown[] = [];
  while (!sub.done) {
    out.push(readScalar(sub, { ...field, repeated: false, packed: false }));
  }
  return out;
}

/** Reflection decode: walk the wire stream and materialize a plain object. */
export function decodeMessage(
  schema: MessageSchema,
  buf: Uint8Array,
): Record<string, unknown> {
  const byNumber = new Map<number, FieldDescriptor>();
  for (const f of schema.fields) {
    if (byNumber.has(f.fieldNumber)) {
      throw new SchemaError(`duplicate field number ${f.fieldNumber} in ${schema.name}`);
    }
    byNumber.set(f.fieldNumber, f);
  }

  const out: Record<string, unknown> = {};
  const reader = new WireReader(buf);
  while (!reader.done) {
    const { fieldNumber, wireType } = reader.readTag();
    const field = byNumber.get(fieldNumber);
    if (!field) {
      reader.skip(wireType); // unknown field: skip safely
      continue;
    }
    const expected = wireTypeOf(field);
    if (field.repeated && field.packed && isPackable(field) && wireType === WireType.LengthDelimited) {
      const values = readPacked(reader, field);
      const arr = (out[field.name] as unknown[] | undefined) ?? [];
      arr.push(...values);
      out[field.name] = arr;
      continue;
    }
    if (wireType !== expected) {
      throw new SchemaError(
        `${schema.name}.${field.name}: wire type ${wireType} does not match expected ${expected}`,
      );
    }
    const value = readScalar(reader, field);
    if (field.repeated) {
      const arr = (out[field.name] as unknown[] | undefined) ?? [];
      arr.push(value);
      out[field.name] = arr;
    } else {
      out[field.name] = value;
    }
  }
  return out;
}

function writeScalar(writer: WireWriter, field: FieldDescriptor, value: unknown): void {
  switch (field.type) {
    case FieldType.Int32:
    case FieldType.Int64:
    case FieldType.Uint32:
    case FieldType.Uint64:
      writer.writeVarint(value as number | bigint);
      return;
    case FieldType.Sint32:
    case FieldType.Sint64:
      writer.writeVarint(zigzagEncode(value as number | bigint));
      return;
    case FieldType.Bool:
      writer.writeVarint(value ? 1 : 0);
      return;
    case FieldType.Fixed32:
    case FieldType.Sfixed32:
      writer.writeFixed32(value as number);
      return;
    case FieldType.Fixed64:
    case FieldType.Sfixed64:
      writer.writeFixed64(value as number | bigint);
      return;
    case FieldType.String:
      writer.writeString(value as string);
      return;
    case FieldType.Bytes:
      writer.writeBytes(value as Uint8Array);
      return;
    case FieldType.Message: {
      if (!field.messageType) {
        throw new SchemaError(`field "${field.name}" is a message without messageType`);
      }
      writer.writeBytes(encodeMessage(field.messageType, value as Record<string, unknown>));
      return;
    }
    default:
      throw new SchemaError(`unknown field type: ${field.type}`);
  }
}

/** Reflection encode: serialize a plain object according to the schema. */
export function encodeMessage(
  schema: MessageSchema,
  obj: Record<string, unknown>,
): Uint8Array {
  const writer = new WireWriter();
  const sorted = [...schema.fields].sort((a, b) => a.fieldNumber - b.fieldNumber);
  for (const field of sorted) {
    const value = obj[field.name];
    if (value === undefined || value === null) continue;
    if (field.repeated) {
      const items = value as unknown[];
      if (field.packed && isPackable(field) && items.length > 0) {
        const inner = new WireWriter();
        for (const item of items) writeScalar(inner, field, item);
        writer.writeTag(field.fieldNumber, WireType.LengthDelimited);
        writer.writeBytes(inner.finish());
      } else {
        for (const item of items) {
          writer.writeTag(field.fieldNumber, wireTypeOf(field));
          writeScalar(writer, field, item);
        }
      }
    } else {
      writer.writeTag(field.fieldNumber, wireTypeOf(field));
      writeScalar(writer, field, value);
    }
  }
  return writer.finish();
}

const INT32_MIN = -2147483648;
const INT32_MAX = 2147483647;
const UINT32_MAX = 4294967295;
const INT64_MIN = -(1n << 63n);
const INT64_MAX = (1n << 63n) - 1n;
const UINT64_MAX = (1n << 64n) - 1n;

function validateScalar(
  field: FieldDescriptor,
  value: unknown,
  path: string,
  violations: string[],
): void {
  const fail = (reason: string) => violations.push(`${path}: ${reason}`);
  switch (field.type) {
    case FieldType.Int32:
    case FieldType.Sint32:
    case FieldType.Sfixed32:
      if (typeof value !== 'number' || !Number.isInteger(value)) return fail('expected integer');
      if (value < INT32_MIN || value > INT32_MAX) return fail(`out of int32 range: ${value}`);
      return;
    case FieldType.Uint32:
    case FieldType.Fixed32:
      if (typeof value !== 'number' || !Number.isInteger(value)) return fail('expected integer');
      if (value < 0 || value > UINT32_MAX) return fail(`out of uint32 range: ${value}`);
      return;
    case FieldType.Int64:
    case FieldType.Sint64:
    case FieldType.Sfixed64:
      if (typeof value !== 'bigint') return fail('expected bigint');
      if (value < INT64_MIN || value > INT64_MAX) return fail(`out of int64 range: ${value}`);
      return;
    case FieldType.Uint64:
    case FieldType.Fixed64:
      if (typeof value !== 'bigint') return fail('expected bigint');
      if (value < 0n || value > UINT64_MAX) return fail(`out of uint64 range: ${value}`);
      return;
    case FieldType.Bool:
      if (typeof value !== 'boolean') return fail('expected boolean');
      return;
    case FieldType.String:
      if (typeof value !== 'string') return fail('expected string');
      return;
    case FieldType.Bytes:
      if (!(value instanceof Uint8Array)) return fail('expected Uint8Array');
      return;
    case FieldType.Message: {
      if (typeof value !== 'object' || value === null || value instanceof Uint8Array) {
        return fail('expected nested message object');
      }
      if (field.messageType) {
        validateMessage(field.messageType, value as Record<string, unknown>, path, violations);
      }
      return;
    }
    default:
      return fail(`unknown field type: ${field.type}`);
  }
}

/**
 * Reflection validation engine: recursively checks a decoded object
 * against the schema. Collects every violation instead of failing fast.
 */
export function validateMessage(
  schema: MessageSchema,
  obj: Record<string, unknown>,
  path = schema.name,
  violations: string[] = [],
): string[] {
  for (const field of schema.fields) {
    const fieldPath = `${path}.${field.name}`;
    const value = obj[field.name];
    if (value === undefined || value === null) {
      if (field.required) violations.push(`${fieldPath}: required field is missing`);
      continue;
    }
    if (field.repeated) {
      if (!Array.isArray(value)) {
        violations.push(`${fieldPath}: expected array for repeated field`);
        continue;
      }
      value.forEach((item, i) => validateScalar(field, item, `${fieldPath}[${i}]`, violations));
    } else {
      validateScalar(field, value, fieldPath, violations);
    }
  }
  return violations;
}

/** Decode then validate; throws ValidationError listing every violation. */
export function decodeAndValidate(
  schema: MessageSchema,
  buf: Uint8Array,
): Record<string, unknown> {
  const obj = decodeMessage(schema, buf);
  const violations = validateMessage(schema, obj);
  if (violations.length > 0) throw new ValidationError(violations);
  return obj;
}
