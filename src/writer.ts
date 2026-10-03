import { OverflowError } from './errors.ts';

const MASK_64 = 0xffff_ffff_ffff_ffffn;

/**
 * Growable binary encoder for the Protocol Buffers wire format.
 * Owns its scratch buffer; `finish()` returns an exact-length copy,
 * so no internal capacity ever leaks into the returned value.
 */
export class WireWriter {
  private buf = new Uint8Array(64);
  private view = new DataView(this.buf.buffer);
  private len = 0;

  get length(): number {
    return this.len;
  }

  private ensure(extra: number): void {
    const need = this.len + extra;
    if (need <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < need) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
    this.view = new DataView(next.buffer);
  }

  byte(b: number): this {
    this.ensure(1);
    this.buf[this.len++] = b & 0xff;
    return this;
  }

  /** Unsigned LEB128 varint: 7 payload bits per byte, MSB = continuation. */
  varint(value: bigint | number): this {
    let v = BigInt.asUintN(64, typeof value === 'number' ? BigInt(value) : value);
    this.ensure(10);
    while (v > 0x7fn) {
      this.buf[this.len++] = Number(v & 0x7fn) | 0x80;
      v >>= 7n;
    }
    this.buf[this.len++] = Number(v);
    return this;
  }

  uint32(v: number): this {
    return this.varint(v >>> 0);
  }

  /** int32 negatives are sign-extended to 64 bits, per the protobuf spec. */
  int32(v: number): this {
    return this.varint(BigInt.asUintN(64, BigInt(v | 0)));
  }

  uint64(v: bigint): this {
    return this.varint(BigInt.asUintN(64, v));
  }

  int64(v: bigint): this {
    return this.varint(BigInt.asUintN(64, v));
  }

  /** ZigZag: map signed -> unsigned so small magnitudes stay small. */
  zigzag32(v: number): this {
    return this.varint(((v << 1) ^ (v >> 31)) >>> 0);
  }

  zigzag64(v: bigint): this {
    return this.varint(BigInt.asUintN(64, (v << 1n) ^ (v >> 63n)));
  }

  fixed32(v: number): this {
    this.ensure(4);
    this.view.setUint32(this.len, v >>> 0, true);
    this.len += 4;
    return this;
  }

  sfixed32(v: number): this {
    this.ensure(4);
    this.view.setInt32(this.len, v | 0, true);
    this.len += 4;
    return this;
  }

  float(v: number): this {
    this.ensure(4);
    this.view.setFloat32(this.len, v, true);
    this.len += 4;
    return this;
  }

  fixed64(v: bigint): this {
    this.ensure(8);
    this.view.setBigUint64(this.len, BigInt.asUintN(64, v), true);
    this.len += 8;
    return this;
  }

  sfixed64(v: bigint): this {
    this.ensure(8);
    this.view.setBigInt64(this.len, BigInt.asIntN(64, v), true);
    this.len += 8;
    return this;
  }

  double(v: number): this {
    this.ensure(8);
    this.view.setFloat64(this.len, v, true);
    this.len += 8;
    return this;
  }

  /** Raw bytes without a length prefix (caller frames them). */
  raw(bytes: Uint8Array): this {
    this.ensure(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
    return this;
  }

  /** Length-delimited field body. */
  bytes(bytes: Uint8Array): this {
    return this.varint(bytes.length).raw(bytes);
  }

  string(s: string): this {
    return this.bytes(new TextEncoder().encode(s));
  }

  /** Field tag = (fieldNumber << 3) | wireType. */
  tag(fieldNo: number, wireType: number): this {
    if (fieldNo < 1 || fieldNo > 0x1fffffff) {
      throw new OverflowError(`field number ${fieldNo} out of range [1, 2^29-1]`, this.len);
    }
    return this.varint((fieldNo << 3) | wireType);
  }

  /** Exact-length snapshot; the writer keeps no reference to the result. */
  finish(): Uint8Array {
    return this.buf.slice(0, this.len);
  }
}

export { MASK_64 };
