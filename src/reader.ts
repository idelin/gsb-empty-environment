import { DecodeError } from "./errors.ts";
import { decodeVarint, toSigned32, toSigned64, zigzagDecode } from "./varint.ts";

export const WIRE_VARINT = 0;
export const WIRE_FIXED64 = 1;
export const WIRE_LENGTH_DELIMITED = 2;
export const WIRE_SGROUP = 3;
export const WIRE_EGROUP = 4;
export const WIRE_FIXED32 = 5;

export const MAX_NESTING_DEPTH = 100;

/**
 * Bounds-checked cursor over a protobuf wire-format buffer.
 * Every read validates the remaining length and throws DecodeError on
 * truncation — no read can ever escape the buffer (or the enclosing
 * length-delimited sub-message limit).
 */
export class WireReader {
  private buf: Uint8Array | null;
  pos: number;
  private readonly limit: number;
  private readonly depth: number;

  constructor(buf: Uint8Array, offset = 0, length?: number, depth = 0) {
    if (offset < 0 || offset > buf.length) {
      throw new DecodeError("reader offset out of bounds", offset);
    }
    const end = length === undefined ? buf.length : offset + length;
    if (end > buf.length) {
      throw new DecodeError("reader length exceeds buffer", offset);
    }
    if (depth > MAX_NESTING_DEPTH) {
      throw new DecodeError(`nesting depth exceeds ${MAX_NESTING_DEPTH}`, offset);
    }
    this.buf = buf;
    this.pos = offset;
    this.limit = end;
    this.depth = depth;
  }

  private buffer(): Uint8Array {
    if (this.buf === null) {
      throw new DecodeError("reader has been released", this.pos);
    }
    return this.buf;
  }

  get eof(): boolean {
    return this.pos >= this.limit;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  private need(n: number, what: string): Uint8Array {
    const buf = this.buffer();
    if (n > this.remaining) {
      throw new DecodeError(
        `truncated ${what}: need ${n} byte(s), only ${this.remaining} remaining`,
        this.pos,
      );
    }
    return buf;
  }

  readVarint(): bigint {
    const buf = this.need(1, "varint");
    const { value, bytesRead } = decodeVarint(buf, this.pos, this.limit);
    this.pos += bytesRead;
    return value;
  }

  readVarint32(): number {
    return Number(this.readVarint() & 0xffff_ffffn);
  }

  readZigZag(): bigint {
    return zigzagDecode(this.readVarint());
  }

  readBool(): boolean {
    return this.readVarint() !== 0n;
  }

  readFixed32(): number {
    const buf = this.need(4, "fixed32");
    const p = this.pos;
    this.pos += 4;
    return (buf[p] | (buf[p + 1] << 8) | (buf[p + 2] << 16) | (buf[p + 3] << 24)) >>> 0;
  }

  readSFixed32(): number {
    return toSigned32(this.readFixed32());
  }

  readFixed64(): bigint {
    const buf = this.need(8, "fixed64");
    const p = this.pos;
    this.pos += 8;
    let v = 0n;
    for (let i = 7; i >= 0; i--) {
      v = (v << 8n) | BigInt(buf[p + i]);
    }
    return v;
  }

  readSFixed64(): bigint {
    return toSigned64(this.readFixed64());
  }

  /** Read a length-delimited raw byte block (copy). */
  readBytes(): Uint8Array {
    const len = this.readVarint();
    if (len > BigInt(this.remaining)) {
      throw new DecodeError(
        `truncated length-delimited field: declared ${len} byte(s), only ${this.remaining} remaining`,
        this.pos,
      );
    }
    const n = Number(len);
    const buf = this.buffer();
    const out = buf.slice(this.pos, this.pos + n);
    this.pos += n;
    return out;
  }

  readString(): string {
    return new TextDecoder("utf-8", { fatal: true }).decode(this.readBytes());
  }

  /**
   * Open a bounded sub-reader for a length-delimited nested message.
   * The sub-reader cannot read past its declared length; the parent cursor
   * is advanced past the whole block up front, so a malformed nested payload
   * can never corrupt the parent's framing.
   */
  subReader(): WireReader {
    const len = this.readVarint();
    if (len > BigInt(this.remaining)) {
      throw new DecodeError(
        `truncated nested message: declared ${len} byte(s), only ${this.remaining} remaining`,
        this.pos,
      );
    }
    const n = Number(len);
    const buf = this.buffer();
    const child = new WireReader(buf, this.pos, n, this.depth + 1);
    this.pos += n;
    return child;
  }

  /** Read a field tag. Returns null at end of the (sub-)message. */
  readTag(): { field: number; wireType: number } | null {
    if (this.eof) return null;
    const key = this.readVarint();
    const field = Number(key >> 3n);
    const wireType = Number(key & 0x7n);
    if (field === 0) {
      throw new DecodeError("invalid tag: field number 0", this.pos);
    }
    if (wireType === WIRE_SGROUP || wireType === WIRE_EGROUP) {
      throw new DecodeError(`unsupported wire type ${wireType} (groups)`, this.pos);
    }
    if (wireType > WIRE_FIXED32) {
      throw new DecodeError(`invalid wire type ${wireType}`, this.pos);
    }
    return { field, wireType };
  }

  /** Skip a field of the given wire type, fully bounds-checked. */
  skip(wireType: number): void {
    switch (wireType) {
      case WIRE_VARINT:
        this.readVarint();
        return;
      case WIRE_FIXED64:
        this.need(8, "fixed64");
        this.pos += 8;
        return;
      case WIRE_LENGTH_DELIMITED: {
        const len = this.readVarint();
        if (len > BigInt(this.remaining)) {
          throw new DecodeError(
            `truncated skipped field: declared ${len} byte(s), only ${this.remaining} remaining`,
            this.pos,
          );
        }
        this.pos += Number(len);
        return;
      }
      case WIRE_FIXED32:
        this.need(4, "fixed32");
        this.pos += 4;
        return;
      default:
        throw new DecodeError(`cannot skip wire type ${wireType}`, this.pos);
    }
  }

  /** Drop the buffer reference so large inputs are not retained. */
  release(): void {
    this.buf = null;
  }
}
