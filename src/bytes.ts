/** Growable byte writer / cursor-based byte reader used by the segment format. */

export class ByteWriter {
  private buf: Uint8Array;
  private len = 0;

  constructor(capacity = 256) {
    this.buf = new Uint8Array(capacity);
  }

  get length(): number {
    return this.len;
  }

  private ensure(extra: number): void {
    if (this.len + extra <= this.buf.length) return;
    let cap = Math.max(this.buf.length * 2, 16);
    while (cap < this.len + extra) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  writeByte(b: number): void {
    this.ensure(1);
    this.buf[this.len++] = b & 0xff;
  }

  /** Unsigned 32-bit big-endian. */
  writeU32(n: number): void {
    this.ensure(4);
    this.buf[this.len++] = (n >>> 24) & 0xff;
    this.buf[this.len++] = (n >>> 16) & 0xff;
    this.buf[this.len++] = (n >>> 8) & 0xff;
    this.buf[this.len++] = n & 0xff;
  }

  /** Unsigned LEB128 varint (values up to 2^32 - 1). */
  writeVarint(n: number): void {
    let v = n >>> 0;
    while (v > 0x7f) {
      this.writeByte((v & 0x7f) | 0x80);
      v >>>= 7;
    }
    this.writeByte(v);
  }

  writeBytes(bytes: Uint8Array): void {
    this.ensure(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
  }

  toUint8Array(): Uint8Array {
    return this.buf.slice(0, this.len);
  }
}

export class ByteReader {
  readonly buf: Uint8Array;
  offset: number;

  constructor(buf: Uint8Array, offset = 0) {
    this.buf = buf;
    this.offset = offset;
  }

  readByte(): number {
    return this.buf[this.offset++];
  }

  readU32(): number {
    const b = this.buf;
    const o = this.offset;
    this.offset += 4;
    return ((b[o] << 24) | (b[o + 1] << 16) | (b[o + 2] << 8) | b[o + 3]) >>> 0;
  }

  readVarint(): number {
    let result = 0;
    let shift = 0;
    for (;;) {
      const b = this.buf[this.offset++];
      result |= (b & 0x7f) << shift;
      if ((b & 0x80) === 0) break;
      shift += 7;
    }
    return result >>> 0;
  }

  readBytes(len: number): Uint8Array {
    const out = this.buf.subarray(this.offset, this.offset + len);
    this.offset += len;
    return out;
  }
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export function encodeUtf8(s: string): Uint8Array {
  return encoder.encode(s);
}

export function decodeUtf8(bytes: Uint8Array): string {
  return decoder.decode(bytes);
}
