import { writeVarint } from './varint.ts';

/** Growable byte writer producing an owned, detached buffer. */
export class ByteWriter {
  #buf: Buffer;
  #len = 0;

  constructor(capacity = 256) {
    this.#buf = Buffer.alloc(capacity);
  }

  get length(): number {
    return this.#len;
  }

  #ensure(n: number): void {
    if (this.#len + n <= this.#buf.length) return;
    let cap = this.#buf.length * 2;
    while (cap < this.#len + n) cap *= 2;
    const next = Buffer.alloc(cap);
    this.#buf.copy(next, 0, 0, this.#len);
    this.#buf = next;
  }

  u8(v: number): this {
    this.#ensure(1);
    this.#buf[this.#len++] = v & 0xff;
    return this;
  }

  u16le(v: number): this {
    this.#ensure(2);
    this.#buf.writeUInt16LE(v & 0xffff, this.#len);
    this.#len += 2;
    return this;
  }

  u32le(v: number): this {
    this.#ensure(4);
    this.#buf.writeUInt32LE(v >>> 0, this.#len);
    this.#len += 4;
    return this;
  }

  varint(v: number): this {
    writeVarint((b) => this.u8(b), v);
    return this;
  }

  bytes(b: Buffer | Uint8Array): this {
    this.#ensure(b.length);
    this.#buf.set(b, this.#len);
    this.#len += b.length;
    return this;
  }

  /** Detached copy of the written bytes. */
  finish(): Buffer {
    return Buffer.from(this.#buf.subarray(0, this.#len));
  }
}
