import { ProtobufTruncationError } from './errors.ts';
import { decodeVarint } from './varint.ts';
import { zigzagDecode32, zigzagDecode64 } from './zigzag.ts';
import { parseTag, WireType } from './wire.ts';

const textDecoder = new TextDecoder();

/**
 * Bounds-checked cursor over a protobuf wire-format buffer.
 * Every read validates the remaining length first: a truncated field always
 * raises ProtobufTruncationError instead of reading out of bounds.
 */
export class Reader {
  private pos = 0;
  readonly buf: Uint8Array;

  constructor(buf: Uint8Array) {
    this.buf = buf;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.buf.length - this.pos;
  }

  get eof(): boolean {
    return this.pos >= this.buf.length;
  }

  private need(n: number): void {
    if (this.remaining < n) {
      throw new ProtobufTruncationError(this.pos, n, this.remaining);
    }
  }

  /** Read a field tag; returns null at end of buffer. */
  tag(): { fieldNumber: number; wireType: WireType } | null {
    if (this.eof) return null;
    const key = this.varint();
    return parseTag(key);
  }

  varint(): bigint {
    const { value, offset } = decodeVarint(this.buf, this.pos);
    this.pos = offset;
    return value;
  }

  uint32(): number {
    return Number(BigInt.asUintN(32, this.varint()));
  }

  int32(): number {
    return Number(BigInt.asIntN(32, this.varint()));
  }

  uint64(): bigint {
    return BigInt.asUintN(64, this.varint());
  }

  int64(): bigint {
    return BigInt.asIntN(64, this.varint());
  }

  sint32(): number {
    return zigzagDecode32(Number(BigInt.asUintN(32, this.varint())));
  }

  sint64(): bigint {
    return zigzagDecode64(this.varint());
  }

  bool(): boolean {
    return this.varint() !== 0n;
  }

  fixed32(): number {
    this.need(4);
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4).getUint32(0, true);
    this.pos += 4;
    return v;
  }

  sfixed32(): number {
    this.need(4);
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4).getInt32(0, true);
    this.pos += 4;
    return v;
  }

  fixed64(): bigint {
    this.need(8);
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8).getBigUint64(0, true);
    this.pos += 8;
    return v;
  }

  sfixed64(): bigint {
    this.need(8);
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8).getBigInt64(0, true);
    this.pos += 8;
    return v;
  }

  float(): number {
    this.need(4);
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4).getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  double(): number {
    this.need(8);
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8).getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  /** Read a length-delimited byte slice (bounds-checked). */
  bytes(): Uint8Array {
    const len = Number(this.varint());
    this.need(len);
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  string(): string {
    return textDecoder.decode(this.bytes());
  }

  /** Skip a field of the given wire type (bounds-checked). */
  skip(wireType: WireType): void {
    switch (wireType) {
      case WireType.Varint:
        this.varint();
        return;
      case WireType.Fixed64:
        this.need(8);
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = Number(this.varint());
        this.need(len);
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.need(4);
        this.pos += 4;
        return;
    }
  }
}
