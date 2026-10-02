/**
 * Bounds-checked cursor over a protobuf wire-format buffer.
 * Every read validates remaining length and throws TruncatedFieldError
 * instead of ever touching memory out of range.
 */
import { TruncatedFieldError } from "./errors.ts";
import {
  decodeVarint,
  decodeVarint64,
  zigzagDecode32,
  zigzagDecode64,
} from "./varint.ts";

export const WIRE_VARINT = 0;
export const WIRE_FIXED64 = 1;
export const WIRE_LENGTH_DELIMITED = 2;
export const WIRE_FIXED32 = 5;

const textDecoder = new TextDecoder();

export class WireReader {
  buf: Uint8Array;
  pos: number;
  end: number;

  constructor(buf?: Uint8Array, start = 0, end?: number) {
    this.buf = buf ?? new Uint8Array(0);
    this.pos = start;
    this.end = end ?? this.buf.length;
  }

  /** Rebind in place — the pooling-friendly reset path (no allocation). */
  reset(buf: Uint8Array, start = 0, end: number = buf.length): this {
    this.buf = buf;
    this.pos = start;
    this.end = end;
    return this;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.end;
  }

  private need(n: number, context: string): void {
    if (this.end - this.pos < n) {
      throw new TruncatedFieldError(this.pos, n, this.end - this.pos, context);
    }
  }

  readVarint(): number {
    const [v, next] = decodeVarint(this.buf, this.pos, this.end);
    this.pos = next;
    return v;
  }

  readVarint64(): bigint {
    const [v, next] = decodeVarint64(this.buf, this.pos, this.end);
    this.pos = next;
    return v;
  }

  readZigZag32(): number {
    return zigzagDecode32(this.readVarint());
  }

  readZigZag64(): bigint {
    return zigzagDecode64(this.readVarint64());
  }

  readFixed32(): number {
    this.need(4, "fixed32");
    const b = this.buf;
    const p = this.pos;
    const v =
      (b[p] | (b[p + 1] << 8) | (b[p + 2] << 16) | (b[p + 3] << 24)) >>> 0;
    this.pos = p + 4;
    return v;
  }

  readFixed64(): bigint {
    this.need(8, "fixed64");
    const lo = this.readFixed32();
    const hi = this.readFixed32();
    return (BigInt(hi) << 32n) | BigInt(lo);
  }

  /** Zero-copy: returns a view into the underlying buffer. */
  readBytes(): Uint8Array {
    const len = this.readVarint();
    this.need(len, "bytes payload");
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return textDecoder.decode(this.readBytes());
  }

  /**
   * Enter a length-delimited sub-message: reads the length, bounds-checks it,
   * and rebinds THIS reader to the sub-window. Pair with leaveSub().
   * Zero-allocation nested parsing.
   */
  enterSub(): number {
    const len = this.readVarint();
    this.need(len, "sub-message");
    const savedEnd = this.end;
    this.end = this.pos + len;
    return savedEnd;
  }

  leaveSub(savedEnd: number): void {
    if (this.pos !== this.end) {
      // A well-formed decoder consumes exactly the sub-window.
      this.pos = this.end;
    }
    this.end = savedEnd;
  }

  /** Skip a field of the given wire type, fully bounds-checked. */
  skip(wireType: number): void {
    switch (wireType) {
      case WIRE_VARINT:
        this.readVarint64();
        return;
      case WIRE_FIXED64:
        this.need(8, "skip fixed64");
        this.pos += 8;
        return;
      case WIRE_LENGTH_DELIMITED: {
        const len = this.readVarint();
        this.need(len, "skip length-delimited");
        this.pos += len;
        return;
      }
      case WIRE_FIXED32:
        this.need(4, "skip fixed32");
        this.pos += 4;
        return;
      default:
        throw new TruncatedFieldError(this.pos, 0, 0, `bad wire type ${wireType}`);
    }
  }

  /** Read a tag; returns [fieldNumber, wireType]. */
  readTag(): [number, number] {
    const tag = this.readVarint();
    return [tag >>> 3, tag & 0x07];
  }
}
