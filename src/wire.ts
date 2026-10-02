/**
 * Zero-dependency Protocol Buffers binary wire-format primitives.
 * Hand-rolled parser: Varint (7-bit), ZigZag, Fixed32/64, Length-delimited.
 * Every read is bounds-checked; truncation raises DecodeError with a stable code.
 */

export const WireType = {
  VARINT: 0,
  I64: 1,
  LEN: 2,
  SGROUP: 3,
  EGROUP: 4,
  I32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export const ErrorCode = {
  TRUNCATED: 'ERR_TRUNCATED',
  INVALID_VARINT: 'ERR_INVALID_VARINT',
  INVALID_WIRE_TYPE: 'ERR_INVALID_WIRE_TYPE',
  INVALID_TAG: 'ERR_INVALID_TAG',
  NEGATIVE_LENGTH: 'ERR_NEGATIVE_LENGTH',
  NESTING_TOO_DEEP: 'ERR_NESTING_TOO_DEEP',
  GROUP_MISMATCH: 'ERR_GROUP_MISMATCH',
} as const;
export type ErrorCodeValue = (typeof ErrorCode)[keyof typeof ErrorCode];

export class DecodeError extends Error {
  readonly code: ErrorCodeValue;
  readonly offset: number;
  constructor(code: ErrorCodeValue, offset: number, detail: string) {
    super(`[${code}] @offset ${offset}: ${detail}`);
    this.name = 'DecodeError';
    this.code = code;
    this.offset = offset;
  }
}

const MAX_VARINT_BYTES = 10; // ceil(64 / 7)
const TWO_POW_32 = 0x1_0000_0000n;
const MASK_64 = 0xffff_ffff_ffff_ffffn;

export class WireReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly end: number;

  constructor(buf: Uint8Array, start = 0, end = buf.length) {
    if (start < 0 || end > buf.length || start > end) {
      throw new DecodeError(ErrorCode.TRUNCATED, start, 'reader window out of bounds');
    }
    this.buf = buf;
    this.pos = start;
    this.end = end;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.end;
  }

  private need(n: number, what: string): void {
    if (this.remaining < n) {
      throw new DecodeError(
        ErrorCode.TRUNCATED,
        this.pos,
        `${what} needs ${n} byte(s), only ${this.remaining} remain`,
      );
    }
  }

  /** Read one raw byte. */
  readByte(): number {
    this.need(1, 'byte');
    return this.buf[this.pos++];
  }

  /**
   * Read an unsigned varint of up to 64 bits.
   * Returns bigint so the full uint64 range is representable.
   */
  readVarint64(): bigint {
    const start = this.pos;
    let result = 0n;
    let shift = 0n;
    for (let i = 0; i < MAX_VARINT_BYTES; i++) {
      if (this.pos >= this.end) {
        throw new DecodeError(ErrorCode.TRUNCATED, start, 'unterminated varint');
      }
      const b = this.buf[this.pos++];
      if (i === 9 && b > 0x01) {
        // 10th byte may only carry the single top bit of a 64-bit value.
        throw new DecodeError(ErrorCode.INVALID_VARINT, start, 'varint exceeds 64 bits');
      }
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) {
        return result;
      }
      shift += 7n;
    }
    throw new DecodeError(ErrorCode.INVALID_VARINT, start, 'varint longer than 10 bytes');
  }

  /** Read an unsigned varint that must fit in uint32. */
  readUint32(): number {
    const v = this.readVarint64();
    if (v > 0xffff_ffffn) {
      throw new DecodeError(ErrorCode.INVALID_VARINT, this.pos, 'varint does not fit in uint32');
    }
    return Number(v);
  }

  /** Read a varint that must fit in a safe JS integer (<= 2^53-1). */
  readVarint(): number {
    const v = this.readVarint64();
    if (v > BigInt(Number.MAX_SAFE_INTEGER)) {
      throw new DecodeError(ErrorCode.INVALID_VARINT, this.pos, 'varint exceeds safe integer range');
    }
    return Number(v);
  }

  /** int32/int64 enums etc.: varint reinterpreted as signed two's complement. */
  readInt32(): number {
    const v = this.readVarint64();
    return Number(BigInt.asIntN(32, v));
  }

  readInt64(): bigint {
    return BigInt.asIntN(64, this.readVarint64());
  }

  readBool(): boolean {
    return this.readVarint64() !== 0n;
  }

  /** ZigZag: (n >> 1) ^ -(n & 1) */
  readSInt32(): number {
    const v = this.readUint32();
    return (v >>> 1) ^ -(v & 1);
  }

  readSInt64(): bigint {
    const v = this.readVarint64();
    return BigInt.asIntN(64, (v >> 1n) ^ -(v & 1n));
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const p = this.pos;
    const v =
      (this.buf[p] |
        (this.buf[p + 1] << 8) |
        (this.buf[p + 2] << 16) |
        (this.buf[p + 3] << 24)) >>>
      0;
    this.pos += 4;
    return v;
  }

  readSFixed32(): number {
    return this.readFixed32() | 0;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    const lo = BigInt(this.readFixed32());
    const hi = BigInt(this.readFixed32());
    return (hi << 32n) | lo;
  }

  readSFixed64(): bigint {
    return BigInt.asIntN(64, this.readFixed64());
  }

  readFloat(): number {
    this.need(4, 'float');
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4);
    const v = view.getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  readDouble(): number {
    this.need(8, 'double');
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8);
    const v = view.getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  /** Length-delimited raw bytes. Returns a copy so callers can't alias the source buffer. */
  readBytes(): Uint8Array {
    const start = this.pos;
    const lenBig = this.readVarint64();
    if (lenBig > BigInt(this.remaining)) {
      throw new DecodeError(
        ErrorCode.TRUNCATED,
        start,
        `length-delimited field declares ${lenBig} byte(s), only ${this.remaining} remain`,
      );
    }
    const len = Number(lenBig);
    const out = this.buf.slice(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return new TextDecoder('utf-8', { fatal: true }).decode(this.readBytes());
  }

  /** Field tag: varint of (fieldNumber << 3) | wireType. */
  readTag(): { field: number; wire: WireTypeValue } {
    const start = this.pos;
    const key = this.readVarint64();
    const wire = Number(key & 0x7n);
    const field = Number(key >> 3n);
    if (field === 0 || field > 0x1fff_ffff) {
      throw new DecodeError(ErrorCode.INVALID_TAG, start, `invalid field number ${field}`);
    }
    if (wire !== 0 && wire !== 1 && wire !== 2 && wire !== 3 && wire !== 4 && wire !== 5) {
      throw new DecodeError(ErrorCode.INVALID_WIRE_TYPE, start, `invalid wire type ${wire}`);
    }
    return { field, wire: wire as WireTypeValue };
  }

  /** Skip a field of the given wire type (unknown-field tolerance). */
  skipField(wire: WireTypeValue, depth = 0): void {
    switch (wire) {
      case WireType.VARINT:
        this.readVarint64();
        return;
      case WireType.I64:
        this.need(8, 'fixed64 skip');
        this.pos += 8;
        return;
      case WireType.LEN: {
        const start = this.pos;
        const len = this.readVarint64();
        if (len > BigInt(this.remaining)) {
          throw new DecodeError(ErrorCode.TRUNCATED, start, 'length-delimited skip overruns buffer');
        }
        this.pos += Number(len);
        return;
      }
      case WireType.I32:
        this.need(4, 'fixed32 skip');
        this.pos += 4;
        return;
      case WireType.SGROUP: {
        if (depth > 100) {
          throw new DecodeError(ErrorCode.NESTING_TOO_DEEP, this.pos, 'group nesting too deep');
        }
        for (;;) {
          const tag = this.readTag();
          if (tag.wire === WireType.EGROUP) return;
          this.skipField(tag.wire, depth + 1);
        }
      }
      case WireType.EGROUP:
        throw new DecodeError(ErrorCode.GROUP_MISMATCH, this.pos, 'unmatched end-group');
      default:
        throw new DecodeError(ErrorCode.INVALID_WIRE_TYPE, this.pos, `cannot skip wire type ${wire}`);
    }
  }
}

/* ------------------------------ Encoder ------------------------------ */

export class WireWriter {
  private chunks: number[] = [];

  get length(): number {
    return this.chunks.length;
  }

  byte(b: number): this {
    this.chunks.push(b & 0xff);
    return this;
  }

  varint(value: number | bigint): this {
    let v = BigInt.asUintN(64, BigInt(value));
    do {
      let b = Number(v & 0x7fn);
      v >>= 7n;
      if (v !== 0n) b |= 0x80;
      this.chunks.push(b);
    } while (v !== 0n);
    return this;
  }

  zigZag32(value: number): this {
    return this.varint(((value << 1) ^ (value >> 31)) >>> 0);
  }

  zigZag64(value: bigint): this {
    return this.varint(BigInt.asUintN(64, (value << 1n) ^ (value >> 63n)));
  }

  fixed32(value: number): this {
    const v = value >>> 0;
    this.chunks.push(v & 0xff, (v >>> 8) & 0xff, (v >>> 16) & 0xff, (v >>> 24) & 0xff);
    return this;
  }

  fixed64(value: bigint | number): this {
    let v = BigInt.asUintN(64, BigInt(value));
    for (let i = 0; i < 8; i++) {
      this.chunks.push(Number(v & 0xffn));
      v >>= 8n;
    }
    return this;
  }

  float(value: number): this {
    const buf = new Uint8Array(4);
    new DataView(buf.buffer).setFloat32(0, value, true);
    for (const b of buf) this.chunks.push(b);
    return this;
  }

  double(value: number): this {
    const buf = new Uint8Array(8);
    new DataView(buf.buffer).setFloat64(0, value, true);
    for (const b of buf) this.chunks.push(b);
    return this;
  }

  tag(field: number, wire: WireTypeValue): this {
    return this.varint((field << 3) | wire);
  }

  bytes(data: Uint8Array): this {
    this.varint(data.length);
    for (const b of data) this.chunks.push(b);
    return this;
  }

  string(s: string): this {
    return this.bytes(new TextEncoder().encode(s));
  }

  /** Prepend a LEN tag and length around a nested writer's payload. */
  message(field: number, nested: WireWriter): this {
    this.tag(field, WireType.LEN);
    return this.bytes(nested.finish());
  }

  finish(): Uint8Array {
    return Uint8Array.from(this.chunks);
  }
}

export function zigZagEncode32(n: number): number {
  return ((n << 1) ^ (n >> 31)) >>> 0;
}

export function zigZagDecode32(n: number): number {
  return (n >>> 1) ^ -(n & 1);
}

export function zigZagEncode64(n: bigint): bigint {
  return BigInt.asUintN(64, (n << 1n) ^ (n >> 63n));
}

export function zigZagDecode64(n: bigint): bigint {
  return BigInt.asIntN(64, (n >> 1n) ^ -(n & 1n));
}

export { TWO_POW_32, MASK_64 };
