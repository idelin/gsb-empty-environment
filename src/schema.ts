import { ProtoError } from './errors.ts';
import {
  Reader,
  WireType,
  Writer,
  readScalarVarint,
  writeScalarVarint,
  type WireTypeValue,
} from './wire.ts';

/** 支持的标量字段类型。 */
export type ScalarType =
  | 'int32' | 'int64' | 'uint32' | 'uint64'
  | 'sint32' | 'sint64'
  | 'bool' | 'enum'
  | 'fixed32' | 'fixed64' | 'sfixed32' | 'sfixed64'
  | 'float' | 'double'
  | 'string' | 'bytes';

export interface FieldDescriptor {
  number: number;
  name: string;
  type: ScalarType | 'message';
  repeated?: boolean;
  /** repeated 标量是否按 packed 编码（解码端两种格式都接受）。 */
  packed?: boolean;
  required?: boolean;
  /** type === 'message' 时的子消息 schema。 */
  message?: MessageSchema;
}

export interface MessageSchema {
  name: string;
  fields: FieldDescriptor[];
}

export type MessageValue = Record<string, unknown>;

const VARINT_KINDS = new Set(['int32', 'int64', 'uint32', 'uint64', 'sint32', 'sint64', 'bool', 'enum']);
const FIXED32_KINDS = new Set(['fixed32', 'sfixed32', 'float']);
const FIXED64_KINDS = new Set(['fixed64', 'sfixed64', 'double']);

function expectedWireType(field: FieldDescriptor): WireTypeValue {
  const t = field.type;
  if (VARINT_KINDS.has(t)) return WireType.Varint;
  if (FIXED32_KINDS.has(t)) return WireType.Fixed32;
  if (FIXED64_KINDS.has(t)) return WireType.Fixed64;
  return WireType.LengthDelimited; // string / bytes / message
}

function isPackable(field: FieldDescriptor): boolean {
  return field.type !== 'string' && field.type !== 'bytes' && field.type !== 'message';
}

function readScalar(r: Reader, type: ScalarType): unknown {
  if (VARINT_KINDS.has(type)) return readScalarVarint(r, type);
  switch (type) {
    case 'fixed32': return r.readFixed32();
    case 'sfixed32': return r.readFixed32() | 0;
    case 'float': return r.readFloat();
    case 'fixed64': return r.readFixed64();
    case 'sfixed64': return BigInt.asIntN(64, r.readFixed64());
    case 'double': return r.readDouble();
    case 'string': return r.readString();
    case 'bytes': return r.readBytes();
    default: throw new ProtoError('SCHEMA_VIOLATION', `未知标量类型 ${type}`);
  }
}

function writeScalar(w: Writer, type: ScalarType, value: unknown): void {
  if (VARINT_KINDS.has(type)) {
    writeScalarVarint(w, type, value);
    return;
  }
  switch (type) {
    case 'fixed32': w.writeFixed32(value as number); return;
    case 'sfixed32': w.writeFixed32((value as number) | 0); return;
    case 'float': w.writeFloat(value as number); return;
    case 'fixed64': w.writeFixed64(value as bigint | number); return;
    case 'sfixed64': w.writeFixed64(value as bigint | number); return;
    case 'double': w.writeDouble(value as number); return;
    case 'string': w.writeString(value as string); return;
    case 'bytes': w.writeLengthDelimited(value as Uint8Array); return;
    default: throw new ProtoError('SCHEMA_VIOLATION', `未知标量类型 ${type}`);
  }
}

/** 编码：按 schema 将结构化对象写成 protobuf 二进制流。 */
export function encodeMessage(schema: MessageSchema, value: MessageValue): Uint8Array {
  const w = new Writer();
  encodeInto(w, schema, value, schema.name);
  return w.finish();
}

function encodeInto(w: Writer, schema: MessageSchema, value: MessageValue, path: string): void {
  const byNumber = new Map(schema.fields.map((f) => [f.number, f]));
  const sorted = [...byNumber.values()].sort((a, b) => a.number - b.number);
  for (const field of sorted) {
    const v = value[field.name];
    const fieldPath = `${path}.${field.name}`;
    if (v === undefined || v === null) {
      if (field.required) {
        throw new ProtoError('SCHEMA_VIOLATION', `缺少必填字段 ${field.name}`, -1, fieldPath);
      }
      continue;
    }
    if (field.repeated) {
      const arr = v as unknown[];
      if (!Array.isArray(arr)) {
        throw new ProtoError('SCHEMA_VIOLATION', `repeated 字段应为数组`, -1, fieldPath);
      }
      if (field.packed && isPackable(field)) {
        const inner = new Writer();
        for (const item of arr) writeScalar(inner, field.type as ScalarType, item);
        w.writeTag(field.number, WireType.LengthDelimited);
        w.writeLengthDelimited(inner.finish());
      } else {
        for (const item of arr) encodeOne(w, field, item, fieldPath);
      }
    } else {
      encodeOne(w, field, v, fieldPath);
    }
  }
}

function encodeOne(w: Writer, field: FieldDescriptor, v: unknown, path: string): void {
  if (field.type === 'message') {
    if (!field.message) throw new ProtoError('SCHEMA_VIOLATION', 'message 字段缺少子 schema', -1, path);
    const inner = new Writer();
    encodeInto(inner, field.message, v as MessageValue, path);
    w.writeTag(field.number, WireType.LengthDelimited);
    w.writeLengthDelimited(inner.finish());
    return;
  }
  w.writeTag(field.number, expectedWireType(field));
  writeScalar(w, field.type as ScalarType, v);
}

/**
 * 解码 + 反射校验：
 *  - wire type 与 schema 不匹配 -> SCHEMA_VIOLATION
 *  - packed / 非 packed repeated 均可还原
 *  - 未知字段安全跳过
 *  - 任何截断 -> TRUNCATED（带字节偏移与字段路径）
 */
export function decodeMessage(schema: MessageSchema, buf: Uint8Array): MessageValue {
  const r = new Reader(buf);
  const out = decodeInto(r, schema, schema.name);
  r.assertEnd();
  return out;
}

function decodeInto(r: Reader, schema: MessageSchema, path: string): MessageValue {
  const byNumber = new Map(schema.fields.map((f) => [f.number, f]));
  const out: MessageValue = {};
  for (const f of schema.fields) {
    if (f.repeated) out[f.name] = [];
  }
  while (!r.done) {
    const { fieldNumber, wireType } = r.readTag();
    const field = byNumber.get(fieldNumber);
    if (!field) {
      r.skip(wireType); // 未知字段：安全跳过
      continue;
    }
    const fieldPath = `${path}.${field.name}`;
    try {
      readField(r, field, wireType, out, fieldPath);
    } catch (err) {
      if (err instanceof ProtoError && !err.path) {
        throw new ProtoError(err.code, err.message.replace(/^\[[^\]]+\]\s*(@byte \d+)?\s*:?\s*/, ''), err.offset, fieldPath);
      }
      throw err;
    }
  }
  for (const f of schema.fields) {
    if (f.required && out[f.name] === undefined) {
      throw new ProtoError('SCHEMA_VIOLATION', `缺少必填字段 ${f.name}`, -1, `${path}.${f.name}`);
    }
  }
  return out;
}

function readField(
  r: Reader,
  field: FieldDescriptor,
  wireType: WireTypeValue,
  out: MessageValue,
  path: string,
): void {
  const expected = expectedWireType(field);
  // packed repeated：以 length-delimited 形式出现
  if (field.repeated && isPackable(field) && wireType === WireType.LengthDelimited) {
    const payload = r.readBytes();
    const sub = new Reader(payload);
    const arr = out[field.name] as unknown[];
    while (!sub.done) arr.push(readScalar(sub, field.type as ScalarType));
    return;
  }
  if (wireType !== expected) {
    throw new ProtoError(
      'SCHEMA_VIOLATION',
      `字段 ${field.name} 期望 wire type ${expected}，实际 ${wireType}`,
      r.offset,
      path,
    );
  }
  if (field.type === 'message') {
    const payload = r.readBytes();
    const sub = new Reader(payload);
    const nested = decodeInto(sub, field.message!, path);
    sub.assertEnd();
    if (field.repeated) (out[field.name] as unknown[]).push(nested);
    else out[field.name] = nested;
    return;
  }
  const value = readScalar(r, field.type as ScalarType);
  if (field.repeated) (out[field.name] as unknown[]).push(value);
  else out[field.name] = value;
}
