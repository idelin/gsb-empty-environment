/**
 * 反射模式校验引擎: 以 MessageSchema 描述符驱动编解码,
 * 解码时逐字段校验 wire type 兼容性、枚举域、required 约束,
 * 未知字段安全跳过, packed/unpacked repeated 均可还原。
 */

import { SchemaValidationError, WireFormatError } from './errors.ts';
import { WireReader, WireWriter, WireType } from './wire.ts';
import { zigzagDecode, zigzagEncode, int64ToUint64, uint64ToInt64 } from './varint.ts';

export type ScalarType =
  | 'int64'
  | 'uint64'
  | 'sint64'
  | 'bool'
  | 'fixed32'
  | 'fixed64'
  | 'float'
  | 'double'
  | 'string'
  | 'bytes'
  | 'enum';

export interface FieldSpec {
  name: string;
  number: number;
  type: ScalarType | 'message';
  repeated?: boolean;
  /** repeated 标量按 packed 编码 (解码端两种格式都接受)。 */
  packed?: boolean;
  required?: boolean;
  /** type === 'message' 时的子 schema。 */
  message?: MessageSchema;
  /** type === 'enum' 时的合法取值集合。 */
  enumValues?: ReadonlySet<number>;
}

export interface MessageSchema {
  name: string;
  fields: FieldSpec[];
}

export type DecodedMessage = Record<string, unknown>;

function expectedWireType(f: FieldSpec): WireType {
  switch (f.type) {
    case 'int64':
    case 'uint64':
    case 'sint64':
    case 'bool':
    case 'enum':
      return WireType.Varint;
    case 'fixed64':
    case 'double':
      return WireType.Fixed64;
    case 'fixed32':
    case 'float':
      return WireType.Fixed32;
    case 'string':
    case 'bytes':
    case 'message':
      return WireType.LengthDelimited;
  }
}

/** packed 元素类型 -> 元素 wire type。 */
function isPackable(t: FieldSpec['type']): boolean {
  return (
    t === 'int64' || t === 'uint64' || t === 'sint64' || t === 'bool' ||
    t === 'enum' || t === 'fixed32' || t === 'fixed64' ||
    t === 'float' || t === 'double'
  );
}

function readScalar(reader: WireReader, f: FieldSpec, path: string): unknown {
  switch (f.type) {
    case 'int64':
      return reader.readInt64();
    case 'uint64':
      return reader.readVarint();
    case 'sint64':
      return reader.readSInt64();
    case 'bool':
      return reader.readBool();
    case 'enum': {
      const v = Number(reader.readVarint());
      if (f.enumValues && !f.enumValues.has(v)) {
        throw new SchemaValidationError(`枚举值 ${v} 不在合法域内`, path);
      }
      return v;
    }
    case 'fixed32':
      return reader.readFixed32();
    case 'fixed64':
      return reader.readFixed64();
    case 'float':
      return reader.readFloat();
    case 'double':
      return reader.readDouble();
    case 'string':
      return reader.readString();
    case 'bytes':
      return reader.readBytes();
    case 'message':
      throw new WireFormatError('message 类型不走标量路径', reader.offset);
  }
}

export interface DecodeOptions {
  /** 遇到未知字段时是否报错 (默认安全跳过)。 */
  rejectUnknownFields?: boolean;
}

export class ReflectionEngine {
  /** 按 schema 解码, 返回以字段名为键的结构体。 */
  decode(
    schema: MessageSchema,
    bytes: Uint8Array,
    options: DecodeOptions = {},
  ): DecodedMessage {
    return this.decodeFrom(schema, new WireReader(bytes), schema.name, options);
  }

  private decodeFrom(
    schema: MessageSchema,
    reader: WireReader,
    path: string,
    options: DecodeOptions,
  ): DecodedMessage {
    const byNumber = new Map(schema.fields.map((f) => [f.number, f]));
    const out: DecodedMessage = {};
    const seen = new Set<string>();

    while (!reader.done) {
      const header = reader.readFieldHeader();
      const spec = byNumber.get(header.fieldNumber);
      if (!spec) {
        if (options.rejectUnknownFields) {
          throw new SchemaValidationError(
            `未知字段 #${header.fieldNumber}`,
            path,
          );
        }
        reader.skipField(header.wireType);
        continue;
      }
      const fieldPath = `${path}.${spec.name}`;
      const value = this.readFieldValue(reader, spec, header.wireType, fieldPath, options);
      if (spec.repeated) {
        seen.add(spec.name);
        const arr = (out[spec.name] ??= []) as unknown[];
        if (Array.isArray(value)) arr.push(...value); // packed 展开
        else arr.push(value);
      } else {
        seen.add(spec.name);
        out[spec.name] = value;
      }
    }

    for (const f of schema.fields) {
      if (f.required && !seen.has(f.name)) {
        throw new SchemaValidationError(`缺少 required 字段 ${f.name}`, path);
      }
    }
    return out;
  }

  private readFieldValue(
    reader: WireReader,
    spec: FieldSpec,
    wireType: WireType,
    path: string,
    options: DecodeOptions,
  ): unknown {
    // packed repeated: length-delimited 块内连续元素
    if (spec.repeated && spec.packed && wireType === WireType.LengthDelimited) {
      if (!isPackable(spec.type)) {
        throw new SchemaValidationError(`类型 ${spec.type} 不可 packed`, path);
      }
      const sub = new WireReader(reader.readBytes());
      const values: unknown[] = [];
      while (!sub.done) values.push(readScalar(sub, spec, path));
      return values;
    }

    const expected = expectedWireType(spec);
    if (wireType !== expected) {
      throw new SchemaValidationError(
        `wire type 不匹配: 期望 ${expected}, 实际 ${wireType}`,
        path,
      );
    }

    if (spec.type === 'message') {
      if (!spec.message) {
        throw new SchemaValidationError('message 字段缺少子 schema', path);
      }
      return this.decodeFrom(spec.message, reader.readSubMessage(), path, options);
    }
    return readScalar(reader, spec, path);
  }

  /** 按 schema 编码结构体。 */
  encode(schema: MessageSchema, message: DecodedMessage): Uint8Array {
    const writer = new WireWriter();
    for (const f of schema.fields) {
      const value = message[f.name];
      if (value === undefined || value === null) {
        if (f.required) {
          throw new SchemaValidationError(
            `缺少 required 字段 ${f.name}`,
            schema.name,
          );
        }
        continue;
      }
      if (f.repeated) {
        const items = value as unknown[];
        if (f.packed && isPackable(f.type)) {
          this.writePacked(writer, f, items);
        } else {
          for (const item of items) this.writeOne(writer, f, item);
        }
      } else {
        this.writeOne(writer, f, value);
      }
    }
    return writer.finish();
  }

  private writeOne(writer: WireWriter, f: FieldSpec, value: unknown): void {
    switch (f.type) {
      case 'int64':
        writer.writeInt64(f.number, value as bigint);
        return;
      case 'uint64':
        writer.writeUint64(f.number, value as bigint);
        return;
      case 'sint64':
        writer.writeSInt64(f.number, value as bigint);
        return;
      case 'bool':
        writer.writeBool(f.number, value as boolean);
        return;
      case 'enum': {
        const v = value as number;
        if (f.enumValues && !f.enumValues.has(v)) {
          throw new SchemaValidationError(
            `枚举值 ${v} 不在合法域内`,
            `${f.name}`,
          );
        }
        writer.writeUint64(f.number, BigInt(v));
        return;
      }
      case 'fixed32':
        writer.writeFixed32(f.number, value as number);
        return;
      case 'fixed64':
        writer.writeFixed64(f.number, value as bigint);
        return;
      case 'float':
        writer.writeFloat(f.number, value as number);
        return;
      case 'double':
        writer.writeDouble(f.number, value as number);
        return;
      case 'string':
        writer.writeString(f.number, value as string);
        return;
      case 'bytes':
        writer.writeBytes(f.number, value as Uint8Array);
        return;
      case 'message': {
        if (!f.message) {
          throw new SchemaValidationError('message 字段缺少子 schema', f.name);
        }
        const sub = this.encode(f.message, value as DecodedMessage);
        writer.writeBytes(f.number, sub);
        return;
      }
    }
  }

  private writePacked(writer: WireWriter, f: FieldSpec, items: unknown[]): void {
    const sub = new WireWriter();
    // 定长元素先累积到独立缓冲区, 再一次性写入
    const fixedWidth =
      f.type === 'fixed32' || f.type === 'float' ? 4
      : f.type === 'fixed64' || f.type === 'double' ? 8
      : 0;
    const fixed = fixedWidth > 0 ? new Uint8Array(items.length * fixedWidth) : null;
    const fixedView = fixed ? new DataView(fixed.buffer) : null;
    items.forEach((item, i) => {
      switch (f.type) {
        case 'int64':
          sub.writeVarintRaw(int64ToUint64(item as bigint));
          break;
        case 'uint64':
          sub.writeVarintRaw(item as bigint);
          break;
        case 'sint64':
          sub.writeVarintRaw(zigzagEncode(item as bigint));
          break;
        case 'bool':
          sub.writeVarintRaw(item ? 1n : 0n);
          break;
        case 'enum':
          sub.writeVarintRaw(BigInt(item as number));
          break;
        case 'fixed32':
          fixedView!.setUint32(i * 4, (item as number) >>> 0, true);
          break;
        case 'float':
          fixedView!.setFloat32(i * 4, item as number, true);
          break;
        case 'fixed64':
          fixedView!.setBigUint64(i * 8, (item as bigint) & 0xffffffffffffffffn, true);
          break;
        case 'double':
          fixedView!.setFloat64(i * 8, item as number, true);
          break;
        default:
          throw new SchemaValidationError(`类型 ${f.type} 不可 packed`, f.name);
      }
    });
    if (fixed) sub.writeRawBytes(fixed);
    writer.writeBytes(f.number, sub.finish());
  }
}

export { zigzagDecode, zigzagEncode, uint64ToInt64 };
