/**
 * Bounds-checked cursor over a protobuf byte stream.
 * Every read is guarded: crossing `limit` raises TruncationError (safe out-of-bounds protection).
 */
import {
  WireType,
  WireError,
  TruncationError,
  MAX_UINT32,
  MAX_LENGTH_PREFIX,
  decodeVarint,
  decodeFixed32,
  decodeFixed64,
  zigzagDecode,
} from './wire.ts';

export class WireReader {
  readonly buf: Uint8Array;
  readonly limit: number;
  readonly path: string;
  private pos: number;
  private children: WireReader[] = [];

  constructor(buf: Uint8Array, limit?: number, path = '$', offset = 0) {
    this.buf = buf;
    this.limit = limit ?? buf.length;
    this.path = path;
    this.pos = offset;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  atEnd(): boolean {
    return this.pos >= this.limit;
  }

  private need(n: number, what: string): void {
    if (n > this.remaining) {
      throw new TruncationError(
        `${this.path}: truncated ${what} at offset ${this.pos} ` +
          `(need ${n}B, only ${this.remaining}B available)`,
        this.pos,
        n,
        this.remaining,
      );
    }
  }

  readVarint(): bigint {
    const r = decodeVarint(this.buf, this.pos, this.limit);
    this.pos = r.next;
    return r.value;
  }

  readZigZag(): bigint {
    return zigzagDecode(this.readVarint());
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const r = decodeFixed32(this.buf, this.pos, this.limit);
    this.pos = r.next;
    return r.value;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    const r = decodeFixed64(this.buf, this.pos, this.limit);
    this.pos = r.next;
    return r.value;
  }

  readLength(): number {
    const v = this.readVarint();
    if (v > MAX_LENGTH_PREFIX) {
      throw new WireError(`${this.path}: length prefix ${v} exceeds 2^31-1 at offset ${this.pos}`);
    }
    return Number(v);
  }

  readBytes(): Uint8Array {
    const len = this.readLength();
    this.need(len, `length-delimited(${len}B)`);
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return new TextDecoder('utf-8', { fatal: true }).decode(this.readBytes());
  }

  readTag(): { field: number; wire: number } {
    const v = this.readVarint();
    if (v > MAX_UINT32) {
      throw new WireError(`${this.path}: tag ${v} exceeds 32 bits at offset ${this.pos}`);
    }
    const tag = Number(v);
    const field = tag >>> 3;
    const wire = tag & 7;
    if (field === 0) {
      throw new WireError(`${this.path}: invalid field number 0 at offset ${this.pos}`);
    }
    return { field, wire };
  }

  /** Skip a value of the given wire type, fully bounds-checked. */
  skip(wire: number): void {
    switch (wire) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.I64:
        this.need(8, 'fixed64');
        this.pos += 8;
        return;
      case WireType.Len: {
        const len = this.readLength();
        this.need(len, `length-delimited(${len}B)`);
        this.pos += len;
        return;
      }
      case WireType.I32:
        this.need(4, 'fixed32');
        this.pos += 4;
        return;
      default:
        throw new WireError(`${this.path}: unsupported wire type ${wire} at offset ${this.pos}`);
    }
  }

  /** Create a child reader bounded to `len` bytes; the parent cursor advances past them. */
  subReader(len: number, path: string): WireReader {
    this.need(len, `sub-message(${len}B)`);
    const child = new WireReader(this.buf, this.pos + len, path, this.pos);
    this.children.push(child);
    this.pos += len;
    return child;
  }

  /**
   * Leak assertion: this reader and every spawned sub-reader must be fully drained.
   * Returns the list of paths that still hold unconsumed bytes.
   */
  assertDrained(): string[] {
    const leaks: string[] = [];
    if (!this.atEnd()) leaks.push(`${this.path} (${this.remaining}B unconsumed)`);
    for (const child of this.children) leaks.push(...child.assertDrained());
    return leaks;
  }
}
