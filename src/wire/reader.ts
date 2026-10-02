import { truncation, WireError } from "./errors.ts";

/** Protobuf wire types (lower 3 bits of a tag). */
export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  StartGroup: 3,
  EndGroup: 4,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

const MAX_VARINT_BYTES = 10; // ceil(64 / 7)

/**
 * Bounds-checked, zero-copy reader for the Protocol Buffers binary wire
 * format. Every accessor validates the remaining length before touching the
 * buffer, so a truncated field always raises a `WireError` (kind
 * "truncation") instead of reading out of bounds.
 */
export class WireReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly end: number;

  constructor(buf: Uint8Array, offset = 0, length?: number) {
    if (offset < 0 || offset > buf.length) {
      throw new WireError("truncation", offset, `offset ${offset} outside buffer of length ${buf.length}`);
    }
    const end = length === undefined ? buf.length : offset + length;
    if (end > buf.length) {
      throw truncation(offset, length!, buf.length - offset);
    }
    this.buf = buf;
    this.pos = offset;
    this.end = end;
  }

  /** Absolute offset of the read cursor inside the underlying buffer. */
  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.end;
  }

  private need(n: number): void {
    if (this.remaining < n) throw truncation(this.pos, n, this.remaining);
  }

  /** Read a base-128 varint (7 payload bits per byte) as an unsigned 64-bit value. */
  readVarint(): bigint {
    let result = 0n;
    for (let i = 0; i < MAX_VARINT_BYTES; i++) {
      this.need(1);
      const byte = this.buf[this.pos++];
      if (i === MAX_VARINT_BYTES - 1 && byte > 0x01) {
        throw new WireError("overflow", this.pos - 1, "varint exceeds 64 bits");
      }
      result |= BigInt(byte & 0x7f) << BigInt(7 * i);
      if ((byte & 0x80) === 0) return result;
    }
    throw new WireError("overflow", this.pos, "varint longer than 10 bytes");
  }

  /** Read a varint that must fit in an unsigned 32-bit integer. */
  readVarint32(): number {
    const value = this.readVarint();
    if (value > 0xffff_ffffn) {
      throw new WireError("overflow", this.pos, `varint ${value} does not fit in uint32`);
    }
    return Number(value);
  }

  /** Read a varint reinterpreted as a signed 32-bit integer (int32/enum). */
  readInt32(): number {
    const value = this.readVarint();
    return Number(BigInt.asIntN(32, value));
  }

  readBool(): boolean {
    return this.readVarint() !== 0n;
  }

  /** ZigZag-decoded signed 32-bit integer (sint32). */
  readZigZag32(): number {
    const n = this.readVarint32();
    return (n >>> 1) ^ -(n & 1);
  }

  /** ZigZag-decoded signed 64-bit integer (sint64). */
  readZigZag64(): bigint {
    const n = this.readVarint();
    return (n >> 1n) ^ -(n & 1n);
  }

  readFixed32(): number {
    this.need(4);
    const b = this.buf;
    const p = this.pos;
    this.pos += 4;
    return (b[p] | (b[p + 1] << 8) | (b[p + 2] << 16) | (b[p + 3] << 24)) >>> 0;
  }

  readSFixed32(): number {
    return this.readFixed32() | 0;
  }

  readFixed64(): bigint {
    this.need(8);
    const b = this.buf;
    const p = this.pos;
    this.pos += 8;
    let lo = 0n;
    let hi = 0n;
    for (let i = 0; i < 4; i++) lo |= BigInt(b[p + i]) << BigInt(8 * i);
    for (let i = 0; i < 4; i++) hi |= BigInt(b[p + 4 + i]) << BigInt(8 * i);
    return (hi << 32n) | lo;
  }

  readSFixed64(): bigint {
    return BigInt.asIntN(64, this.readFixed64());
  }

  readFloat(): number {
    this.need(4);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4);
    this.pos += 4;
    return view.getFloat32(0, true);
  }

  readDouble(): number {
    this.need(8);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8);
    this.pos += 8;
    return view.getFloat64(0, true);
  }

  /** Read a length-delimited byte slice (zero-copy view into the source). */
  readBytes(): Uint8Array {
    const len = this.readVarint32();
    this.need(len);
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return new TextDecoder().decode(this.readBytes());
  }

  /** Read a field tag: (fieldNumber << 3) | wireType. Returns null at end of input. */
  readTag(): { field: number; wireType: WireTypeValue } | null {
    if (this.done) return null;
    const start = this.pos;
    const tag = this.readVarint32();
    const wireType = (tag & 0x7) as WireTypeValue;
    const field = tag >>> 3;
    if (field === 0) {
      throw new WireError("invalid-tag", start, "field number 0 is not allowed");
    }
    if (
      wireType !== WireType.Varint &&
      wireType !== WireType.Fixed64 &&
      wireType !== WireType.LengthDelimited &&
      wireType !== WireType.Fixed32
    ) {
      throw new WireError("invalid-wire-type", start, `unsupported wire type ${wireType}`);
    }
    return { field, wireType };
  }

  /** Create a sub-reader bounded to the next length-delimited payload. */
  readSubReader(): WireReader {
    const len = this.readVarint32();
    this.need(len);
    const sub = new WireReader(this.buf, this.pos, len);
    this.pos += len;
    return sub;
  }

  /** Skip a field of the given wire type, with full bounds checking. */
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
        throw new WireError("invalid-wire-type", this.pos, `cannot skip wire type ${wireType}`);
    }
  }
}
