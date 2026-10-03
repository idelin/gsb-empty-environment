/**
 * Hand-rolled Protocol Buffers binary wire-format primitives.
 *
 * Zero dependencies. Every read is bounds-checked: a truncated or malformed
 * stream always raises DecodeError (carrying the byte offset) and never
 * reads out of bounds.
 */

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;

export type WireType = (typeof WireType)[keyof typeof WireType];

export class DecodeError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (offset ${offset})`);
    this.name = 'DecodeError';
    this.offset = offset;
  }
}

export class EncodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'EncodeError';
  }
}

/* ------------------------------------------------------------------ */
/* ZigZag                                                              */
/* ------------------------------------------------------------------ */

export function encodeZigZag32(value: number): number {
  return ((value << 1) ^ (value >> 31)) >>> 0;
}

export function decodeZigZag32(value: number): number {
  return (value >>> 1) ^ -(value & 1);
}

export function encodeZigZag64(value: bigint): bigint {
  return BigInt.asUintN(64, (value << 1n) ^ (value >> 63n));
}

export function decodeZigZag64(value: bigint): bigint {
  return BigInt.asIntN(64, (value >> 1n) ^ -(value & 1n));
}

/* ------------------------------------------------------------------ */
/* Reader                                                              */
/* ------------------------------------------------------------------ */

export interface Tag {
  field: number;
  wireType: WireType;
}

const MAX_VARINT32 = 0xffff_ffffn;

export class WireReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly limit: number;
  private readonly baseOffset: number;

  constructor(buf: Uint8Array, offset = 0, length = buf.length - offset) {
    if (!Number.isInteger(offset) || !Number.isInteger(length) ||
        offset < 0 || length < 0 || offset + length > buf.length) {
      throw new DecodeError('invalid reader view bounds', offset);
    }
    this.buf = buf;
    this.pos = offset;
    this.limit = offset + length;
    this.baseOffset = offset;
  }

  /** Bytes consumed relative to this reader's view. */
  get offset(): number {
    return this.pos - this.baseOffset;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.limit;
  }

  private need(count: number, what: string): void {
    if (this.remaining < count) {
      throw new DecodeError(
        `truncated ${what}: need ${count} byte(s), only ${this.remaining} left`,
        this.offset,
      );
    }
  }

  readByte(what = 'byte'): number {
    this.need(1, what);
    return this.buf[this.pos++]!;
  }

  /** Reads a base-128 varint of at most 10 bytes as an unsigned 64-bit value. */
  readVarint64(): bigint {
    let result = 0n;
    for (let shift = 0n; shift < 64n; shift += 7n) {
      const byte = this.readByte('varint');
      if (shift === 63n) {
        // 10th byte: a single payload bit remains; anything else overflows.
        if (byte > 1) {
          throw new DecodeError('varint overflows 64 bits', this.offset - 1);
        }
        return result | (BigInt(byte) << shift);
      }
      result |= BigInt(byte & 0x7f) << shift;
      if ((byte & 0x80) === 0) return result;
    }
    throw new DecodeError('varint overflows 64 bits', this.offset);
  }

  /** Varint that must fit in an unsigned 32-bit range (tags, lengths, uint32). */
  readVarint32(): number {
    const start = this.offset;
    const value = this.readVarint64();
    if (value > MAX_VARINT32) {
      throw new DecodeError('varint exceeds 32 bits', start);
    }
    return Number(value);
  }

  readInt32(): number {
    // Negative int32 values are sign-extended to 10-byte varints on the wire.
    return Number(BigInt.asIntN(32, this.readVarint64()));
  }

  readInt64(): bigint {
    return BigInt.asIntN(64, this.readVarint64());
  }

  readUint32(): number {
    return this.readVarint32();
  }

  readUint64(): bigint {
    return this.readVarint64();
  }

  readBool(): boolean {
    return this.readVarint64() !== 0n;
  }

  readSint32(): number {
    return decodeZigZag32(Number(BigInt.asUintN(32, this.readVarint64())));
  }

  readSint64(): bigint {
    return decodeZigZag64(this.readVarint64());
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const b = this.buf;
    const p = this.pos;
    this.pos += 4;
    return (b[p]! | (b[p + 1]! << 8) | (b[p + 2]! << 16) | (b[p + 3]! << 24)) >>> 0;
  }

  readSfixed32(): number {
    return this.readFixed32() | 0;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    let result = 0n;
    for (let i = 0; i < 8; i++) {
      result |= BigInt(this.buf[this.pos + i]!) << BigInt(i * 8);
    }
    this.pos += 8;
    return result;
  }

  readSfixed64(): bigint {
    return BigInt.asIntN(64, this.readFixed64());
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

  /** Bounds-checked copy of `length` bytes. */
  readBytes(length: number): Uint8Array {
    if (!Number.isInteger(length) || length < 0) {
      throw new DecodeError(`invalid length ${length}`, this.offset);
    }
    this.need(length, 'length-delimited payload');
    const out = this.buf.slice(this.pos, this.pos + length);
    this.pos += length;
    return out;
  }

  readLengthDelimited(): Uint8Array {
    return this.readBytes(this.readVarint32());
  }

  /**
   * Returns a sub-reader scoped to the next length-delimited payload
   * (nested message). The parent advances past the payload.
   */
  readSubReader(): WireReader {
    const length = this.readVarint32();
    this.need(length, 'nested message');
    const sub = new WireReader(this.buf, this.pos, length);
    this.pos += length;
    return sub;
  }

  /** Reads a field tag, or returns null when the view is exhausted. */
  readTag(): Tag | null {
    if (this.done) return null;
    const start = this.offset;
    const value = this.readVarint32();
    const wireType = value & 0x7;
    const field = value >>> 3;
    if (field === 0) {
      throw new DecodeError('invalid field number 0', start);
    }
    if (wireType !== WireType.Varint && wireType !== WireType.Fixed64 &&
        wireType !== WireType.LengthDelimited && wireType !== WireType.Fixed32) {
      throw new DecodeError(`unsupported wire type ${wireType}`, start);
    }
    return { field, wireType: wireType as WireType };
  }

  /** Skips one field of the given wire type, with full bounds checking. */
  skipField(wireType: WireType): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint64();
        return;
      case WireType.Fixed64:
        this.need(8, 'fixed64');
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const length = this.readVarint32();
        this.need(length, 'length-delimited payload');
        this.pos += length;
        return;
      }
      case WireType.Fixed32:
        this.need(4, 'fixed32');
        this.pos += 4;
        return;
      default:
        throw new DecodeError(`unsupported wire type ${wireType}`, this.offset);
    }
  }
}

/* ------------------------------------------------------------------ */
/* Writer                                                              */
/* ------------------------------------------------------------------ */

export class WireWriter {
  private readonly bytes: number[] = [];

  get length(): number {
    return this.bytes.length;
  }

  writeByte(byte: number): this {
    this.bytes.push(byte & 0xff);
    return this;
  }

  /** Encodes an unsigned 64-bit value as a base-128 varint (7-bit groups). */
  writeVarint64(value: bigint): this {
    let v = BigInt.asUintN(64, value);
    do {
      const byte = Number(v & 0x7fn);
      v >>= 7n;
      this.bytes.push(v === 0n ? byte : byte | 0x80);
    } while (v !== 0n);
    return this;
  }

  writeVarint32(value: number): this {
    if (!Number.isInteger(value) || value < 0 || value > 0xffffffff) {
      throw new EncodeError(`uint32 out of range: ${value}`);
    }
    return this.writeVarint64(BigInt(value >>> 0));
  }

  writeInt32(value: number): this {
    if (!Number.isInteger(value) || value < -0x80000000 || value > 0x7fffffff) {
      throw new EncodeError(`int32 out of range: ${value}`);
    }
    return this.writeVarint64(BigInt(value));
  }

  writeInt64(value: bigint): this {
    return this.writeVarint64(BigInt.asIntN(64, value));
  }

  writeUint32(value: number): this {
    return this.writeVarint32(value);
  }

  writeUint64(value: bigint): this {
    return this.writeVarint64(BigInt.asUintN(64, value));
  }

  writeBool(value: boolean): this {
    return this.writeVarint64(value ? 1n : 0n);
  }

  writeSint32(value: number): this {
    return this.writeVarint64(BigInt(encodeZigZag32(value)));
  }

  writeSint64(value: bigint): this {
    return this.writeVarint64(encodeZigZag64(value));
  }

  writeTag(field: number, wireType: WireType): this {
    if (!Number.isInteger(field) || field < 1 || field > 0x1fffffff) {
      throw new EncodeError(`invalid field number: ${field}`);
    }
    return this.writeVarint32(((field << 3) | wireType) >>> 0);
  }

  writeFixed32(value: number): this {
    const v = value >>> 0;
    this.bytes.push(v & 0xff, (v >>> 8) & 0xff, (v >>> 16) & 0xff, (v >>> 24) & 0xff);
    return this;
  }

  writeSfixed32(value: number): this {
    return this.writeFixed32(value >>> 0);
  }

  writeFixed64(value: bigint): this {
    const v = BigInt.asUintN(64, value);
    for (let i = 0n; i < 8n; i++) {
      this.bytes.push(Number((v >> (i * 8n)) & 0xffn));
    }
    return this;
  }

  writeSfixed64(value: bigint): this {
    return this.writeFixed64(BigInt.asUintN(64, value));
  }

  writeFloat(value: number): this {
    const tmp = new DataView(new ArrayBuffer(4));
    tmp.setFloat32(0, value, true);
    for (let i = 0; i < 4; i++) this.bytes.push(tmp.getUint8(i));
    return this;
  }

  writeDouble(value: number): this {
    const tmp = new DataView(new ArrayBuffer(8));
    tmp.setFloat64(0, value, true);
    for (let i = 0; i < 8; i++) this.bytes.push(tmp.getUint8(i));
    return this;
  }

  writeBytes(data: Uint8Array): this {
    for (const byte of data) this.bytes.push(byte);
    return this;
  }

  writeLengthDelimited(data: Uint8Array): this {
    this.writeVarint32(data.length);
    return this.writeBytes(data);
  }

  writeString(value: string): this {
    return this.writeLengthDelimited(new TextEncoder().encode(value));
  }

  finish(): Uint8Array {
    return Uint8Array.from(this.bytes);
  }
}
