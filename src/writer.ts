import { encodeVarint, zigzagEncode } from "./varint.ts";
import { WIRE_FIXED32, WIRE_FIXED64, WIRE_LENGTH_DELIMITED, WIRE_VARINT } from "./reader.ts";

/** Growable protobuf wire-format encoder. Zero dependencies. */
export class WireWriter {
  private buf: Uint8Array;
  private len = 0;

  constructor(capacity = 256) {
    this.buf = new Uint8Array(capacity);
  }

  private ensure(n: number): void {
    if (this.len + n <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < this.len + n) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  writeByte(b: number): this {
    this.ensure(1);
    this.buf[this.len++] = b & 0xff;
    return this;
  }

  writeRaw(bytes: Uint8Array): this {
    this.ensure(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
    return this;
  }

  writeVarint(value: bigint | number): this {
    return this.writeRaw(encodeVarint(value));
  }

  writeZigZag(value: bigint | number): this {
    return this.writeVarint(zigzagEncode(value));
  }

  writeTag(field: number, wireType: number): this {
    if (!Number.isInteger(field) || field <= 0) {
      throw new RangeError(`invalid field number ${field}`);
    }
    return this.writeVarint((field << 3) | wireType);
  }

  writeFixed32(value: number): this {
    this.ensure(4);
    const v = value >>> 0;
    this.buf[this.len++] = v & 0xff;
    this.buf[this.len++] = (v >>> 8) & 0xff;
    this.buf[this.len++] = (v >>> 16) & 0xff;
    this.buf[this.len++] = (v >>> 24) & 0xff;
    return this;
  }

  writeFixed64(value: bigint | number): this {
    this.ensure(8);
    let v = BigInt(value) & 0xffff_ffff_ffff_ffffn;
    for (let i = 0; i < 8; i++) {
      this.buf[this.len++] = Number(v & 0xffn);
      v >>= 8n;
    }
    return this;
  }

  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    return this.writeRaw(bytes);
  }

  writeString(s: string): this {
    return this.writeBytes(new TextEncoder().encode(s));
  }

  /** Convenience: tag + scalar writers. */
  varintField(field: number, value: bigint | number): this {
    return this.writeTag(field, WIRE_VARINT).writeVarint(value);
  }

  zigzagField(field: number, value: bigint | number): this {
    return this.writeTag(field, WIRE_VARINT).writeZigZag(value);
  }

  fixed32Field(field: number, value: number): this {
    return this.writeTag(field, WIRE_FIXED32).writeFixed32(value);
  }

  fixed64Field(field: number, value: bigint | number): this {
    return this.writeTag(field, WIRE_FIXED64).writeFixed64(value);
  }

  bytesField(field: number, bytes: Uint8Array): this {
    return this.writeTag(field, WIRE_LENGTH_DELIMITED).writeBytes(bytes);
  }

  stringField(field: number, s: string): this {
    return this.writeTag(field, WIRE_LENGTH_DELIMITED).writeString(s);
  }

  /** Write a nested message produced by `fill`. */
  messageField(field: number, fill: (w: WireWriter) => void): this {
    const inner = new WireWriter();
    fill(inner);
    return this.bytesField(field, inner.finish());
  }

  finish(): Uint8Array {
    return this.buf.slice(0, this.len);
  }

  reset(): void {
    this.len = 0;
  }
}
