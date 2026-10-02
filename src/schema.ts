/**
 * 反射模式消息编解码：以 MessageDescriptor 描述结构体，
 * 纯手工解析 Wire-format，支持嵌套 Length-delimited 消息与 Packed Repeated。
 * 未知字段被保留（$unknown），可无损回写。
 */

import { MalformedError } from './errors.ts';
import { WireReader, WireType } from './reader.ts';
import type { WireTypeValue } from './reader.ts';
import { WireWriter } from './writer.ts';
import { zigzagDecode32, zigzagDecode64, zigzagEncode32, zigzagEncode64 } from './varint.ts';

export type ScalarKind =
  | 'int32' | 'int64' | 'uint32' | 'uint64'
  | 'sint32' | 'sint64'
  | 'bool'
  | 'fixed32' | 'fixed64' | 'sfixed32' | 'sfixed64'
  | 'float' | 'double'
  | 'bytes' | 'string';

export type FieldKind = ScalarKind | 'message';

export interface FieldDescriptor {
  name: string;
  number: number;
  kind: FieldKind;
  /** kind === 'message' 时的嵌套描述符 */
  message?: MessageDescriptor;
  repeated?: boolean;
  /** repeated 标量是否按 packed 编码（解码端两种形态都接受） */
  packed?: boolean;
  /** 巡检用：标记该字段为实体主键 */
  id?: boolean;
  /** 巡检用：标记该字段为指向目标实体类型的引用 */
  ref?: string;
}

export interface MessageDescriptor {
  typeName: string;
  fields: FieldDescriptor[];
}

export interface UnknownField {
  fieldNumber: number;
  wireType: WireTypeValue;
  value: bigint | number | Uint8Array;
}

export type DecodedMessage = {
  $type: string;
  $unknown: UnknownField[];
  [key: string]: unknown;
};

export const DEFAULT_MAX_DEPTH = 100;

export interface DecodeOptions {
  maxDepth?: number;
}

const textDecoder = new TextDecoder('utf-8', { fatal: true });
const textEncoder = new TextEncoder();

function expectedWireType(kind: ScalarKind): WireTypeValue {
  switch (kind) {
    case 'int32': case 'int64': case 'uint32': case 'uint64':
    case 'sint32': case 'sint64': case 'bool':
      return WireType.Varint;
    case 'fixed64': case 'sfixed64': case 'double':
      return WireType.Fixed64;
    case 'fixed32': case 'sfixed32': case 'float':
      return WireType.Fixed32;
    case 'bytes': case 'string':
      return WireType.LengthDelimited;
  }
}

function isPackable(kind: FieldKind): kind is ScalarKind {
  return kind !== 'message' && kind !== 'bytes' && kind !== 'string';
}

function decodeScalar(reader: WireReader, kind: ScalarKind): unknown {
  switch (kind) {
    case 'int32': return Number(BigInt.asIntN(32, reader.readVarint()));
    case 'int64': return BigInt.asIntN(64, reader.readVarint());
    case 'uint32': return Number(reader.readVarint() & 0xffffffffn);
    case 'uint64': return reader.readVarint();
    case 'sint32': return zigzagDecode32(Number(reader.readVarint() & 0xffffffffn));
    case 'sint64': return zigzagDecode64(reader.readVarint());
    case 'bool': return reader.readVarint() !== 0n;
    case 'fixed32': return reader.readFixed32();
    case 'fixed64': return reader.readFixed64();
    case 'sfixed32': return reader.readSFixed32();
    case 'sfixed64': return reader.readSFixed64();
    case 'float': return reader.readFloat();
    case 'double': return reader.readDouble();
    case 'bytes': return reader.readLengthDelimited();
    case 'string': {
      const raw = reader.readLengthDelimited();
      try {
        return textDecoder.decode(raw);
      } catch {
        throw new MalformedError('invalid UTF-8 in string field', reader.offset);
      }
    }
  }
}

function readUnknown(reader: WireReader, fieldNumber: number, wireType: WireTypeValue): UnknownField {
  switch (wireType) {
    case WireType.Varint:
      return { fieldNumber, wireType, value: reader.readVarint() };
    case WireType.Fixed64:
      return { fieldNumber, wireType, value: reader.readFixed64() };
    case WireType.LengthDelimited:
      return { fieldNumber, wireType, value: reader.readLengthDelimited() };
    case WireType.Fixed32:
      return { fieldNumber, wireType, value: reader.readFixed32() };
    default:
      throw new MalformedError(`unsupported wire type ${wireType}`, reader.offset);
  }
}

function assignField(out: DecodedMessage, field: FieldDescriptor, value: unknown): void {
  if (field.repeated) {
    (out[field.name] as unknown[]).push(value);
  } else {
    out[field.name] = value;
  }
}

function decodeField(
  field: FieldDescriptor,
  reader: WireReader,
  out: DecodedMessage,
  wireType: WireTypeValue,
  depth: number,
  maxDepth: number,
): void {
  const at = reader.offset;
  if (field.kind === 'message') {
    if (wireType !== WireType.LengthDelimited) {
      throw new MalformedError(`field "${field.name}" expects length-delimited message, got wire type ${wireType}`, at);
    }
    if (!field.message) {
      throw new MalformedError(`field "${field.name}" missing nested message descriptor`, at);
    }
    const payload = reader.readLengthDelimited();
    const child = decodeMessage(field.message, payload, { maxDepth, _depth: depth + 1 });
    assignField(out, field, child);
    return;
  }

  const kind = field.kind as ScalarKind;
  const expected = expectedWireType(kind);

  if (field.repeated && isPackable(kind) && wireType === WireType.LengthDelimited) {
    // Packed Repeated：长度前缀负载内连续排列的标量
    const payload = reader.readLengthDelimited();
    const sub = new WireReader(payload);
    while (!sub.eof) {
      assignField(out, field, decodeScalar(sub, kind));
    }
    return;
  }

  if (wireType !== expected) {
    throw new MalformedError(
      `field "${field.name}" expects wire type ${expected}, got ${wireType}`,
      at,
    );
  }
  assignField(out, field, decodeScalar(reader, kind));
}

export function decodeMessage(
  desc: MessageDescriptor,
  input: Uint8Array | WireReader,
  options: DecodeOptions & { _depth?: number } = {},
): DecodedMessage {
  const depth = options._depth ?? 0;
  const maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
  const reader = input instanceof WireReader ? input : new WireReader(input);
  if (depth > maxDepth) {
    throw new MalformedError(`nesting depth exceeds limit ${maxDepth}`, reader.offset);
  }

  const out: DecodedMessage = { $type: desc.typeName, $unknown: [] };
  for (const f of desc.fields) {
    if (f.repeated) out[f.name] = [];
  }
  const byNumber = new Map<number, FieldDescriptor>();
  for (const f of desc.fields) byNumber.set(f.number, f);

  while (!reader.eof) {
    const tag = reader.readTag();
    const field = byNumber.get(tag.fieldNumber);
    if (!field) {
      out.$unknown.push(readUnknown(reader, tag.fieldNumber, tag.wireType));
      continue;
    }
    decodeField(field, reader, out, tag.wireType, depth, maxDepth);
  }
  return out;
}

/* ---------------- 编码端（用于样例与测试构造夹具） ---------------- */

function encodeScalar(writer: WireWriter, kind: ScalarKind, value: unknown): void {
  switch (kind) {
    case 'int32': case 'int64': case 'uint32': case 'uint64':
      writer.writeVarint(value as number | bigint);
      return;
    case 'sint32': writer.writeVarint(zigzagEncode32(value as number)); return;
    case 'sint64': writer.writeVarint(zigzagEncode64(value as bigint)); return;
    case 'bool': writer.writeVarint(value ? 1 : 0); return;
    case 'fixed32': writer.writeFixed32(value as number); return;
    case 'fixed64': writer.writeFixed64(value as bigint); return;
    case 'sfixed32': writer.writeFixed32((value as number) >>> 0); return;
    case 'sfixed64': writer.writeFixed64(BigInt.asUintN(64, value as bigint)); return;
    case 'float': writer.writeFloat(value as number); return;
    case 'double': writer.writeDouble(value as number); return;
    case 'bytes': writer.writeLengthDelimited(value as Uint8Array); return;
    case 'string': writer.writeLengthDelimited(textEncoder.encode(value as string)); return;
  }
}

function encodeField(writer: WireWriter, field: FieldDescriptor, value: unknown, maxDepth: number, depth: number): void {
  if (field.kind === 'message') {
    const payload = encodeMessage(field.message as MessageDescriptor, value as Record<string, unknown>, { maxDepth, _depth: depth + 1 });
    writer.writeTag(field.number, WireType.LengthDelimited);
    writer.writeLengthDelimited(payload);
    return;
  }
  const kind = field.kind as ScalarKind;
  writer.writeTag(field.number, expectedWireType(kind));
  encodeScalar(writer, kind, value);
}

export function encodeMessage(
  desc: MessageDescriptor,
  obj: Record<string, unknown>,
  options: DecodeOptions & { _depth?: number } = {},
): Uint8Array {
  const depth = options._depth ?? 0;
  const maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
  if (depth > maxDepth) {
    throw new MalformedError(`nesting depth exceeds limit ${maxDepth}`, 0);
  }
  const writer = new WireWriter();
  for (const field of desc.fields) {
    const value = obj[field.name];
    if (value === undefined || value === null) continue;
    if (field.repeated) {
      const items = value as unknown[];
      if (field.packed && field.kind !== 'message' && isPackable(field.kind)) {
        const packed = new WireWriter();
        for (const item of items) encodeScalar(packed, field.kind as ScalarKind, item);
        writer.writeTag(field.number, WireType.LengthDelimited);
        writer.writeLengthDelimited(packed.finish());
      } else {
        for (const item of items) encodeField(writer, field, item, maxDepth, depth);
      }
    } else {
      encodeField(writer, field, value, maxDepth, depth);
    }
  }
  const unknown = obj.$unknown as UnknownField[] | undefined;
  if (Array.isArray(unknown)) {
    for (const u of unknown) {
      writer.writeTag(u.fieldNumber, u.wireType);
      switch (u.wireType) {
        case WireType.Varint: writer.writeVarint(u.value as bigint); break;
        case WireType.Fixed64: writer.writeFixed64(u.value as bigint); break;
        case WireType.LengthDelimited: writer.writeLengthDelimited(u.value as Uint8Array); break;
        case WireType.Fixed32: writer.writeFixed32(u.value as number); break;
      }
    }
  }
  return writer.finish();
}
