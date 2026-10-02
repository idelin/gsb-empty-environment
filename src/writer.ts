/**
 * Reusable protobuf wire-format writer backed by a single growable buffer.
 * reset() keeps the backing store, so steady-state encoding allocates nothing.
 */
import { encodeVarintInto, zigzagEncode32, zigzagEncode64 } from "./varint.ts";
import {
  WIRE_FIXED32,
  WIRE_FIXED64,
  WIRE_LENGTH_DELIMITED,
  WIRE_VARINT,
} from "./reader.ts";

const textEncoder = new TextEncoder();

export class WireWriter {
  private buf: Uint8Array;
  pos: number;

  constructor(initialCapacity = 256) {
    this.buf = new Uint8Array(initialCapacity);
    this.pos = 0;
  }

  reset(): this {
    this.pos = 0;
    return this;
  }

  get length(): number {
    return this.pos;
  }

  private ensure(n: number): void {
    if (this.pos + n <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < this.pos + n) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.pos));
    this.buf = next;
  }

  writeVarint(value: number | bigint): this {
    this.ensure(10);
    this.pos = encodeVarintInto(value, this.buf, this.pos);
    return this;
  }

  writeZigZag32(n: number): this {
    return this.writeVarint(zigzagEncode32(n) >>> 0);
  }

  writeZigZag64(n: bigint): this {
    return this.writeVarint(BigInt.asUintN(64, zigzagEncode64(n)));
  }

  writeFixed32(v: number): this {
    this.ensure(4);
    const b = this.buf;
    const p = this.pos;
    b[p] = v & 0xff;
    b[p + 1] = (v >>> 8) & 0xff;
    b[p + 2] = (v >>> 16) & 0xff;
    b[p + 3] = (v >>> 24) & 0xff;
    this.pos = p + 4;
    return this;
  }

  writeFixed64(v: bigint): this {
    const u = BigInt.asUintN(64, v);
    this.writeFixed32(Number(u & 0xffffffffn));
    this.writeFixed32(Number((u >> 32n) & 0xffffffffn));
    return this;
  }

  writeTag(field: number, wireType: number): this {
    return this.writeVarint((field << 3) | wireType);
  }

  writeBytes(data: Uint8Array): this {
    this.writeVarint(data.length);
    this.ensure(data.length);
    this.buf.set(data, this.pos);
    this.pos += data.length;
    return this;
  }

  writeString(s: string): this {
    return this.writeBytes(textEncoder.encode(s));
  }

  /** Convenience: tag + varint field. */
  fieldVarint(field: number, value: number | bigint): this {
    return this.writeTag(field, WIRE_VARINT).writeVarint(value);
  }

  fieldZigZag32(field: number, value: number): this {
    return this.writeTag(field, WIRE_VARINT).writeZigZag32(value);
  }

  fieldFixed32(field: number, value: number): this {
    return this.writeTag(field, WIRE_FIXED32).writeFixed32(value);
  }

  fieldFixed64(field: number, value: bigint): this {
    return this.writeTag(field, WIRE_FIXED64).writeFixed64(value);
  }

  fieldBytes(field: number, data: Uint8Array): this {
    return this.writeTag(field, WIRE_LENGTH_DELIMITED).writeBytes(data);
  }

  fieldString(field: number, s: string): this {
    return this.writeTag(field, WIRE_LENGTH_DELIMITED).writeString(s);
  }

  /** Copy out the encoded bytes. */
  finish(): Uint8Array {
    return this.buf.slice(0, this.pos);
  }

  /** Zero-copy view of the encoded bytes (invalidated by next reset/write). */
  view(): Uint8Array {
    return this.buf.subarray(0, this.pos);
  }
}
