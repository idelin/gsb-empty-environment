/**
 * Bounded binary primitives: a growable writer and a bounds-checked reader.
 * Every out-of-bounds access raises CorruptionError (never a raw RangeError),
 * so malformed or truncated input is reported as data corruption.
 */

export class CorruptionError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (offset ${offset})`);
    this.name = "CorruptionError";
    this.offset = offset;
  }
}

export class UnsupportedVersionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "UnsupportedVersionError";
  }
}

const CRC32_TABLE = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) {
    c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  }
  CRC32_TABLE[n] = c >>> 0;
}

/** CRC-32 (IEEE 802.3, polynomial 0xEDB88320). */
export function crc32(data: Uint8Array): number {
  let crc = 0xffffffff;
  for (let i = 0; i < data.length; i++) {
    crc = CRC32_TABLE[(crc ^ data[i]) & 0xff] ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

const MAX_U64 = (1n << 64n) - 1n;

/** Growable little-endian writer. */
export class BoundedWriter {
  private buf: Buffer;
  private len = 0;

  constructor(capacity = 256) {
    this.buf = Buffer.allocUnsafe(Math.max(capacity, 16));
  }

  get offset(): number {
    return this.len;
  }

  private ensure(n: number): void {
    if (this.len + n <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < this.len + n) cap *= 2;
    const next = Buffer.allocUnsafe(cap);
    this.buf.copy(next, 0, 0, this.len);
    this.buf = next;
  }

  writeU8(v: number): this {
    this.ensure(1);
    this.buf[this.len] = v & 0xff;
    this.len += 1;
    return this;
  }

  writeU16(v: number): this {
    this.ensure(2);
    this.buf.writeUInt16LE(v, this.len);
    this.len += 2;
    return this;
  }

  writeU32(v: number): this {
    this.ensure(4);
    this.buf.writeUInt32LE(v >>> 0, this.len);
    this.len += 4;
    return this;
  }

  writeU64(v: number | bigint): this {
    this.ensure(8);
    this.buf.writeBigUInt64LE(BigInt(v), this.len);
    this.len += 8;
    return this;
  }

  /** Unsigned LEB128 varint, up to 64 bits. */
  writeVarInt(v: number | bigint): this {
    let x = BigInt(v);
    if (x < 0n || x > MAX_U64) throw new RangeError("varint out of u64 range");
    while (x >= 0x80n) {
      this.writeU8(Number(x & 0x7fn) | 0x80);
      x >>= 7n;
    }
    this.writeU8(Number(x));
    return this;
  }

  writeBytes(b: Uint8Array): this {
    this.ensure(b.length);
    this.buf.set(b, this.len);
    this.len += b.length;
    return this;
  }

  /** u32 length prefix + raw bytes. */
  writeLenBytes(b: Uint8Array): this {
    this.writeU32(b.length);
    return this.writeBytes(b);
  }

  /** View of the written region (aliases internal storage; do not mutate). */
  toBuffer(): Buffer {
    return this.buf.subarray(0, this.len);
  }
}

/** Bounds-checked little-endian reader over a fixed window. */
export class BoundedReader {
  offset: number;
  readonly limit: number;
  readonly buf: Uint8Array;

  constructor(buf: Uint8Array, offset = 0, limit = buf.length) {
    if (offset < 0 || limit > buf.length || offset > limit) {
      throw new CorruptionError("reader window out of bounds", offset);
    }
    this.buf = buf;
    this.offset = offset;
    this.limit = limit;
  }

  get remaining(): number {
    return this.limit - this.offset;
  }

  private need(n: number): void {
    if (n < 0 || this.offset + n > this.limit) {
      throw new CorruptionError(`read of ${n} byte(s) past end of buffer`, this.offset);
    }
  }

  readU8(): number {
    this.need(1);
    const v = this.buf[this.offset];
    this.offset += 1;
    return v;
  }

  readU16(): number {
    this.need(2);
    const v = this.buf[this.offset] | (this.buf[this.offset + 1] << 8);
    this.offset += 2;
    return v;
  }

  readU32(): number {
    this.need(4);
    const o = this.offset;
    const v =
      (this.buf[o] |
        (this.buf[o + 1] << 8) |
        (this.buf[o + 2] << 16) |
        (this.buf[o + 3] << 24)) >>>
      0;
    this.offset += 4;
    return v;
  }

  readU64(): bigint {
    this.need(8);
    let v = 0n;
    for (let i = 7; i >= 0; i--) {
      v = (v << 8n) | BigInt(this.buf[this.offset + i]);
    }
    this.offset += 8;
    return v;
  }

  /** u64 that must fit in a safe JS integer (LSNs, sizes). */
  readU64Number(): number {
    const at = this.offset;
    const v = this.readU64();
    if (v > BigInt(Number.MAX_SAFE_INTEGER)) {
      throw new CorruptionError("u64 exceeds MAX_SAFE_INTEGER", at);
    }
    return Number(v);
  }

  /** Unsigned LEB128 varint; rejects encodings longer than 64 bits. */
  readVarInt(): bigint {
    let result = 0n;
    let shift = 0n;
    for (let i = 0; i < 10; i++) {
      const b = this.readU8();
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) {
        if (result > MAX_U64) {
          throw new CorruptionError("varint overflows u64", this.offset - i - 1);
        }
        return result;
      }
      shift += 7n;
    }
    throw new CorruptionError("varint exceeds 64 bits", this.offset - 10);
  }

  /** Zero-copy view into the underlying buffer (aliases; do not mutate). */
  readBytes(n: number): Uint8Array {
    this.need(n);
    const v = this.buf.subarray(this.offset, this.offset + n);
    this.offset += n;
    return v;
  }

  readLenBytes(): Uint8Array {
    const n = this.readU32();
    return this.readBytes(n);
  }
}
