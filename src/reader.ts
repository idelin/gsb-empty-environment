import {
  ProtobufDecodeError,
  decodeVarint,
  zigzagDecode,
  toSigned64,
} from './encoding.ts';

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  StartGroup: 3,
  EndGroup: 4,
  Fixed32: 5,
} as const;

export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export interface Tag {
  fieldNumber: number;
  wireType: WireTypeValue;
}

export class ProtoReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly limit: number;

  constructor(buf: Uint8Array, offset = 0, length = buf.length - offset) {
    if (offset < 0 || length < 0 || offset + length > buf.length) {
      throw new ProtobufDecodeError('reader window out of bounds', offset);
    }
    this.buf = buf;
    this.pos = offset;
    this.limit = offset + length;
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

  private need(n: number): void {
    if (this.pos + n > this.limit) {
      throw new ProtobufDecodeError(
        `truncated field: need ${n} byte(s), only ${this.remaining} remain`,
        this.pos,
      );
    }
  }

  readTag(): Tag | null {
    if (this.eof) return null;
    const key = this.readVarint();
    const fieldNumber = Number(key >> 3n);
    const wireType = Number(key & 0x7n) as WireTypeValue;
    if (fieldNumber <= 0) {
      throw new ProtobufDecodeError(
        `invalid field number ${fieldNumber}`,
        this.pos,
      );
    }
    if (wireType === WireType.StartGroup || wireType === WireType.EndGroup) {
      throw new ProtobufDecodeError(
        'groups are not supported by this parser',
        this.pos,
      );
    }
    if (wireType > WireType.Fixed32) {
      throw new ProtobufDecodeError(`invalid wire type ${wireType}`, this.pos);
    }
    return { fieldNumber, wireType };
  }

  readVarint(): bigint {
    const { value, bytesRead } = decodeVarint(this.buf, this.pos, this.limit);
    this.pos += bytesRead;
    return value;
  }

  readVarint32(): number {
    return Number(this.readVarint() & 0xffffffffn);
  }

  readSVarint(): bigint {
    return zigzagDecode(this.readVarint());
  }

  readBool(): boolean {
    return this.readVarint() !== 0n;
  }

  readFixed32(): number {
    this.need(4);
    const v =
      this.buf[this.pos] |
      (this.buf[this.pos + 1] << 8) |
      (this.buf[this.pos + 2] << 16) |
      (this.buf[this.pos + 3] << 24);
    this.pos += 4;
    return v >>> 0;
  }

  readFixed64(): bigint {
    this.need(8);
    const lo = this.readFixed32();
    const hi = this.readFixed32();
    return (BigInt(hi) << 32n) | BigInt(lo);
  }

  readFloat(): number {
    this.need(4);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4);
    const v = view.getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  readDouble(): number {
    this.need(8);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8);
    const v = view.getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  readBytes(): Uint8Array {
    const len = this.readVarint32();
    if (len > this.remaining) {
      throw new ProtobufDecodeError(
        `truncated length-delimited field: declared ${len} byte(s), only ${this.remaining} remain`,
        this.pos,
      );
    }
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return new TextDecoder('utf-8', { fatal: true }).decode(this.readBytes());
  }

  readInt64(): bigint {
    return toSigned64(this.readVarint());
  }

  subReader(): ProtoReader {
    const bytes = this.readBytes();
    return new ProtoReader(bytes);
  }

  skip(wireType: WireTypeValue): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.need(8);
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = this.readVarint32();
        this.need(len);
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.need(4);
        this.pos += 4;
        return;
      default:
        throw new ProtobufDecodeError(
          `cannot skip unknown wire type ${wireType}`,
          this.pos,
        );
    }
  }
}
