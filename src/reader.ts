/**
 * WireReader：在固定窗口 [start, end) 上的只读游标。
 * 所有读取先 require() 校验剩余字节，任何越界都以 TruncationError 安全失败，
 * 绝不读取窗口之外的内存。读取 bytes 一律返回拷贝，不保留输入缓冲引用。
 */

import { MalformedError, TruncationError } from './errors.ts';
import { decodeVarint } from './varint.ts';

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  StartGroup: 3,
  EndGroup: 4,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export const MAX_FIELD_NUMBER = 0x1fffffff; // 2^29 - 1

export interface Tag {
  fieldNumber: number;
  wireType: WireTypeValue;
}

export class WireReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly limit: number;

  constructor(buf: Uint8Array, start = 0, end = buf.length) {
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end > buf.length || start > end) {
      throw new RangeError(`invalid reader window [${start}, ${end}) for buffer of ${buf.length}`);
    }
    this.buf = buf;
    this.pos = start;
    this.limit = end;
  }

  get offset(): number {
    return this.pos;
  }
  get remaining(): number {
    return this.limit - this.pos;
  }
  get eof(): boolean {
    return this.pos >= this.limit;
  }

  private require(n: number): void {
    if (this.remaining < n) {
      throw new TruncationError(`truncated: need ${n} more byte(s)`, this.pos, n, this.remaining);
    }
  }

  readVarint(): bigint {
    const { value, bytesRead } = decodeVarint(this.buf, this.pos, this.limit);
    this.pos += bytesRead;
    return value;
  }

  readTag(): Tag {
    const at = this.pos;
    const raw = this.readVarint();
    const fieldNumber = Number(raw >> 3n);
    const wireType = Number(raw & 7n) as WireTypeValue;
    if (fieldNumber < 1 || fieldNumber > MAX_FIELD_NUMBER) {
      throw new MalformedError(`invalid field number ${fieldNumber}`, at);
    }
    if (wireType === WireType.StartGroup || wireType === WireType.EndGroup || wireType > WireType.Fixed32) {
      throw new MalformedError(`unsupported wire type ${wireType}`, at);
    }
    return { fieldNumber, wireType };
  }

  readFixed32(): number {
    this.require(4);
    const p = this.pos;
    const b = this.buf;
    this.pos += 4;
    return (b[p] | (b[p + 1] << 8) | (b[p + 2] << 16) | (b[p + 3] << 24)) >>> 0;
  }

  readFixed64(): bigint {
    const lo = BigInt(this.readFixed32());
    const hi = BigInt(this.readFixed32());
    return (hi << 32n) | lo;
  }

  readSFixed32(): number {
    return this.readFixed32() | 0;
  }

  readSFixed64(): bigint {
    return BigInt.asIntN(64, this.readFixed64());
  }

  readFloat(): number {
    this.require(4);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4);
    this.pos += 4;
    return view.getFloat32(0, true);
  }

  readDouble(): number {
    this.require(8);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8);
    this.pos += 8;
    return view.getFloat64(0, true);
  }

  /** 返回拷贝，调用方之后修改输入缓冲不影响结果（无悬挂引用）。 */
  readBytes(n: number): Uint8Array {
    if (!Number.isInteger(n) || n < 0) {
      throw new MalformedError(`invalid byte length ${n}`, this.pos);
    }
    this.require(n);
    const out = this.buf.slice(this.pos, this.pos + n);
    this.pos += n;
    return out;
  }

  readLengthDelimited(): Uint8Array {
    const at = this.pos;
    const len = this.readVarint();
    if (len > BigInt(this.remaining)) {
      throw new TruncationError(
        `length-delimited field declares ${len} byte(s) beyond buffer end`,
        at,
        Number(len > BigInt(Number.MAX_SAFE_INTEGER) ? -1 : len),
        this.remaining,
      );
    }
    return this.readBytes(Number(len));
  }

  skip(wireType: WireTypeValue): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.require(8);
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const payload = this.readLengthDelimited();
        void payload;
        return;
      }
      case WireType.Fixed32:
        this.require(4);
        this.pos += 4;
        return;
      default:
        throw new MalformedError(`cannot skip wire type ${wireType}`, this.pos);
    }
  }
}
