import { MalformedError, TruncationError } from "./errors.ts";
import {
  MAX_VARINT_BYTES,
  decodeVarint,
  varintLength,
} from "./varint.ts";

/** Protobuf wire types (low 3 bits of every tag). */
export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  StartGroup: 3, // deprecated, rejected on decode
  EndGroup: 4, // deprecated, rejected on decode
  Fixed32: 5,
} as const;
export type WireType = (typeof WireType)[keyof typeof WireType];

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder("utf-8", { fatal: true });

/** Growable little-endian wire-format writer. */
export class Writer {
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
    let cap = this.buf.length * 2;
    while (cap < this.len + extra) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  byte(b: number): this {
    this.ensure(1);
    this.buf[this.len++] = b & 0xff;
    return this;
  }

  varint(value: bigint | number): this {
    let v = BigInt(value) & 0xffff_ffff_ffff_ffffn;
    this.ensure(MAX_VARINT_BYTES);
    while (v >= 0x80n) {
      this.buf[this.len++] = Number(v & 0x7fn) | 0x80;
      v >>= 7n;
    }
    this.buf[this.len++] = Number(v);
    return this;
  }

  tag(fieldNo: number, wireType: WireType): this {
    if (!Number.isInteger(fieldNo) || fieldNo < 1 || fieldNo > 0x1fff_ffff) {
      throw new MalformedError(`illegal field number ${fieldNo}`);
    }
    return this.varint((fieldNo << 3) | wireType);
  }

  fixed32(value: number): this {
    this.ensure(4);
    new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 4).setUint32(
      0,
      value >>> 0,
      true,
    );
    this.len += 4;
    return this;
  }

  sfixed32(value: number): this {
    return this.fixed32(value >>> 0);
  }

  fixed64(value: bigint | number): this {
    this.ensure(8);
    new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 8).setBigUint64(
      0,
      BigInt(value) & 0xffff_ffff_ffff_ffffn,
      true,
    );
    this.len += 8;
    return this;
  }

  sfixed64(value: bigint | number): this {
    return this.fixed64(value);
  }

  float(value: number): this {
    this.ensure(4);
    new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 4).setFloat32(
      0,
      value,
      true,
    );
    this.len += 4;
    return this;
  }

  double(value: number): this {
    this.ensure(8);
    new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 8).setFloat64(
      0,
      value,
      true,
    );
    this.len += 8;
    return this;
  }

  bytes(data: Uint8Array): this {
    this.varint(data.length);
    this.ensure(data.length);
    this.buf.set(data, this.len);
    this.len += data.length;
    return this;
  }

  string(text: string): this {
    return this.bytes(textEncoder.encode(text));
  }

  /** Detach the written bytes (copy-free view of exact length). */
  finish(): Uint8Array {
    return this.buf.slice(0, this.len);
  }
}

/**
 * Bounds-checked cursor over a wire-format buffer. Every read validates the
 * remaining length first, so a truncated stream can never read out of bounds.
 */
export class Reader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly limit: number;
  /** Field path breadcrumb for error reporting, e.g. "3.payload.1". */
  fieldPath = "";

  constructor(buf: Uint8Array, offset = 0, length = buf.length - offset) {
    if (offset < 0 || length < 0 || offset + length > buf.length) {
      throw new TruncationError("reader window", length, buf.length - offset, offset);
    }
    this.buf = buf;
    this.pos = offset;
    this.limit = offset + length;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.limit;
  }

  private need(n: number, what: string): void {
    if (this.remaining < n) {
      throw new TruncationError(what, n, this.remaining, this.pos, this.fieldPath);
    }
  }

  varint(): bigint {
    try {
      const { value, bytesRead } = decodeVarint(this.buf, this.pos, this.limit);
      this.pos += bytesRead;
      return value;
    } catch (err) {
      if (err instanceof TruncationError && this.fieldPath) {
        throw new TruncationError("varint", 1, 0, this.pos, this.fieldPath);
      }
      throw err;
    }
  }

  tag(): { fieldNo: number; wireType: WireType } {
    const raw = this.varint();
    const fieldNo = Number(raw >> 3n);
    const wireType = Number(raw & 7n) as WireType;
    if (fieldNo === 0) {
      throw new MalformedError("field number 0 is illegal", this.pos);
    }
    if (wireType === WireType.StartGroup || wireType === WireType.EndGroup) {
      throw new MalformedError(
        `groups (wire type ${wireType}) are not supported`,
        this.pos,
      );
    }
    return { fieldNo, wireType };
  }

  fixed32(): number {
    this.need(4, "fixed32");
    const v = new DataView(
      this.buf.buffer,
      this.buf.byteOffset + this.pos,
      4,
    ).getUint32(0, true);
    this.pos += 4;
    return v;
  }

  sfixed32(): number {
    return this.fixed32() | 0;
  }

  fixed64(): bigint {
    this.need(8, "fixed64");
    const v = new DataView(
      this.buf.buffer,
      this.buf.byteOffset + this.pos,
      8,
    ).getBigUint64(0, true);
    this.pos += 8;
    return v;
  }

  sfixed64(): bigint {
    const v = this.fixed64();
    return v >= 1n << 63n ? v - (1n << 64n) : v;
  }

  float(): number {
    this.need(4, "float");
    const v = new DataView(
      this.buf.buffer,
      this.buf.byteOffset + this.pos,
      4,
    ).getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  double(): number {
    this.need(8, "double");
    const v = new DataView(
      this.buf.buffer,
      this.buf.byteOffset + this.pos,
      8,
    ).getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  /** Read a length-delimited payload as a detached copy. */
  bytes(): Uint8Array {
    const n = Number(this.varint());
    if (n > this.remaining) {
      throw new TruncationError(
        "length-delimited payload",
        n,
        this.remaining,
        this.pos,
        this.fieldPath,
      );
    }
    const out = this.buf.slice(this.pos, this.pos + n);
    this.pos += n;
    return out;
  }

  string(): string {
    const raw = this.bytes();
    try {
      return textDecoder.decode(raw);
    } catch {
      throw new MalformedError("invalid UTF-8 in string field", this.pos);
    }
  }

  /**
   * Enter a length-delimited sub-message. Returns a nested Reader whose limit
   * is the declared length; the parent advances past the whole block.
   */
  subMessage(childPath: string): Reader {
    const n = Number(this.varint());
    if (n > this.remaining) {
      throw new TruncationError(
        "embedded message",
        n,
        this.remaining,
        this.pos,
        this.fieldPath,
      );
    }
    const child = new Reader(this.buf, this.pos, n);
    child.fieldPath = childPath;
    this.pos += n;
    return child;
  }

  /** Skip a field of the given wire type, bounds-checked. */
  skip(wireType: WireType): void {
    switch (wireType) {
      case WireType.Varint:
        this.varint();
        return;
      case WireType.Fixed64:
        this.need(8, "fixed64 skip");
        this.pos += 8;
        return;
      case WireType.Fixed32:
        this.need(4, "fixed32 skip");
        this.pos += 4;
        return;
      case WireType.LengthDelimited: {
        const n = Number(this.varint());
        this.need(n, "length-delimited skip");
        this.pos += n;
        return;
      }
      default:
        throw new MalformedError(`cannot skip wire type ${wireType}`, this.pos);
    }
  }
}

export { varintLength };
