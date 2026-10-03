/**
 * Wire-format reader/writer for the Protocol Buffers binary encoding.
 * Hand-rolled, zero-dependency, with strict bounds checking on every read.
 */

import {
  decodeFixed32,
  decodeFixed64,
  decodeVarint,
  encodeFixed32,
  encodeFixed64,
  encodeVarint,
  InvalidWireTypeError,
  ProtobufDecodeError,
  TruncatedFieldError,
  varintLength,
  zigzagDecode,
  zigzagEncode,
} from "./varint.ts";

export const WireType = {
  VARINT: 0,
  FIXED64: 1,
  LENGTH_DELIMITED: 2,
  SGROUP: 3, // deprecated, rejected on read
  EGROUP: 4, // deprecated, rejected on read
  FIXED32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export interface Tag {
  readonly fieldNumber: number;
  readonly wireType: WireTypeValue;
}

export function makeTag(fieldNumber: number, wireType: WireTypeValue): number {
  if (!Number.isInteger(fieldNumber) || fieldNumber < 1 || fieldNumber > 0x1fffffff) {
    throw new RangeError(`invalid field number: ${fieldNumber}`);
  }
  return (fieldNumber << 3) | wireType;
}

/** Streaming reader over a byte buffer with a movable cursor. */
export class WireReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly limit: number;

  constructor(buf: Uint8Array, offset = 0, length = buf.length - offset) {
    if (offset < 0 || length < 0 || offset + length > buf.length) {
      throw new RangeError("reader window out of bounds");
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

  get eof(): boolean {
    return this.pos >= this.limit;
  }

  private ensure(n: number, what: string): void {
    if (this.pos + n > this.limit) {
      throw new TruncatedFieldError(what, n, this.limit - this.pos);
    }
  }

  /** Read a tag; returns null at a clean end of the message. */
  readTag(): Tag | null {
    if (this.eof) return null;
    const { value, bytesRead } = decodeVarint(
      this.buf.subarray(0, this.limit),
      this.pos,
    );
    this.pos += bytesRead;
    const raw = Number(value);
    const fieldNumber = raw >>> 3;
    const wireType = (raw & 0x7) as WireTypeValue;
    if (fieldNumber === 0) {
      throw new ProtobufDecodeError("tag with field number 0");
    }
    if (
      wireType === WireType.SGROUP ||
      wireType === WireType.EGROUP ||
      wireType > WireType.FIXED32
    ) {
      throw new InvalidWireTypeError(
        `unsupported wire type ${wireType} for field ${fieldNumber}`,
      );
    }
    return { fieldNumber, wireType };
  }

  readVarint(): bigint {
    const { value, bytesRead } = decodeVarint(
      this.buf.subarray(0, this.limit),
      this.pos,
    );
    this.pos += bytesRead;
    return value;
  }

  readUint32(): number {
    return Number(this.readVarint() & 0xffffffffn);
  }

  readUint64(): bigint {
    return this.readVarint();
  }

  readInt32(): number {
    return Number(BigInt.asIntN(32, this.readVarint()));
  }

  readInt64(): bigint {
    return BigInt.asIntN(64, this.readVarint());
  }

  readSint32(): number {
    return Number(BigInt.asIntN(32, zigzagDecode(this.readVarint())));
  }

  readSint64(): bigint {
    return zigzagDecode(this.readVarint());
  }

  readBool(): boolean {
    return this.readVarint() !== 0n;
  }

  readFixed32(): number {
    this.ensure(4, "fixed32");
    const v = decodeFixed32(this.buf, this.pos);
    this.pos += 4;
    return v;
  }

  readSfixed32(): number {
    return this.readFixed32() | 0;
  }

  readFixed64(): bigint {
    this.ensure(8, "fixed64");
    const v = decodeFixed64(this.buf, this.pos);
    this.pos += 8;
    return v;
  }

  readSfixed64(): bigint {
    return BigInt.asIntN(64, this.readFixed64());
  }

  readFloat(): number {
    this.ensure(4, "float");
    const v = new DataView(
      this.buf.buffer,
      this.buf.byteOffset + this.pos,
      4,
    ).getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  readDouble(): number {
    this.ensure(8, "double");
    const v = new DataView(
      this.buf.buffer,
      this.buf.byteOffset + this.pos,
      8,
    ).getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  /** Read a length-delimited byte slice (zero-copy view). */
  readBytes(): Uint8Array {
    const len = Number(this.readVarint());
    if (len > this.remaining) {
      throw new TruncatedFieldError("length-delimited field", len, this.remaining);
    }
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return new TextDecoder().decode(this.readBytes());
  }

  /** Read a length-delimited nested message as an independent reader. */
  readSubreader(): WireReader {
    const len = Number(this.readVarint());
    if (len > this.remaining) {
      throw new TruncatedFieldError("embedded message", len, this.remaining);
    }
    const sub = new WireReader(this.buf, this.pos, len);
    this.pos += len;
    return sub;
  }

  /** Skip a field of the given wire type, with bounds checks. */
  skip(wireType: WireTypeValue): void {
    switch (wireType) {
      case WireType.VARINT:
        this.readVarint();
        return;
      case WireType.FIXED64:
        this.ensure(8, "fixed64 (skip)");
        this.pos += 8;
        return;
      case WireType.LENGTH_DELIMITED: {
        const len = Number(this.readVarint());
        this.ensure(len, "length-delimited field (skip)");
        this.pos += len;
        return;
      }
      case WireType.FIXED32:
        this.ensure(4, "fixed32 (skip)");
        this.pos += 4;
        return;
      default:
        throw new InvalidWireTypeError(`cannot skip wire type ${wireType}`);
    }
  }
}

/** Growable byte writer. */
export class WireWriter {
  private buf: Uint8Array;
  private len = 0;

  constructor(capacity = 256) {
    this.buf = new Uint8Array(capacity);
  }

  get byteLength(): number {
    return this.len;
  }

  private grow(need: number): void {
    if (this.len + need <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < this.len + need) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  writeRawBytes(bytes: Uint8Array): this {
    this.grow(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
    return this;
  }

  writeTag(fieldNumber: number, wireType: WireTypeValue): this {
    return this.writeVarint(BigInt(makeTag(fieldNumber, wireType)));
  }

  writeVarint(value: bigint): this {
    return this.writeRawBytes(encodeVarint(value));
  }

  writeUint32(value: number): this {
    return this.writeVarint(BigInt(value >>> 0));
  }

  writeUint64(value: bigint): this {
    return this.writeVarint(BigInt.asUintN(64, value));
  }

  writeInt32(value: number): this {
    // int32 negative values are sign-extended to 10-byte varints.
    return this.writeVarint(BigInt.asUintN(64, BigInt(value | 0)));
  }

  writeInt64(value: bigint): this {
    return this.writeVarint(BigInt.asUintN(64, value));
  }

  writeSint32(value: number): this {
    return this.writeVarint(zigzagEncode(BigInt(value | 0)));
  }

  writeSint64(value: bigint): this {
    return this.writeVarint(zigzagEncode(value));
  }

  writeBool(value: boolean): this {
    return this.writeVarint(value ? 1n : 0n);
  }

  writeFixed32(value: number): this {
    return this.writeRawBytes(encodeFixed32(value));
  }

  writeSfixed32(value: number): this {
    return this.writeFixed32(value);
  }

  writeFixed64(value: bigint): this {
    return this.writeRawBytes(encodeFixed64(value));
  }

  writeSfixed64(value: bigint): this {
    return this.writeFixed64(value);
  }

  writeFloat(value: number): this {
    const tmp = new Uint8Array(4);
    new DataView(tmp.buffer).setFloat32(0, value, true);
    return this.writeRawBytes(tmp);
  }

  writeDouble(value: number): this {
    const tmp = new Uint8Array(8);
    new DataView(tmp.buffer).setFloat64(0, value, true);
    return this.writeRawBytes(tmp);
  }

  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(BigInt(bytes.length));
    return this.writeRawBytes(bytes);
  }

  writeString(value: string): this {
    return this.writeBytes(new TextEncoder().encode(value));
  }

  /** Pre-computed length of a varint, for sizing. */
  static varintLength(value: bigint): number {
    return varintLength(value);
  }

  /** Detach the written bytes. The writer keeps no reference to the result. */
  toBytes(): Uint8Array {
    return this.buf.slice(0, this.len);
  }
}
