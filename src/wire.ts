/**
 * Protobuf 二进制 Wire-format 安全读写器。
 * Wire types: 0=Varint, 1=Fixed64, 2=LengthDelimited, 5=Fixed32。
 * 读端所有访问都经过边界检查 —— 任何截断字段都会触发安全越界防护。
 */

import { TruncatedFieldError, WireFormatError } from './errors.ts';
import {
  decodeVarint,
  encodeVarint,
  varintLength,
  zigzagDecode,
  zigzagEncode,
  int64ToUint64,
  uint64ToInt64,
} from './varint.ts';

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireType = (typeof WireType)[keyof typeof WireType];

export interface FieldHeader {
  fieldNumber: number;
  wireType: WireType;
}

/** 只读安全游标: 手工解析二进制流, 全程边界防护。 */
export class WireReader {
  private pos = 0;
  private readonly buf: Uint8Array;
  private readonly limit: number;
  constructor(buf: Uint8Array, limit: number = buf.length) {
    this.buf = buf;
    this.limit = limit;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.limit;
  }

  private need(n: number, what: string): void {
    if (n > this.remaining) {
      throw new TruncatedFieldError(n, this.remaining, this.pos, what);
    }
  }

  /** 读取字段头 (tag varint)。 */
  readFieldHeader(): FieldHeader {
    this.need(1, 'field tag');
    const { value, bytesRead } = decodeVarint(this.buf, this.pos);
    this.pos += bytesRead;
    const tag = Number(value & 0xffffffffn);
    const wireType = (tag & 0x7) as WireType;
    const fieldNumber = tag >>> 3;
    if (fieldNumber === 0) {
      throw new WireFormatError('非法字段号 0', this.pos - bytesRead);
    }
    if (
      wireType !== WireType.Varint &&
      wireType !== WireType.Fixed64 &&
      wireType !== WireType.LengthDelimited &&
      wireType !== WireType.Fixed32
    ) {
      throw new WireFormatError(`不支持的 wire type ${wireType}`, this.pos - bytesRead);
    }
    return { fieldNumber, wireType };
  }

  readVarint(): bigint {
    this.need(1, 'varint');
    const { value, bytesRead } = decodeVarint(this.buf, this.pos);
    this.pos += bytesRead;
    return value;
  }

  readInt64(): bigint {
    return uint64ToInt64(this.readVarint());
  }

  readSInt64(): bigint {
    return zigzagDecode(this.readVarint());
  }

  readBool(): boolean {
    return this.readVarint() !== 0n;
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const v =
      this.buf[this.pos] |
      (this.buf[this.pos + 1] << 8) |
      (this.buf[this.pos + 2] << 16) |
      (this.buf[this.pos + 3] << 24);
    this.pos += 4;
    return v >>> 0;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    let v = 0n;
    for (let i = 0; i < 8; i++) {
      v |= BigInt(this.buf[this.pos + i]) << BigInt(8 * i);
    }
    this.pos += 8;
    return v;
  }

  readFloat(): number {
    this.need(4, 'float');
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4);
    const v = view.getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  readDouble(): number {
    this.need(8, 'double');
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8);
    const v = view.getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  /** 读取 length-delimited 载荷 (带长度前缀的原始字节)。 */
  readBytes(): Uint8Array {
    const len = Number(this.readVarint());
    if (len > 0x7fffffff) {
      throw new WireFormatError(`length-delimited 长度异常: ${len}`, this.pos);
    }
    this.need(len, 'length-delimited payload');
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return new TextDecoder('utf-8', { fatal: true }).decode(this.readBytes());
  }

  /** 嵌套消息: 返回限定在载荷区间内的子读取器。 */
  readSubMessage(): WireReader {
    const bytes = this.readBytes();
    return new WireReader(bytes, bytes.length);
  }

  /** 未知字段安全跳过。 */
  skipField(wireType: WireType): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.need(8, 'skip fixed64');
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = Number(this.readVarint());
        this.need(len, 'skip length-delimited');
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.need(4, 'skip fixed32');
        this.pos += 4;
        return;
      default:
        throw new WireFormatError(`无法跳过未知 wire type ${wireType}`, this.pos);
    }
  }
}

/** 编码器: 追加式写出 wire-format 字节。 */
export class WireWriter {
  private chunks: Uint8Array[] = [];
  private size = 0;

  private push(bytes: Uint8Array): void {
    this.chunks.push(bytes);
    this.size += bytes.length;
  }

  writeTag(fieldNumber: number, wireType: WireType): this {
    this.push(encodeVarint(BigInt((fieldNumber << 3) | wireType)));
    return this;
  }

  writeVarintRaw(value: bigint): this {
    this.push(encodeVarint(value));
    return this;
  }

  /** 追加无前缀裸字节 (packed 定长元素用)。 */
  writeRawBytes(bytes: Uint8Array): this {
    this.push(bytes);
    return this;
  }

  writeUint64(fieldNumber: number, value: bigint): this {
    return this.writeTag(fieldNumber, WireType.Varint).writeVarintRaw(value);
  }

  writeInt64(fieldNumber: number, value: bigint): this {
    return this.writeUint64(fieldNumber, int64ToUint64(value));
  }

  writeSInt64(fieldNumber: number, value: bigint): this {
    return this.writeUint64(fieldNumber, zigzagEncode(value));
  }

  writeBool(fieldNumber: number, value: boolean): this {
    return this.writeUint64(fieldNumber, value ? 1n : 0n);
  }

  writeFixed32(fieldNumber: number, value: number): this {
    this.writeTag(fieldNumber, WireType.Fixed32);
    const b = new Uint8Array(4);
    new DataView(b.buffer).setUint32(0, value >>> 0, true);
    return this.push(b), this;
  }

  writeFixed64(fieldNumber: number, value: bigint): this {
    this.writeTag(fieldNumber, WireType.Fixed64);
    const b = new Uint8Array(8);
    new DataView(b.buffer).setBigUint64(0, value & ((1n << 64n) - 1n), true);
    return this.push(b), this;
  }

  writeFloat(fieldNumber: number, value: number): this {
    this.writeTag(fieldNumber, WireType.Fixed32);
    const b = new Uint8Array(4);
    new DataView(b.buffer).setFloat32(0, value, true);
    return this.push(b), this;
  }

  writeDouble(fieldNumber: number, value: number): this {
    this.writeTag(fieldNumber, WireType.Fixed64);
    const b = new Uint8Array(8);
    new DataView(b.buffer).setFloat64(0, value, true);
    return this.push(b), this;
  }

  writeBytes(fieldNumber: number, value: Uint8Array): this {
    this.writeTag(fieldNumber, WireType.LengthDelimited);
    this.push(encodeVarint(BigInt(value.length)));
    return this.push(value), this;
  }

  writeString(fieldNumber: number, value: string): this {
    return this.writeBytes(fieldNumber, new TextEncoder().encode(value));
  }

  /** 嵌套消息: 先编码子消息再带长度前缀写入。 */
  writeMessage(fieldNumber: number, encode: (w: WireWriter) => void): this {
    const sub = new WireWriter();
    encode(sub);
    return this.writeBytes(fieldNumber, sub.finish());
  }

  /** Packed repeated: 所有元素紧排在一个 length-delimited 块内。 */
  writePackedVarints(fieldNumber: number, values: bigint[]): this {
    const sub = new WireWriter();
    for (const v of values) sub.writeVarintRaw(v);
    return this.writeBytes(fieldNumber, sub.finish());
  }

  writePackedFixed32(fieldNumber: number, values: number[]): this {
    const b = new Uint8Array(values.length * 4);
    const view = new DataView(b.buffer);
    values.forEach((v, i) => view.setUint32(i * 4, v >>> 0, true));
    return this.writeBytes(fieldNumber, b);
  }

  writePackedFixed64(fieldNumber: number, values: bigint[]): this {
    const b = new Uint8Array(values.length * 8);
    const view = new DataView(b.buffer);
    values.forEach((v, i) => view.setBigUint64(i * 8, v & ((1n << 64n) - 1n), true));
    return this.writeBytes(fieldNumber, b);
  }

  /** 预计算 packed varint 块长度 (用于测试与上层规划)。 */
  static packedVarintLength(values: bigint[]): number {
    return values.reduce((acc, v) => acc + varintLength(v), 0);
  }

  finish(): Uint8Array {
    const out = new Uint8Array(this.size);
    let off = 0;
    for (const c of this.chunks) {
      out.set(c, off);
      off += c.length;
    }
    this.chunks = [];
    this.size = 0;
    return out;
  }
}
