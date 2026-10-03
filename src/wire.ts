import { ProtoError } from './errors.ts';
import {
  decodeVarint,
  encodeVarint,
  toSigned64,
  toUnsigned64,
  varintLength,
  zigzagDecode,
  zigzagEncode,
} from './varint.ts';

/** Protobuf wire types（const 对象代替 enum，兼容 Node 类型剥离）。 */
export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export function isValidWireType(w: number): w is WireTypeValue {
  return w === 0 || w === 1 || w === 2 || w === 5;
}

/** 增量式编码器：零拷贝收集 chunk，finish 时一次性拼接。 */
export class Writer {
  private chunks: Uint8Array[] = [];
  private byteLength = 0;

  private push(bytes: Uint8Array): this {
    this.chunks.push(bytes);
    this.byteLength += bytes.length;
    return this;
  }

  writeRawBytes(bytes: Uint8Array): this {
    return this.push(bytes);
  }

  writeVarint(value: bigint | number): this {
    return this.push(encodeVarint(value));
  }

  writeTag(fieldNumber: number, wireType: WireTypeValue): this {
    if (!Number.isInteger(fieldNumber) || fieldNumber < 1 || fieldNumber > 0x1fffffff) {
      throw new ProtoError('INVALID_TAG', `非法字段号 ${fieldNumber}`);
    }
    return this.writeVarint((fieldNumber << 3) | wireType);
  }

  writeFixed32(value: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setUint32(0, value >>> 0, true);
    return this.push(b);
  }

  writeFixed64(value: bigint | number): this {
    const b = new Uint8Array(8);
    new DataView(b.buffer).setBigUint64(0, toUnsigned64(value), true);
    return this.push(b);
  }

  writeFloat(value: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setFloat32(0, value, true);
    return this.push(b);
  }

  writeDouble(value: number): this {
    const b = new Uint8Array(8);
    new DataView(b.buffer).setFloat64(0, value, true);
    return this.push(b);
  }

  /** 写入 length-delimited 块（tag 已由调用方写入）。 */
  writeLengthDelimited(payload: Uint8Array): this {
    this.writeVarint(payload.length);
    return this.push(payload);
  }

  writeString(text: string): this {
    return this.writeLengthDelimited(new TextEncoder().encode(text));
  }

  finish(): Uint8Array {
    const out = new Uint8Array(this.byteLength);
    let offset = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, offset);
      offset += chunk.length;
    }
    this.chunks = [];
    this.byteLength = 0;
    return out;
  }
}

/**
 * 边界安全读取器：所有读取先校验剩余长度，越界一律抛 TRUNCATED，
 * 绝不返回 undefined/NaN 或读取邻接内存。
 */
export class Reader {
  private pos: number;
  private readonly end: number;
  private readonly buf: Uint8Array;

  constructor(
    buf: Uint8Array,
    offset = 0,
    length = buf.length - offset,
  ) {
    this.buf = buf;
    if (offset < 0 || length < 0 || offset + length > buf.length) {
      throw new ProtoError('TRUNCATED', 'Reader 初始窗口越界', offset);
    }
    this.pos = offset;
    this.end = offset + length;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.end;
  }

  private need(n: number, what: string): void {
    if (this.pos + n > this.end) {
      throw new ProtoError(
        'TRUNCATED',
        `${what} 需要 ${n} 字节，仅剩 ${this.end - this.pos} 字节`,
        this.pos,
      );
    }
  }

  readVarint(): bigint {
    const { value, bytesRead } = decodeVarint(this.buf, this.pos, this.end);
    this.pos += bytesRead;
    return value;
  }

  readVarintAsNumber(limit: number): number {
    const v = this.readVarint();
    if (v > BigInt(limit)) {
      throw new ProtoError('OVERFLOW', `varint 值 ${v} 超过上限 ${limit}`, this.pos);
    }
    return Number(v);
  }

  /** 读取并校验 tag，返回字段号与 wire type。 */
  readTag(): { fieldNumber: number; wireType: WireTypeValue } {
    const at = this.pos;
    const tag = this.readVarint();
    const wireType = Number(tag & 0x7n);
    const fieldNumber = Number(tag >> 3n);
    if (!isValidWireType(wireType)) {
      throw new ProtoError('INVALID_WIRE_TYPE', `不支持的 wire type ${wireType}`, at);
    }
    if (fieldNumber < 1 || fieldNumber > 0x1fffffff) {
      throw new ProtoError('INVALID_TAG', `非法字段号 ${fieldNumber}`, at);
    }
    return { fieldNumber, wireType };
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4).getUint32(0, true);
    this.pos += 4;
    return v;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8).getBigUint64(0, true);
    this.pos += 8;
    return v;
  }

  readFloat(): number {
    this.need(4, 'float');
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4).getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  readDouble(): number {
    this.need(8, 'double');
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8).getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  /** 读取 length-delimited 块，返回子视图（不拷贝）。长度越界 -> TRUNCATED。 */
  readBytes(): Uint8Array {
    const at = this.pos;
    const len = this.readVarintAsNumber(0x7fffffff);
    this.need(len, 'length-delimited 载荷');
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    if (out.length !== len) {
      // 理论不可达，防御性断言
      throw new ProtoError('TRUNCATED', 'length-delimited 载荷不足', at);
    }
    return out;
  }

  readString(): string {
    return new TextDecoder('utf-8', { fatal: true }).decode(this.readBytes());
  }

  /** 按 wire type 安全跳过未知字段。 */
  skip(wireType: WireTypeValue): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.need(8, 'fixed64');
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = this.readVarintAsNumber(0x7fffffff);
        this.need(len, 'length-delimited 载荷');
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.need(4, 'fixed32');
        this.pos += 4;
        return;
      default:
        throw new ProtoError('INVALID_WIRE_TYPE', `无法跳过 wire type ${wireType}`, this.pos);
    }
  }

  assertEnd(): void {
    if (!this.done) {
      throw new ProtoError('SCHEMA_VIOLATION', `消息末尾残留 ${this.remaining} 字节`, this.pos);
    }
  }
}

/* ---------- 标量便捷编解码（供反射引擎复用） ---------- */

export function writeScalarVarint(w: Writer, kind: string, value: unknown): void {
  switch (kind) {
    case 'int32':
    case 'int64':
    case 'uint32':
    case 'uint64':
    case 'enum':
      w.writeVarint(toUnsigned64(value as bigint | number));
      return;
    case 'sint32':
    case 'sint64':
      w.writeVarint(zigzagEncode(value as bigint | number));
      return;
    case 'bool':
      w.writeVarint(value ? 1 : 0);
      return;
    default:
      throw new ProtoError('SCHEMA_VIOLATION', `非 varint 标量类型 ${kind}`);
  }
}

export function readScalarVarint(r: Reader, kind: string): bigint | number | boolean {
  const raw = r.readVarint();
  switch (kind) {
    case 'int32':
      return Number(toSigned64(raw)) | 0;
    case 'int64':
      return toSigned64(raw);
    case 'uint32':
      return Number(raw & 0xffffffffn);
    case 'uint64':
      return raw;
    case 'enum':
      return Number(toSigned64(raw)) | 0;
    case 'sint32':
      return Number(zigzagDecode(raw)) | 0;
    case 'sint64':
      return zigzagDecode(raw);
    case 'bool':
      return raw !== 0n;
    default:
      throw new ProtoError('SCHEMA_VIOLATION', `非 varint 标量类型 ${kind}`);
  }
}

export { varintLength };
