/**
 * Binary codec primitives: LEB128 varints, CRC32, and bounds-checked
 * Reader/Writer with zero-copy byte-slice views.
 *
 * Zero-copy contract: `Reader.readBytes()` returns a Buffer *view* that
 * shares memory with the source buffer (Buffer.subarray). No copy is made.
 */

export class CodecError extends Error {
  constructor(message: string, public readonly offset: number) {
    super(`${message} (at offset ${offset})`);
    this.name = "CodecError";
  }
}

/** Largest integer we serialize: varints are capped at 8 bytes (56 bits) and
 *  values must stay within Number.MAX_SAFE_INTEGER. */
export const MAX_VARINT_BYTES = 8;
export const MAX_VARINT_VALUE = Number.MAX_SAFE_INTEGER;

/** Upper bound for a single WAL frame payload; guards against corrupt
 *  length prefixes causing absurd allocations. */
export const MAX_FRAME_PAYLOAD = 64 * 1024 * 1024;

// ---------------------------------------------------------------- varint --

export function varintLength(value: number): number {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new CodecError(`varint value out of range: ${value}`, -1);
  }
  let n = 1;
  let v = value;
  while (v >= 0x80) {
    v = Math.floor(v / 0x80);
    n++;
  }
  return n;
}

export function writeVarint(buf: Buffer, offset: number, value: number): number {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new CodecError(`varint value out of range: ${value}`, offset);
  }
  let v = value;
  let i = offset;
  while (v >= 0x80) {
    buf.writeUInt8((v % 0x80) | 0x80, i);
    v = Math.floor(v / 0x80);
    i++;
  }
  buf.writeUInt8(v, i);
  return i + 1 - offset;
}

export function readVarint(buf: Buffer, offset: number): { value: number; bytes: number } {
  let result = 0;
  let shift = 0;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new CodecError("truncated varint", offset);
    }
    const byte = buf.readUInt8(pos);
    const payload = byte & 0x7f;
    if (shift < 49) {
      result += payload * 2 ** shift;
    } else {
      // shift >= 49: only safe-integer-preserving payloads allowed.
      result += payload * 2 ** shift;
      if (result > MAX_VARINT_VALUE) {
        throw new CodecError("varint exceeds MAX_SAFE_INTEGER", offset);
      }
    }
    if ((byte & 0x80) === 0) {
      return { value: result, bytes: i + 1 };
    }
    shift += 7;
  }
  throw new CodecError(`varint too long (> ${MAX_VARINT_BYTES} bytes)`, offset);
}

// ----------------------------------------------------------------- crc32 --

const CRC_TABLE: Uint32Array = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) {
      c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    }
    table[n] = c >>> 0;
  }
  return table;
})();

export function crc32(buf: Uint8Array, crc = 0xffffffff): number {
  let c = crc;
  for (let i = 0; i < buf.length; i++) {
    c = CRC_TABLE[(c ^ buf[i]!) & 0xff]! ^ (c >>> 8);
  }
  return (c ^ 0xffffffff) >>> 0;
}

// ----------------------------------------------------------------- writer --

export class Writer {
  private buf: Buffer;
  private len = 0;

  constructor(capacity = 256) {
    if (capacity <= 0) throw new CodecError("capacity must be positive", 0);
    this.buf = Buffer.allocUnsafe(capacity);
  }

  get offset(): number {
    return this.len;
  }

  private ensure(need: number): void {
    if (this.len + need <= this.buf.length) return;
    let cap = this.buf.length;
    while (cap < this.len + need) cap *= 2;
    const next = Buffer.allocUnsafe(cap);
    this.buf.copy(next, 0, 0, this.len);
    this.buf = next;
  }

  writeU8(v: number): this {
    this.ensure(1);
    this.buf.writeUInt8(v & 0xff, this.len);
    this.len += 1;
    return this;
  }

  writeU32(v: number): this {
    this.ensure(4);
    this.buf.writeUInt32LE(v >>> 0, this.len);
    this.len += 4;
    return this;
  }

  writeVarint(v: number): this {
    this.ensure(MAX_VARINT_BYTES);
    this.len += writeVarint(this.buf, this.len, v);
    return this;
  }

  /** Copies `bytes` into the writer (writers own their output). */
  writeBytes(bytes: Uint8Array): this {
    this.ensure(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
    return this;
  }

  /** Returns a fresh Buffer containing exactly the written bytes. */
  toBuffer(): Buffer {
    const out = Buffer.allocUnsafe(this.len);
    this.buf.copy(out, 0, 0, this.len);
    return out;
  }
}

// ----------------------------------------------------------------- reader --

export class Reader {
  private pos = 0;

  constructor(
    private readonly buf: Buffer,
    private readonly limit = buf.length,
  ) {
    if (limit > buf.length) {
      throw new CodecError(`limit ${limit} exceeds buffer length ${buf.length}`, 0);
    }
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  get exhausted(): boolean {
    return this.pos >= this.limit;
  }

  private need(n: number): void {
    if (n < 0 || this.pos + n > this.limit) {
      throw new CodecError(
        `read of ${n} bytes out of bounds (remaining ${this.remaining})`,
        this.pos,
      );
    }
  }

  readU8(): number {
    this.need(1);
    const v = this.buf.readUInt8(this.pos);
    this.pos += 1;
    return v;
  }

  readU32(): number {
    this.need(4);
    const v = this.buf.readUInt32LE(this.pos);
    this.pos += 4;
    return v;
  }

  readVarint(): number {
    const { value, bytes } = readVarint(
      this.buf.subarray(0, this.limit),
      this.pos,
    );
    this.pos += bytes;
    return value;
  }

  /**
   * Zero-copy read: returns a *view* into the underlying buffer.
   * The returned Buffer shares memory with the source; mutating either is
   * visible through the other. Callers must not outlive the source buffer.
   */
  readBytes(n: number): Buffer {
    this.need(n);
    const view = this.buf.subarray(this.pos, this.pos + n);
    this.pos += n;
    return view;
  }

  /** Skip n bytes (used for forward compatibility with unknown fields). */
  skip(n: number): void {
    this.need(n);
    this.pos += n;
  }
}
