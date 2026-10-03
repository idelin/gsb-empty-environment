/** Shared byte-level helpers for the compact segment format. */

/** Unsigned LEB128-style varint writer backed by a growable byte list. */
export class ByteWriter {
  private bytes: number[] = [];

  get length(): number {
    return this.bytes.length;
  }

  writeVarint(value: number): void {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new RangeError(`varint value must be a non-negative safe integer, got ${value}`);
    }
    let v = value;
    while (v >= 0x80) {
      this.bytes.push((v % 0x80) | 0x80);
      v = Math.floor(v / 0x80);
    }
    this.bytes.push(v);
  }

  writeBytes(data: Uint8Array): void {
    for (const b of data) this.bytes.push(b);
  }

  toUint8Array(): Uint8Array {
    return Uint8Array.from(this.bytes);
  }
}

/** Cursor-style reader over a segment buffer. `offset` is publicly mutable so skip pointers can reposition it. */
export class ByteReader {
  private readonly data: Uint8Array;
  public offset: number;

  constructor(data: Uint8Array, offset: number) {
    this.data = data;
    this.offset = offset;
  }

  readVarint(): number {
    let result = 0;
    let shift = 1;
    for (;;) {
      const byte = this.data[this.offset++];
      if (byte === undefined) throw new RangeError('varint read past end of buffer');
      result += (byte & 0x7f) * shift;
      if ((byte & 0x80) === 0) return result;
      shift *= 0x80;
    }
  }

  readBytes(length: number): Uint8Array {
    const slice = this.data.subarray(this.offset, this.offset + length);
    if (slice.length !== length) throw new RangeError('byte read past end of buffer');
    this.offset += length;
    return slice;
  }
}

/** Lexicographic comparison over raw UTF-8 bytes (matches typical FST/term-dict ordering). */
export function compareBytes(a: Uint8Array, b: Uint8Array): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

/** Length of the shared prefix of two byte strings. */
export function sharedPrefixLength(a: Uint8Array, b: Uint8Array): number {
  const n = Math.min(a.length, b.length);
  let i = 0;
  while (i < n && a[i] === b[i]) i++;
  return i;
}
