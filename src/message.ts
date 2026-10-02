/**
 * 反射模式校验引擎：以 Schema 描述驱动 Protobuf 消息的编解码与结构校验。
 * 支持嵌套 Length-delimited 消息、Packed/非 Packed Repeated 数组、未知字段跳过。
 */

import {
  ByteReader,
  ByteWriter,
  CodecError,
  TruncatedFieldError,
  WireType,
  type WireTypeValue,
} from './wire.ts';

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
  | 'bytes'
  | 'string';

export type FieldType = ScalarType | 'message';

export interface FieldDescriptor {
  name: string;
  number: number;
  type: FieldType;
  repeated?: boolean;
  /** repeated 标量是否按 packed(length-delimited) 编码；解码时两种编码均可识别。 */
  packed?: boolean;
  /** type === 'message' 时的子消息 schema。 */
  message?: MessageSchema;
}

export interface MessageSchema {
  name: string;
  fields: FieldDescriptor[];
}

export interface DecodeOptions {
  /** 最大嵌套深度（防深递归/栈耗尽），默认 32。 */
  maxDepth?: number;
  /** 单条消息最大字节数，默认 16MiB。 */
  maxBytes?: number;
}

export class SchemaError extends CodecError {}
export class MaxDepthExceededError extends CodecError {}

const SIGNED_64_MIN = -(1n << 63n);

function toSigned64(v: bigint): bigint {
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}

function wireTypeOf(type: ScalarType): WireTypeValue {
  switch (type) {
    case 'fixed64':
    case 'sfixed64':
      return WireType.Fixed64;
    case 'fixed32':
    case 'sfixed32':
      return WireType.Fixed32;
    case 'bytes':
    case 'string':
      return WireType.LengthDelimited;
    default:
      return WireType.Varint;
  }
}

function isPackable(type: FieldType): boolean {
  return type !== 'bytes' && type !== 'string' && type !== 'message';
}

function readScalar(reader: ByteReader, type: ScalarType): unknown {
  switch (type) {
    case 'int32':
      return Number(toSigned64(reader.readVarint()));
    case 'int64':
      return toSigned64(reader.readVarint());
    case 'uint32':
      return Number(reader.readVarint() & 0xffffffffn);
    case 'uint64':
      return reader.readVarint();
    case 'sint32':
      return Number(reader.readZigZag());
    case 'sint64':
      return reader.readZigZag();
    case 'bool':
      return reader.readVarint() !== 0n;
    case 'fixed32':
      return reader.readFixed32();
    case 'fixed64':
      return reader.readFixed64();
    case 'sfixed32':
      return reader.readFixed32() | 0;
    case 'sfixed64':
      return toSigned64(reader.readFixed64());
    case 'bytes':
      return reader.readBytes();
    case 'string':
      return reader.readString();
  }
}

function writeScalar(writer: ByteWriter, type: ScalarType, value: unknown): void {
  switch (type) {
    case 'int32':
    case 'int64':
      writer.writeVarint(BigInt(value as number | bigint));
      break;
    case 'uint32':
    case 'uint64':
      writer.writeVarint(BigInt(value as number | bigint));
      break;
    case 'sint32':
    case 'sint64':
      writer.writeZigZag(BigInt(value as number | bigint));
      break;
    case 'bool':
      writer.writeVarint(value ? 1 : 0);
      break;
    case 'fixed32':
    case 'sfixed32':
      writer.writeFixed32(Number(value));
      break;
    case 'fixed64':
    case 'sfixed64':
      writer.writeFixed64(BigInt(value as number | bigint));
      break;
    case 'bytes':
      writer.writeLengthDelimited(value as Uint8Array);
      break;
    case 'string':
      writer.writeString(String(value));
      break;
  }
}

function validateSchema(schema: MessageSchema): void {
  const seen = new Set<number>();
  for (const f of schema.fields) {
    if (!Number.isInteger(f.number) || f.number <= 0) {
      throw new SchemaError(`${schema.name}.${f.name}: 非法字段号 ${f.number}`);
    }
    if (seen.has(f.number)) {
      throw new SchemaError(`${schema.name}.${f.name}: 字段号 ${f.number} 重复`);
    }
    seen.add(f.number);
    if (f.type === 'message' && !f.message) {
      throw new SchemaError(`${schema.name}.${f.name}: message 类型缺少子 schema`);
    }
    if (f.packed && !(f.repeated && isPackable(f.type))) {
      throw new SchemaError(`${schema.name}.${f.name}: 仅 repeated 数值标量可 packed`);
    }
  }
}

/** 跳过未知字段（严格按 wire type 消费字节，带越界防护）。 */
function skipField(reader: ByteReader, wireType: WireTypeValue): void {
  switch (wireType) {
    case WireType.Varint:
      reader.readVarint();
      return;
    case WireType.Fixed64:
      reader.readFixed64();
      return;
    case WireType.LengthDelimited:
      reader.readBytes();
      return;
    case WireType.Fixed32:
      reader.readFixed32();
      return;
  }
}

function decodeInto(
  schema: MessageSchema,
  reader: ByteReader,
  depth: number,
  maxDepth: number,
): Record<string, unknown> {
  if (depth > maxDepth) {
    throw new MaxDepthExceededError(`嵌套深度超过上限 ${maxDepth}（消息 ${schema.name}）`);
  }
  const byNumber = new Map<number, FieldDescriptor>();
  for (const f of schema.fields) byNumber.set(f.number, f);

  const out: Record<string, unknown> = {};
  for (const f of schema.fields) {
    if (f.repeated) out[f.name] = [];
  }

  while (!reader.eof) {
    const [fieldNumber, wireType] = reader.readTag();
    const field = byNumber.get(fieldNumber);
    if (!field) {
      skipField(reader, wireType);
      continue;
    }
    reader.fieldNumber = fieldNumber;

    if (field.type === 'message') {
      if (wireType !== WireType.LengthDelimited) {
        throw new SchemaError(
          `${schema.name}.${field.name}: 期望 LengthDelimited，实际 wire type ${wireType}`,
        );
      }
      const len = reader.readVarintAsNumber();
      const sub = reader.subReader(len); // 越界时抛 TruncatedFieldError
      const value = decodeInto(field.message!, sub, depth + 1, maxDepth);
      if (field.repeated) (out[field.name] as unknown[]).push(value);
      else out[field.name] = value;
      continue;
    }

    const scalarType = field.type as ScalarType;
    const expected = wireTypeOf(scalarType);

    if (field.repeated && isPackable(field.type) && wireType === WireType.LengthDelimited) {
      // Packed repeated：长度前缀 + 连续标量
      const len = reader.readVarintAsNumber();
      const sub = reader.subReader(len);
      const arr = out[field.name] as unknown[];
      while (!sub.eof) {
        sub.fieldNumber = fieldNumber;
        arr.push(readScalar(sub, scalarType));
      }
      continue;
    }

    if (wireType !== expected) {
      throw new SchemaError(
        `${schema.name}.${field.name}: 期望 wire type ${expected}，实际 ${wireType}`,
      );
    }
    const value = readScalar(reader, scalarType);
    if (field.repeated) (out[field.name] as unknown[]).push(value);
    else out[field.name] = value;
  }
  return out;
}

/** 反射解码：按 schema 将二进制流还原为结构体对象。 */
export function decodeMessage<T = Record<string, unknown>>(
  schema: MessageSchema,
  bytes: Uint8Array,
  options: DecodeOptions = {},
): T {
  validateSchema(schema);
  const maxDepth = options.maxDepth ?? 32;
  const maxBytes = options.maxBytes ?? 16 * 1024 * 1024;
  if (bytes.length > maxBytes) {
    throw new CodecError(`消息大小 ${bytes.length} 超过上限 ${maxBytes} 字节`);
  }
  const reader = new ByteReader(bytes);
  return decodeInto(schema, reader, 0, maxDepth) as T;
}

function encodeField(writer: ByteWriter, field: FieldDescriptor, value: unknown): void {
  if (field.type === 'message') {
    const body = encodeMessage(field.message!, value as Record<string, unknown>);
    writer.writeTag(field.number, WireType.LengthDelimited);
    writer.writeLengthDelimited(body);
    return;
  }
  const scalarType = field.type as ScalarType;
  writer.writeTag(field.number, wireTypeOf(scalarType));
  writeScalar(writer, scalarType, value);
}

/** 反射编码：按 schema 将结构体对象序列化为二进制流。 */
export function encodeMessage(schema: MessageSchema, obj: Record<string, unknown>): Uint8Array {
  validateSchema(schema);
  const writer = new ByteWriter();
  for (const field of schema.fields) {
    const value = obj[field.name];
    if (value === undefined || value === null) continue;

    if (field.repeated) {
      const arr = value as unknown[];
      if (field.packed && isPackable(field.type)) {
        if (arr.length === 0) continue;
        const inner = new ByteWriter();
        for (const item of arr) writeScalar(inner, field.type as ScalarType, item);
        writer.writeTag(field.number, WireType.LengthDelimited);
        writer.writeLengthDelimited(inner.concat());
      } else {
        for (const item of arr) encodeField(writer, field, item);
      }
      continue;
    }
    encodeField(writer, field, value);
  }
  return writer.concat();
}

export { TruncatedFieldError };
