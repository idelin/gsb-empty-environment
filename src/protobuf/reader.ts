import { WireError } from './errors.ts';
import { decodeVarint } from './varint.ts';

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  SGroup: 3,
  EGroup: 4,
  Fixed32: 5,
} as const;

export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export interface Tag {
  readonly field: number;
  readonly wireType: WireTypeValue;
}

export class WireReader {
  private pos: number;
  private readonly limit: number;
  private readonly buf: Uint8Array;

  constructor(buf: Uint8Array, offset = 0, end = buf.length) {
    if (offset < 0 || end > buf.length || offset > end) {
      throw new WireError('读取窗口越界：offset/end 不合法', offset);
    }
    this.buf = buf;
    this.pos = offset;
    this.limit = end;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  get offset(): number {
    return this.pos;
  }

  get done(): boolean {
    return this.pos >= this.limit;
  }

  private need(n: number, what: string): void {
    if (this.remaining < n) {
      throw new WireError(
        what + ' 截断：需要 ' + n + ' 字节，仅剩 ' + this.remaining + ' 字节，安全越界防护已触发',
        this.pos,
      );
    }
  }

  readVarint(): bigint {
    const { value, next } = decodeVarint(this.buf, this.pos, this.limit);
    this.pos = next;
    return value;
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const b = this.buf;
    const p = this.pos;
    this.pos += 4;
    return (b[p] | (b[p + 1] << 8) | (b[p + 2] << 16) | (b[p + 3] << 24)) >>> 0;
  }

  readFixed64(): bigint {
    const lo = BigInt(this.readFixed32());
    const hi = BigInt(this.readFixed32());
    return (hi << 32n) | lo;
  }

  readFloat(): number {
    this.need(4, 'float');
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4);
    this.pos += 4;
    return view.getFloat32(0, true);
  }

  readDouble(): number {
    this.need(8, 'double');
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8);
    this.pos += 8;
    return view.getFloat64(0, true);
  }

  readBytes(len: number): Uint8Array {
    if (!Number.isInteger(len) || len < 0) {
      throw new WireError('非法长度：' + len, this.pos);
    }
    this.need(len, 'length-delimited');
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readLengthDelimited(): Uint8Array {
    const len = this.readVarint();
    if (len > BigInt(this.remaining)) {
      throw new WireError(
        'length-delimited 声明长度 ' + len + ' 超过剩余 ' + this.remaining + ' 字节，安全越界防护已触发',
        this.pos,
      );
    }
    return this.readBytes(Number(len));
  }

  readTag(): Tag {
    const v = this.readVarint();
    const wireType = Number(v & 7n) as WireTypeValue;
    const field = Number(v >> 3n);
    if (field < 1 || field > 0x1fffffff) {
      throw new WireError('非法字段号 ' + field, this.pos);
    }
    if (wireType === 6 || wireType === 7) {
      throw new WireError('非法 wire type ' + wireType, this.pos);
    }
    if (wireType === WireType.SGroup || wireType === WireType.EGroup) {
      throw new WireError('不支持已废弃的 group wire type ' + wireType, this.pos);
    }
    return { field, wireType };
  }

  skip(wireType: WireTypeValue): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.need(8, 'fixed64');
        this.pos += 8;
        return;
      case WireType.LengthDelimited:
        this.readLengthDelimited();
        return;
      case WireType.Fixed32:
        this.need(4, 'fixed32');
        this.pos += 4;
        return;
      default:
        throw new WireError('无法跳过未知 wire type ' + wireType, this.pos);
    }
  }
}
