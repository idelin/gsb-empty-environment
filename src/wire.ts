/**
 * Protocol Buffers binary wire-format primitives.
 * Zero external dependencies. Hand-rolled parsing, no subarray copies on the
 * hot path: nested length-delimited messages are handled by moving a limit
 * cursor inside a single reusable reader (preallocated limit stack).
 */

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export class DecodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DecodeError';
  }
}

/** Raised whenever a read would cross the current limit (truncated input). */
export class TruncatedError extends DecodeError {
  constructor(message: string) {
    super(message);
    this.name = 'TruncatedError';
  }
}

const MAX_VARINT_BYTES = 10;
const MAX_NESTING_DEPTH = 128;
const MAX_SAFE_VARINT_NUMBER = Number.MAX_SAFE_INTEGER;
const UINT64_MASK = 0xffff_ffff_ffff_ffffn;

const textDecoder = new TextDecoder('utf-8');
const textEncoder = new TextEncoder();

export class WireReader {
  buf: Uint8Array = new Uint8Array(0);
  pos = 0;
  limit = 0;
  private readonly limitStack = new Int32Array(MAX_NESTING_DEPTH);
  private depth = 0;

  reset(buf: Uint8Array, pos = 0, limit: number = buf.length): this {
    if (pos < 0 || limit < pos || limit > buf.length) {
      throw new DecodeError(`invalid reset range: pos=${pos} limit=${limit} length=${buf.length}`);
    }
    this.buf = buf;
    this.pos = pos;
    this.limit = limit;
    this.depth = 0;
    return this;
  }

  eof(): boolean {
    return this.pos >= this.limit;
  }

  private require(needed: number, what: string): void {
    if (needed > this.limit - this.pos) {
      throw new TruncatedError(
        `truncated ${what}: need ${needed} byte(s) at offset ${this.pos}, only ${this.limit - this.pos} left`,
      );
    }
  }

  /** Unsigned varint as a JS number. Throws if the value exceeds 2^53-1. */
  readVarint(): number {
    const buf = this.buf;
    const limit = this.limit;
    let pos = this.pos;
    let result = 0;
    let shift = 0;
    for (let i = 0; i < MAX_VARINT_BYTES; i++) {
      if (pos >= limit) {
        throw new TruncatedError(`truncated varint at offset ${this.pos}`);
      }
      const b = buf[pos];
      pos++;
      result += (b & 0x7f) * 2 ** shift;
      if ((b & 0x80) === 0) {
        if (result > MAX_SAFE_VARINT_NUMBER) {
          throw new DecodeError(`varint exceeds Number.MAX_SAFE_INTEGER at offset ${this.pos}`);
        }
        this.pos = pos;
        return result;
      }
      shift += 7;
    }
    throw new DecodeError(`malformed varint: exceeds ${MAX_VARINT_BYTES} bytes at offset ${this.pos}`);
  }

  /** Full 64-bit unsigned varint as bigint. */
  readVarint64(): bigint {
    const buf = this.buf;
    const limit = this.limit;
    let pos = this.pos;
    let result = 0n;
    let shift = 0n;
    for (let i = 0; i < MAX_VARINT_BYTES; i++) {
      if (pos >= limit) {
        throw new TruncatedError(`truncated varint64 at offset ${this.pos}`);
      }
      const b = buf[pos];
      pos++;
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) {
        this.pos = pos;
        return result & UINT64_MASK;
      }
      shift += 7n;
    }
    throw new DecodeError(`malformed varint64: exceeds ${MAX_VARINT_BYTES} bytes at offset ${this.pos}`);
  }

  readZigZag32(): number {
    const n = this.readVarint();
    return (n >>> 1) ^ -(n & 1);
  }

  readZigZag64(): bigint {
    const n = this.readVarint64();
    return (n >> 1n) ^ -(n & 1n);
  }

  readBool(): boolean {
    return this.readVarint() !== 0;
  }

  readFixed32(): number {
    this.require(4, 'fixed32');
    const b = this.buf;
    const p = this.pos;
    const v = (b[p] | (b[p + 1] << 8) | (b[p + 2] << 16) | (b[p + 3] << 24)) >>> 0;
    this.pos = p + 4;
    return v;
  }

  readFixed64(): bigint {
    this.require(8, 'fixed64');
    const b = this.buf;
    const p = this.pos;
    const lo = (b[p] | (b[p + 1] << 8) | (b[p + 2] << 16) | (b[p + 3] << 24)) >>> 0;
    const hi = (b[p + 4] | (b[p + 5] << 8) | (b[p + 6] << 16) | (b[p + 7] << 24)) >>> 0;
    this.pos = p + 8;
    return (BigInt(hi) << 32n) | BigInt(lo);
  }

  /** Length prefix of a length-delimited field. Bounds-checked against limit. */
  readLength(): number {
    const len = this.readVarint();
    if (len > this.limit - this.pos) {
      throw new TruncatedError(
        `declared length ${len} at offset ${this.pos} exceeds remaining ${this.limit - this.pos} byte(s)`,
      );
    }
    return len;
  }

  /** View into the source buffer (no copy). */
  readBytes(): Uint8Array {
    const len = this.readLength();
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return textDecoder.decode(this.readBytes());
  }

  /** Enter a nested length-delimited message without copying bytes. */
  pushLimit(len: number): void {
    if (len > this.limit - this.pos) {
      throw new TruncatedError(
        `nested message length ${len} at offset ${this.pos} exceeds remaining ${this.limit - this.pos} byte(s)`,
      );
    }
    if (this.depth >= MAX_NESTING_DEPTH) {
      throw new DecodeError(`nesting depth exceeds ${MAX_NESTING_DEPTH}`);
    }
    this.limitStack[this.depth] = this.limit;
    this.depth++;
    this.limit = this.pos + len;
  }

  /** Aborts nested-limit state after a failed decode; reader must be reset before reuse. */
  abortToDepth(): void {
    this.depth = 0;
  }

  popLimit(): void {
    if (this.depth === 0) {
      throw new DecodeError('popLimit without matching pushLimit');
    }
    if (this.pos !== this.limit) {
      throw new DecodeError(
        `nested message not fully consumed: pos=${this.pos} limit=${this.limit}`,
      );
    }
    this.depth--;
    this.limit = this.limitStack[this.depth];
  }

  /** Reads a tag; returns fieldNumber * 8 + wireType as a single number. */
  readTag(): number {
    const tag = this.readVarint();
    const field = Math.floor(tag / 8);
    const wire = tag & 7;
    if (field < 1 || field > 0x1fff_ffff) {
      throw new DecodeError(`invalid field number ${field} at offset ${this.pos}`);
    }
    if (wire === 3 || wire === 4 || wire > 5) {
      throw new DecodeError(`unsupported wire type ${wire} at offset ${this.pos}`);
    }
    return tag;
  }

  skipField(wireType: number): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint64();
        return;
      case WireType.Fixed64:
        this.require(8, 'fixed64 skip');
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = this.readLength();
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.require(4, 'fixed32 skip');
        this.pos += 4;
        return;
      default:
        throw new DecodeError(`cannot skip unknown wire type ${wireType} at offset ${this.pos}`);
    }
  }
}

export class WireWriter {
  private buf = new Uint8Array(256);
  private len = 0;

  get size(): number {
    return this.len;
  }

  reset(): void {
    this.len = 0;
  }

  private ensure(extra: number): void {
    const needed = this.len + extra;
    if (needed <= this.buf.length) return;
    let capacity = this.buf.length * 2;
    while (capacity < needed) capacity *= 2;
    const next = new Uint8Array(capacity);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  writeVarint(value: number | bigint): this {
    let v = typeof value === 'bigint' ? value : BigInt(value);
    if (v < 0n) throw new DecodeError(`cannot write negative varint ${v}`);
    this.ensure(MAX_VARINT_BYTES);
    const buf = this.buf;
    let pos = this.len;
    while (v > 0x7fn) {
      buf[pos] = Number(v & 0x7fn) | 0x80;
      pos++;
      v >>= 7n;
    }
    buf[pos] = Number(v);
    this.len = pos + 1;
    return this;
  }

  writeZigZag32(value: number): this {
    return this.writeVarint(((value << 1) ^ (value >> 31)) >>> 0);
  }

  writeZigZag64(value: bigint): this {
    return this.writeVarint((value << 1n) ^ (value >> 63n));
  }

  writeBool(value: boolean): this {
    return this.writeVarint(value ? 1 : 0);
  }

  writeFixed32(value: number): this {
    this.ensure(4);
    const b = this.buf;
    const p = this.len;
    b[p] = value & 0xff;
    b[p + 1] = (value >>> 8) & 0xff;
    b[p + 2] = (value >>> 16) & 0xff;
    b[p + 3] = (value >>> 24) & 0xff;
    this.len = p + 4;
    return this;
  }

  writeFixed64(value: bigint): this {
    this.ensure(8);
    const b = this.buf;
    const p = this.len;
    let v = value & UINT64_MASK;
    for (let i = 0; i < 8; i++) {
      b[p + i] = Number(v & 0xffn);
      v >>= 8n;
    }
    this.len = p + 8;
    return this;
  }

  writeTag(field: number, wireType: number): this {
    return this.writeVarint(field * 8 + wireType);
  }

  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    this.ensure(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
    return this;
  }

  writeString(value: string): this {
    return this.writeBytes(textEncoder.encode(value));
  }

  /** Copies the written bytes out. */
  finish(): Uint8Array {
    return this.buf.slice(0, this.len);
  }
}
