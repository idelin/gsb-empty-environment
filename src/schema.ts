import { MalformedMessageError, ValidationError } from './errors.ts';
import { WireReader, WireType, type WireTypeValue } from './reader.ts';
import { WireWriter } from './writer.ts';
import {
  encodeVarint,
  encodeVarintSigned,
  zigZagDecode32,
  zigZagDecode64,
  zigZagEncode32,
  zigZagEncode64,
} from './varint.ts';

export type ScalarType =
  | 'int32'
  | 'int64'
  | 'uint32'
  | 'uint64'
  | 'sint32'
  | 'sint64'
  | 'bool'
  | 'enum'
  | 'fixed32'
  | 'sfixed32'
  | 'float'
  | 'fixed64'
  | 'sfixed64'
  | 'double'
  | 'string'
  | 'bytes';

export type FieldType = ScalarType | 'message';

export interface FieldSpec {
  name: string;
  number: number;
  type: FieldType;
  /** Nested schema, required when type === 'message'. */
  message?: MessageSchema;
  repeated?: boolean;
  /** Encode repeated scalars packed (LEN wire type). Decode always accepts both forms. */
  packed?: boolean;
  required?: boolean;
  default?: unknown;
  min?: number | bigint;
  max?: number | bigint;
  maxLength?: number;
  validate?: (value: unknown) => boolean | string;
}

export interface MessageSchema {
  name: string;
  fields: FieldSpec[];
}

export interface DecodeOptions {
  /** Maximum nesting depth for embedded messages (stack-overflow guard). */
  maxDepth?: number;
}

export interface ValidateOptions {
  /** Maximum nesting depth tolerated in the value graph. */
  maxDepth?: number;
}

export const DEFAULT_MAX_DEPTH = 100;

const WIRE_TYPE_OF: Record<ScalarType, WireTypeValue> = {
  int32: WireType.VARINT,
  int64: WireType.VARINT,
  uint32: WireType.VARINT,
  uint64: WireType.VARINT,
  sint32: WireType.VARINT,
  sint64: WireType.VARINT,
  bool: WireType.VARINT,
  enum: WireType.VARINT,
  fixed32: WireType.FIXED32,
  sfixed32: WireType.FIXED32,
  float: WireType.FIXED32,
  fixed64: WireType.FIXED64,
  sfixed64: WireType.FIXED64,
  double: WireType.FIXED64,
  string: WireType.LEN,
  bytes: WireType.LEN,
};

/** Scalar types that may appear in a packed repeated field. */
const PACKABLE: ReadonlySet<ScalarType> = new Set([
  'int32',
  'int64',
  'uint32',
  'uint64',
  'sint32',
  'sint64',
  'bool',
  'enum',
  'fixed32',
  'sfixed32',
  'float',
  'fixed64',
  'sfixed64',
  'double',
]);

function wireTypeOf(field: FieldSpec): WireTypeValue {
  return field.type === 'message' ? WireType.LEN : WIRE_TYPE_OF[field.type];
}

function isPackable(field: FieldSpec): boolean {
  return field.type !== 'message' && PACKABLE.has(field.type);
}

function readScalar(reader: WireReader, type: ScalarType): unknown {
  switch (type) {
    case 'int32':
      return Number(BigInt.asIntN(32, reader.readVarint()));
    case 'int64':
      return BigInt.asIntN(64, reader.readVarint());
    case 'uint32':
      return Number(reader.readVarint() & 0xffffffffn);
    case 'uint64':
      return reader.readVarint();
    case 'sint32':
      return zigZagDecode32(Number(reader.readVarint() & 0xffffffffn));
    case 'sint64':
      return zigZagDecode64(reader.readVarint());
    case 'bool':
      return reader.readVarint() !== 0n;
    case 'enum':
      return Number(BigInt.asIntN(32, reader.readVarint()));
    case 'fixed32':
      return reader.readFixed32();
    case 'sfixed32':
      return reader.readFixed32() | 0;
    case 'float':
      return reader.readFloat();
    case 'fixed64':
      return reader.readFixed64();
    case 'sfixed64':
      return BigInt.asIntN(64, reader.readFixed64());
    case 'double':
      return reader.readDouble();
    case 'string':
      return reader.readString();
    case 'bytes':
      return reader.readBytes(reader.readVarintNumber());
  }
}

function writeScalar(writer: WireWriter, type: ScalarType, value: unknown): void {
  switch (type) {
    case 'int32':
    case 'enum':
      writer.writeBytes(encodeVarintSigned(value as number));
      return;
    case 'int64':
      writer.writeBytes(encodeVarintSigned(value as bigint));
      return;
    case 'uint32':
      writer.writeVarint(value as number);
      return;
    case 'uint64':
      writer.writeVarint(value as bigint);
      return;
    case 'sint32':
      writer.writeVarint(zigZagEncode32(value as number));
      return;
    case 'sint64':
      writer.writeVarint(zigZagEncode64(value as bigint));
      return;
    case 'bool':
      writer.writeVarint(value ? 1 : 0);
      return;
    case 'fixed32':
      writer.writeFixed32(value as number);
      return;
    case 'sfixed32':
      writer.writeFixed32((value as number) | 0);
      return;
    case 'float':
      writer.writeFloat(value as number);
      return;
    case 'fixed64':
      writer.writeFixed64(value as bigint);
      return;
    case 'sfixed64':
      writer.writeFixed64(value as bigint);
      return;
    case 'double':
      writer.writeDouble(value as number);
      return;
    case 'string':
      writer.writeString(value as string);
      return;
    case 'bytes':
      writer.writeLengthDelimited(value as Uint8Array);
      return;
  }
}

function decodeInto(
  schema: MessageSchema,
  reader: WireReader,
  depth: number,
  maxDepth: number,
): Record<string, unknown> {
  if (depth > maxDepth) {
    throw new MalformedMessageError(
      `message nesting exceeds max depth ${maxDepth} (possible cyclic payload)`,
      reader.pos,
    );
  }
  const byNumber = new Map<number, FieldSpec>();
  for (const f of schema.fields) byNumber.set(f.number, f);

  const out: Record<string, unknown> = {};
  const seen = new Set<string>();

  while (!reader.done) {
    const tag = reader.readTag();
    const field = byNumber.get(tag.fieldNumber);
    if (!field) {
      reader.skipField(tag.wireType);
      continue;
    }
    seen.add(field.name);

    if (field.repeated) {
      let arr = out[field.name] as unknown[] | undefined;
      if (!arr) arr = out[field.name] = [];
      if (isPackable(field) && tag.wireType === WireType.LEN) {
        // Packed encoding: a single LEN field containing back-to-back scalars.
        const packed = reader.readLengthDelimited();
        const sub = new WireReader(packed);
        while (!sub.done) arr.push(readScalar(sub, field.type as ScalarType));
      } else if (tag.wireType === wireTypeOf(field)) {
        arr.push(readOne(reader, field, depth, maxDepth));
      } else {
        throw new MalformedMessageError(
          `field ${field.name}: unexpected wire type ${tag.wireType}`,
          reader.pos,
        );
      }
    } else {
      if (tag.wireType !== wireTypeOf(field)) {
        throw new MalformedMessageError(
          `field ${field.name}: unexpected wire type ${tag.wireType}`,
          reader.pos,
        );
      }
      out[field.name] = readOne(reader, field, depth, maxDepth);
    }
  }

  for (const field of schema.fields) {
    if (!seen.has(field.name) && field.default !== undefined) {
      out[field.name] = field.default;
    }
  }
  return out;
}

function readOne(
  reader: WireReader,
  field: FieldSpec,
  depth: number,
  maxDepth: number,
): unknown {
  if (field.type === 'message') {
    const bytes = reader.readLengthDelimited();
    const sub = new WireReader(bytes);
    const value = decodeInto(field.message!, sub, depth + 1, maxDepth);
    if (!sub.done) {
      throw new MalformedMessageError(`nested message ${field.name} has trailing bytes`, reader.pos);
    }
    return value;
  }
  return readScalar(reader, field.type as ScalarType);
}

/** Decode a protobuf binary buffer into a plain object according to `schema`. */
export function decodeMessage(
  schema: MessageSchema,
  data: Uint8Array,
  options: DecodeOptions = {},
): Record<string, unknown> {
  const reader = new WireReader(data);
  const maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
  const value = decodeInto(schema, reader, 0, maxDepth);
  validateMessage(schema, value, { maxDepth });
  return value;
}

function encodeField(writer: WireWriter, field: FieldSpec, value: unknown): void {
  if (field.type === 'message') {
    const nested = encodeMessage(field.message!, value as Record<string, unknown>);
    writer.writeTag(field.number, WireType.LEN);
    writer.writeLengthDelimited(nested);
    return;
  }
  const type = field.type as ScalarType;
  writer.writeTag(field.number, WIRE_TYPE_OF[type]);
  writeScalar(writer, type, value);
}

/** Encode a plain object into protobuf wire format according to `schema`. Validates first. */
export function encodeMessage(schema: MessageSchema, value: Record<string, unknown>): Uint8Array {
  validateMessage(schema, value);
  const writer = new WireWriter();
  for (const field of schema.fields) {
    const v = value[field.name];
    if (v === undefined || v === null) continue;
    if (field.repeated) {
      const items = v as unknown[];
      if (items.length === 0) continue;
      const packed = field.packed !== false && isPackable(field);
      if (packed) {
        const inner = new WireWriter();
        for (const item of items) writeScalar(inner, field.type as ScalarType, item);
        writer.writeTag(field.number, WireType.LEN);
        writer.writeLengthDelimited(inner.finish());
      } else {
        for (const item of items) encodeField(writer, field, item);
      }
    } else {
      encodeField(writer, field, v);
    }
  }
  return writer.finish();
}

function checkScalarType(type: ScalarType, v: unknown): boolean {
  switch (type) {
    case 'int32':
    case 'uint32':
    case 'sint32':
    case 'enum':
      return typeof v === 'number' && Number.isInteger(v);
    case 'fixed32':
    case 'sfixed32':
      return typeof v === 'number' && Number.isInteger(v);
    case 'float':
    case 'double':
      return typeof v === 'number';
    case 'int64':
    case 'uint64':
    case 'sint64':
    case 'fixed64':
    case 'sfixed64':
      return typeof v === 'bigint';
    case 'bool':
      return typeof v === 'boolean';
    case 'string':
      return typeof v === 'string';
    case 'bytes':
      return v instanceof Uint8Array;
  }
}

function validateOne(
  field: FieldSpec,
  v: unknown,
  path: string,
  issues: string[],
  depth: number,
  maxDepth: number,
): void {
  if (depth > maxDepth) {
    issues.push(`${path}: value graph exceeds max depth ${maxDepth}`);
    return;
  }
  if (field.type === 'message') {
    if (typeof v !== 'object' || v === null || Array.isArray(v)) {
      issues.push(`${path}: expected object for message ${field.message!.name}`);
      return;
    }
    collectIssues(field.message!, v as Record<string, unknown>, path, issues, depth + 1, maxDepth);
    return;
  }
  const type = field.type as ScalarType;
  if (!checkScalarType(type, v)) {
    issues.push(`${path}: expected ${type}, got ${typeof v}`);
    return;
  }
  if (field.min !== undefined && (v as number | bigint) < field.min) {
    issues.push(`${path}: ${String(v)} < min ${String(field.min)}`);
  }
  if (field.max !== undefined && (v as number | bigint) > field.max) {
    issues.push(`${path}: ${String(v)} > max ${String(field.max)}`);
  }
  if (
    field.maxLength !== undefined &&
    (type === 'string' || type === 'bytes') &&
    (v as string | Uint8Array).length > field.maxLength
  ) {
    issues.push(`${path}: length ${(v as string | Uint8Array).length} > maxLength ${field.maxLength}`);
  }
  if (field.validate) {
    const verdict = field.validate(v);
    if (verdict !== true) {
      issues.push(`${path}: ${typeof verdict === 'string' ? verdict : 'custom validation failed'}`);
    }
  }
}

function collectIssues(
  schema: MessageSchema,
  value: Record<string, unknown>,
  path: string,
  issues: string[],
  depth: number,
  maxDepth: number,
): void {
  for (const field of schema.fields) {
    const fieldPath = path ? `${path}.${field.name}` : field.name;
    const v = value[field.name];
    if (v === undefined || v === null) {
      if (field.required) issues.push(`${fieldPath}: required field is missing`);
      continue;
    }
    if (field.repeated) {
      if (!Array.isArray(v)) {
        issues.push(`${fieldPath}: expected array for repeated field`);
        continue;
      }
      v.forEach((item, i) => validateOne(field, item, `${fieldPath}[${i}]`, issues, depth, maxDepth));
    } else {
      validateOne(field, v, fieldPath, issues, depth, maxDepth);
    }
  }
}

/** Reflection-mode validation: walks the schema and checks a decoded/to-be-encoded value. */
export function validateMessage(
  schema: MessageSchema,
  value: Record<string, unknown>,
  options: ValidateOptions = {},
): void {
  const issues: string[] = [];
  collectIssues(schema, value, '', issues, 0, options.maxDepth ?? DEFAULT_MAX_DEPTH);
  if (issues.length > 0) throw new ValidationError(issues);
}
