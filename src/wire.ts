/**
 * Low-level Protocol Buffers wire-format primitives.
 * Zero dependencies; all multi-byte integers are handled via BigInt so the
 * full unsigned/signed 64-bit range is preserved exactly.
 */

export const WIRE_VARINT = 0;
export const WIRE_FIXED64 = 1;
export const WIRE_LENGTH_DELIMITED = 2;
export const WIRE_FIXED32 = 5;

export type WireType =
  | typeof WIRE_VARINT
  | typeof WIRE_FIXED64
  | typeof WIRE_LENGTH_DELIMITED
  | typeof WIRE_FIXED32;

/** Raised whenever a read would cross the end of the buffer (safe bounds guard). */
export class TruncationError extends Error {
  readonly offset: number;
  readonly needed: number;
  readonly available: number;
  readonly context: string;

  constructor(offset: number, needed: number, available: number, context: string) {
    super(
      `truncated buffer at offset ${offset}: need ${needed} byte(s), ` +
        `only ${available} available (${context})`,
    );
    this.name = 'TruncationError';
    this.offset = offset;
    this.needed = needed;
    this.available = available;
    this.context = context;
  }
}

/** Raised for structurally invalid wire data (overlong varint, bad wire type, ...). */
export class MalformedWireError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'MalformedWireError';
  }
}

/** ZigZag-encode a signed 64-bit integer into an unsigned 64-bit pattern. */
export function zigZagEncode(value: bigint): bigint {
  return BigInt.asUintN(64, (value << 1n) ^ (value >> 63n));
}

/** Inverse of {@link zigZagEncode}. */
export function zigZagDecode(value: bigint): bigint {
  return (value >> 1n) ^ (-(value & 1n));
}

/** Encode an unsigned (or two's-complement signed) 64-bit integer as a varint. */
export function encodeVarint(value: bigint | number): Uint8Array {
  let v = BigInt(value);
  if (v < 0n) v = BigInt.asUintN(64, v);
  const out: number[] = [];
  do {
    let byte = Number(v & 0x7fn);
    v >>= 7n;
    if (v !== 0n) byte |= 0x80;
    out.push(byte);
  } while (v !== 0n);
  return Uint8Array.from(out);
}

export class WireReader {
  private offset = 0;
  private readonly buf: Uint8Array;

  constructor(buf: Uint8Array) {
    this.buf = buf;
  }

  get pos(): number {
    return this.offset;
  }

  get remaining(): number {
    return this.buf.length - this.offset;
  }

  get eof(): boolean {
    if (this.offset > this.buf.length) {
      throw new TruncationError(this.buf.length, this.offset - this.buf.length, 0, 'reader overshoot');
    }
    return this.offset >= this.buf.length;
  }

  private require(count: number, context: string): void {
    if (this.remaining < count) {
      throw new TruncationError(this.offset, count, this.remaining, context);
    }
  }

  /** Read a base-128 varint (max 10 bytes = 64 bits). */
  readVarint(context = 'varint'): bigint {
    let result = 0n;
    let shift = 0n;
    for (let i = 0; i < 10; i++) {
      this.require(1, context);
      const byte = this.buf[this.offset++];
      result |= BigInt(byte & 0x7f) << shift;
      if ((byte & 0x80) === 0) {
        if (i === 9 && byte > 1) {
          throw new MalformedWireError(`varint at offset ${this.offset - 10} overflows 64 bits`);
        }
        return result;
      }
      shift += 7n;
    }
    throw new MalformedWireError(`varint at offset ${this.offset - 10} exceeds 10 bytes`);
  }

  readFixed32(context = 'fixed32'): number {
    this.require(4, context);
    const b = this.buf;
    const value =
      (b[this.offset] | (b[this.offset + 1] << 8) | (b[this.offset + 2] << 16) | (b[this.offset + 3] << 24)) >>>
      0;
    this.offset += 4;
    return value;
  }

  readSFixed32(context = 'sfixed32'): number {
    return this.readFixed32(context) | 0;
  }

  readFixed64(context = 'fixed64'): bigint {
    const lo = BigInt(this.readFixed32(context));
    const hi = BigInt(this.readFixed32(context));
    return (hi << 32n) | lo;
  }

  readSFixed64(context = 'sfixed64'): bigint {
    return BigInt.asIntN(64, this.readFixed64(context));
  }

  readFloat(context = 'float'): number {
    this.require(4, context);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.offset, 4);
    this.offset += 4;
    return view.getFloat32(0, true);
  }

  readDouble(context = 'double'): number {
    this.require(8, context);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.offset, 8);
    this.offset += 8;
    return view.getFloat64(0, true);
  }

  /** Read a varint length prefix followed by exactly that many bytes. */
  readBytes(context = 'length-delimited'): Uint8Array {
    const length = Number(this.readVarint(`${context} length`));
    if (!Number.isSafeInteger(length) || length < 0) {
      throw new MalformedWireError(`invalid length-delimited size ${length} at offset ${this.offset}`);
    }
    this.require(length, `${context} payload`);
    const out = this.buf.subarray(this.offset, this.offset + length);
    this.offset += length;
    return out;
  }

  /** Skip a field of the given wire type (used for unknown fields). */
  skip(wireType: number): void {
    switch (wireType) {
      case WIRE_VARINT:
        this.readVarint('skipped varint');
        return;
      case WIRE_FIXED64:
        this.require(8, 'skipped fixed64');
        this.offset += 8;
        return;
      case WIRE_LENGTH_DELIMITED:
        this.readBytes('skipped length-delimited');
        return;
      case WIRE_FIXED32:
        this.require(4, 'skipped fixed32');
        this.offset += 4;
        return;
      default:
        throw new MalformedWireError(`unsupported wire type ${wireType} at offset ${this.offset}`);
    }
  }
}

export class WireWriter {
  private readonly chunks: Uint8Array[] = [];
  private length = 0;

  get byteLength(): number {
    return this.length;
  }

  private push(bytes: Uint8Array): void {
    this.chunks.push(bytes);
    this.length += bytes.length;
  }

  writeVarint(value: bigint | number): this {
    this.push(encodeVarint(value));
    return this;
  }

  writeTag(fieldNumber: number, wireType: WireType): this {
    if (!Number.isInteger(fieldNumber) || fieldNumber <= 0) {
      throw new MalformedWireError(`invalid field number ${fieldNumber}`);
    }
    return this.writeVarint((BigInt(fieldNumber) << 3n) | BigInt(wireType));
  }

  writeFixed32(value: number): this {
    const bytes = new Uint8Array(4);
    bytes[0] = value & 0xff;
    bytes[1] = (value >>> 8) & 0xff;
    bytes[2] = (value >>> 16) & 0xff;
    bytes[3] = (value >>> 24) & 0xff;
    this.push(bytes);
    return this;
  }

  writeFixed64(value: bigint | number): this {
    const v = BigInt.asUintN(64, BigInt(value));
    this.writeFixed32(Number(v & 0xffffffffn));
    this.writeFixed32(Number(v >> 32n));
    return this;
  }

  writeFloat(value: number): this {
    const bytes = new Uint8Array(4);
    new DataView(bytes.buffer).setFloat32(0, value, true);
    this.push(bytes);
    return this;
  }

  writeDouble(value: number): this {
    const bytes = new Uint8Array(8);
    new DataView(bytes.buffer).setFloat64(0, value, true);
    this.push(bytes);
    return this;
  }

  /** Write raw bytes without a length prefix. */
  writeRawBytes(bytes: Uint8Array): this {
    this.push(bytes);
    return this;
  }

  /** Write a varint length prefix followed by the payload. */
  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    this.push(bytes);
    return this;
  }

  finish(): Uint8Array {
    const out = new Uint8Array(this.length);
    let offset = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, offset);
      offset += chunk.length;
    }
    return out;
  }
}
