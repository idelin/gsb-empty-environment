import { ProtobufDecodeError, ProtobufEncodeError } from './errors.ts';
import {
  WireReader,
  WIRE_VARINT,
  WIRE_FIXED64,
  WIRE_LENGTH_DELIMITED,
  WIRE_FIXED32,
} from './reader.ts';
import { WireWriter } from './writer.ts';

/** 支持的标量字段类型。 */
export type FieldType =
  | 'int32' | 'int64' | 'uint32' | 'uint64'
  | 'sint32' | 'sint64'
  | 'fixed32' | 'fixed64' | 'sfixed32' | 'sfixed64'
  | 'bool' | 'float' | 'double'
  | 'string' | 'bytes'
  | 'message';

export interface FieldDescriptor {
  name: string;
  number: number;
  type: FieldType;
  repeated?: boolean;
  /** repeated 数值字段按 packed 编码（proto3 默认）。 */
  packed?: boolean;
  /** type === 'message' 时的嵌套 schema。 */
  messageType?: MessageSchema;
}

export type MessageValue = Record<string, unknown>;

const SIGNED_32 = new Set<FieldType>(['int32', 'sfixed32']);
const SIGNED_64 = new Set<FieldType>(['int64', 'sfixed64']);

function wireTypeOf(type: FieldType): number {
  switch (type) {
    case 'fixed64': case 'sfixed64': case 'double': return WIRE_FIXED64;
    case 'string': case 'bytes': case 'message': return WIRE_LENGTH_DELIMITED;
    case 'fixed32': case 'sfixed32': case 'float': return WIRE_FIXED32;
    default: return WIRE_VARINT;
  }
}

function isPackable(type: FieldType): boolean {
  return type !== 'string' && type !== 'bytes' && type !== 'message';
}

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

/**
 * 反射模式校验引擎：以字段描述符驱动编解码，
 * 解码时校验 wire type、字段边界与嵌套消息完整性。
 */
export class MessageSchema {
  readonly name: string;
  private readonly byNumber = new Map<number, FieldDescriptor>();
  readonly fields: readonly FieldDescriptor[];

  constructor(name: string, fields: FieldDescriptor[]) {
    this.name = name;
    this.fields = fields;
    for (const f of fields) {
      if (this.byNumber.has(f.number)) {
        throw new ProtobufEncodeError(`Duplicate field number ${f.number} in ${name}`);
      }
      if (f.type === 'message' && !f.messageType) {
        throw new ProtobufEncodeError(`Field ${f.name} requires messageType`);
      }
      if (f.packed && !(f.repeated && isPackable(f.type))) {
        throw new ProtobufEncodeError(`Field ${f.name} cannot be packed`);
      }
      this.byNumber.set(f.number, f);
    }
  }

  // ---------- 解码 ----------

  decode(buf: Uint8Array): MessageValue {
    const reader = new WireReader(buf);
    const out = this.decodeFrom(reader);
    if (!reader.eof) {
      throw new ProtobufDecodeError('Trailing bytes after message', reader.offset);
    }
    return out;
  }

  decodeFrom(reader: WireReader): MessageValue {
    const out: MessageValue = {};
    while (!reader.eof) {
      const tag = reader.readTag();
      const field = this.byNumber.get(tag.fieldNumber);
      if (!field) {
        reader.skipField(tag.wireType);
        continue;
      }
      const expected = wireTypeOf(field.type);
      if (field.repeated && field.packed !== false && isPackable(field.type)
          && tag.wireType === WIRE_LENGTH_DELIMITED) {
        // Packed repeated：长度前缀 + 连续元素
        const sub = reader.readLengthDelimited();
        const arr = this.ensureArray(out, field);
        while (!sub.eof) arr.push(this.readScalar(sub, field));
        continue;
      }
      if (tag.wireType !== expected) {
        throw new ProtobufDecodeError(
          `Wire type mismatch for field ${field.name}: expected ${expected}, got ${tag.wireType}`,
          reader.offset,
        );
      }
      const value = this.readScalar(reader, field);
      if (field.repeated) {
        this.ensureArray(out, field).push(value);
      } else {
        out[field.name] = value;
      }
    }
    return out;
  }

  private ensureArray(out: MessageValue, field: FieldDescriptor): unknown[] {
    let arr = out[field.name] as unknown[] | undefined;
    if (!arr) out[field.name] = arr = [];
    return arr;
  }

  private readScalar(reader: WireReader, field: FieldDescriptor): unknown {
    switch (field.type) {
      case 'int32': return toInt32(reader.readVarint32());
      case 'int64': return toInt64(reader.readVarint());
      case 'uint32': return reader.readVarint32();
      case 'uint64': return reader.readVarint();
      case 'sint32': return reader.readSVarint32();
      case 'sint64': return reader.readSVarint64();
      case 'bool': return reader.readBool();
      case 'fixed32': return reader.readFixed32();
      case 'fixed64': return reader.readFixed64();
      case 'sfixed32': return toInt32(reader.readFixed32());
      case 'sfixed64': return toInt64(reader.readFixed64());
      case 'float': return reader.readFloat();
      case 'double': return reader.readDouble();
      case 'string': {
        const sub = reader.readLengthDelimited();
        return textDecoder.decode(sub.readBytes(sub.remaining));
      }
      case 'bytes': {
        const sub = reader.readLengthDelimited();
        return sub.readBytes(sub.remaining).slice();
      }
      case 'message': {
        const sub = reader.readLengthDelimited();
        const nested = field.messageType!.decodeFrom(sub);
        if (!sub.eof) {
          throw new ProtobufDecodeError(
            `Nested message ${field.name} not fully consumed`, reader.offset);
        }
        return nested;
      }
      default:
        throw new ProtobufDecodeError(`Unknown field type ${field.type}`, reader.offset);
    }
  }

  // ---------- 编码 ----------

  encode(value: MessageValue): Uint8Array {
    const writer = new WireWriter();
    this.encodeTo(value, writer);
    return writer.finish();
  }

  encodeTo(value: MessageValue, writer: WireWriter): void {
    for (const field of this.fields) {
      const v = value[field.name];
      if (v === undefined || v === null) continue;
      if (field.repeated) {
        const arr = v as unknown[];
        if (arr.length === 0) continue;
        if (field.packed !== false && isPackable(field.type)) {
          const inner = new WireWriter();
          for (const item of arr) this.writeScalar(inner, field, item);
          writer.writeTag(field.number, WIRE_LENGTH_DELIMITED);
          writer.writeLengthDelimited(inner.finish());
        } else {
          for (const item of arr) {
            writer.writeTag(field.number, wireTypeOf(field.type));
            this.writeScalar(writer, field, item);
          }
        }
      } else {
        writer.writeTag(field.number, wireTypeOf(field.type));
        this.writeScalar(writer, field, v);
      }
    }
  }

  private writeScalar(writer: WireWriter, field: FieldDescriptor, v: unknown): void {
    switch (field.type) {
      case 'int32': case 'uint32': writer.writeVarint(v as number); return;
      case 'int64': case 'uint64': writer.writeVarint(v as bigint); return;
      case 'sint32': writer.writeSVarint32(v as number); return;
      case 'sint64': writer.writeSVarint64(v as bigint); return;
      case 'bool': writer.writeVarint(v ? 1 : 0); return;
      case 'fixed32': writer.writeFixed32(v as number); return;
      case 'fixed64': writer.writeFixed64(v as bigint); return;
      case 'sfixed32': writer.writeFixed32((v as number) >>> 0); return;
      case 'sfixed64': writer.writeFixed64(v as bigint); return;
      case 'float': writer.writeFloat(v as number); return;
      case 'double': writer.writeDouble(v as number); return;
      case 'string': writer.writeLengthDelimited(textEncoder.encode(v as string)); return;
      case 'bytes': writer.writeLengthDelimited(v as Uint8Array); return;
      case 'message': {
        const inner = new WireWriter();
        field.messageType!.encodeTo(v as MessageValue, inner);
        writer.writeLengthDelimited(inner.finish());
        return;
      }
      default:
        throw new ProtobufEncodeError(`Unknown field type ${field.type}`);
    }
  }
}

function toInt32(v: number): number {
  return v | 0;
}

function toInt64(v: bigint): bigint {
  const x = v & ((1n << 64n) - 1n);
  return x >= 1n << 63n ? x - (1n << 64n) : x;
}
