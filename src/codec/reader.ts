import { CodecError } from './errors.ts';
import { readVarint } from './varint.ts';

/**
 * Bounds-checked reader over a shared backing buffer.
 * `bytesView` returns a zero-copy view (Buffer.subarray) into the backing
 * buffer: no allocation, no copy. Callers needing a detached copy use
 * `bytesCopy`.
 */
export class ByteReader {
  pos: number;
  readonly end: number;

  readonly buf: Buffer;

  constructor(buf: Buffer, pos = 0, end = buf.length) {
    this.buf = buf;
    if (pos < 0 || end > buf.length || pos > end) {
      throw new CodecError('MALFORMED', `invalid reader window [${pos}, ${end}) for buffer of ${buf.length}`);
    }
    this.pos = pos;
    this.end = end;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  #ensure(n: number, what: string): void {
    if (!Number.isSafeInteger(n) || n < 0 || n > this.remaining) {
      throw new CodecError('TRUNCATED', `${what}: need ${n} byte(s), only ${this.remaining} remain`);
    }
  }

  u8(): number {
    this.#ensure(1, 'u8');
    return this.buf[this.pos++];
  }

  u16le(): number {
    this.#ensure(2, 'u16le');
    const v = this.buf.readUInt16LE(this.pos);
    this.pos += 2;
    return v;
  }

  u32le(): number {
    this.#ensure(4, 'u32le');
    const v = this.buf.readUInt32LE(this.pos);
    this.pos += 4;
    return v;
  }

  varint(): number {
    return readVarint(() => this.u8());
  }

  /** Zero-copy view into the backing buffer. */
  bytesView(n: number): Buffer {
    this.#ensure(n, 'bytesView');
    const view = this.buf.subarray(this.pos, this.pos + n);
    this.pos += n;
    return view;
  }

  /** Detached copy. */
  bytesCopy(n: number): Buffer {
    this.#ensure(n, 'bytesCopy');
    const out = Buffer.from(this.buf.subarray(this.pos, this.pos + n));
    this.pos += n;
    return out;
  }

  skip(n: number): void {
    this.#ensure(n, 'skip');
    this.pos += n;
  }

  /** Zero-copy sub-reader restricted to the next `n` bytes. */
  subReader(n: number): ByteReader {
    this.#ensure(n, 'subReader');
    const r = new ByteReader(this.buf, this.pos, this.pos + n);
    this.pos += n;
    return r;
  }
}
