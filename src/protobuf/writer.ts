import { encodeVarint } from './varint.ts';
import { zigzagEncode32, zigzagEncode64 } from './zigzag.ts';
import { makeTag, WireType } from './wire.ts';

const textEncoder = new TextEncoder();

/** Growable protobuf wire-format writer. */
export class Writer {
  private buf = new Uint8Array(256);
  private len = 0;

  private ensure(n: number): void {
    if (this.len + n <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < this.len + n) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  byte(b: number): this {
    this.ensure(1);
    this.buf[this.len++] = b & 0xff;
    return this;
  }

  raw(src: Uint8Array): this {
    this.ensure(src.length);
    this.buf.set(src, this.len);
    this.len += src.length;
    return this;
  }

  varint(value: bigint | number): this {
    return this.raw(encodeVarint(BigInt(value)));
  }

  tag(fieldNumber: number, wireType: WireType): this {
    return this.varint(makeTag(fieldNumber, wireType));
  }

  uint32(v: number): this {
    return this.varint(v >>> 0);
  }

  int32(v: number): this {
    // Negative int32 is sign-extended to 64 bits on the wire (10-byte varint).
    return this.varint(BigInt.asUintN(64, BigInt(v | 0)));
  }

  uint64(v: bigint | number): this {
    return this.varint(BigInt.asUintN(64, BigInt(v)));
  }

  int64(v: bigint | number): this {
    return this.varint(BigInt.asUintN(64, BigInt(v)));
  }

  sint32(v: number): this {
    return this.varint(zigzagEncode32(v | 0));
  }

  sint64(v: bigint | number): this {
    return this.varint(zigzagEncode64(BigInt(v)));
  }

  bool(v: boolean): this {
    return this.varint(v ? 1 : 0);
  }

  fixed32(v: number): this {
    this.ensure(4);
    new DataView(this.buf.buffer).setUint32(this.len, v >>> 0, true);
    this.len += 4;
    return this;
  }

  sfixed32(v: number): this {
    this.ensure(4);
    new DataView(this.buf.buffer).setInt32(this.len, v | 0, true);
    this.len += 4;
    return this;
  }

  fixed64(v: bigint | number): this {
    this.ensure(8);
    new DataView(this.buf.buffer).setBigUint64(this.len, BigInt.asUintN(64, BigInt(v)), true);
    this.len += 8;
    return this;
  }

  sfixed64(v: bigint | number): this {
    this.ensure(8);
    new DataView(this.buf.buffer).setBigInt64(this.len, BigInt.asIntN(64, BigInt(v)), true);
    this.len += 8;
    return this;
  }

  float(v: number): this {
    this.ensure(4);
    new DataView(this.buf.buffer).setFloat32(this.len, v, true);
    this.len += 4;
    return this;
  }

  double(v: number): this {
    this.ensure(8);
    new DataView(this.buf.buffer).setFloat64(this.len, v, true);
    this.len += 8;
    return this;
  }

  /** Write a length-delimited byte slice. */
  bytes(src: Uint8Array): this {
    this.varint(src.length);
    return this.raw(src);
  }

  string(s: string): this {
    return this.bytes(textEncoder.encode(s));
  }

  finish(): Uint8Array {
    return this.buf.slice(0, this.len);
  }
}
