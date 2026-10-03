/**
 * Bounds-checked wire-format reader and growable writer.
 * Every read validates remaining capacity before touching the buffer.
 */
import { ProtobufDecodeError, TruncatedMessageError } from './errors.ts';
import {
  asSigned64,
  decodeVarint,
  encodeVarint,
  varintLength,
  zigZagDecode32,
  zigZagDecode64,
  zigZagEncode32,
  zigZagEncode64,
} from './varint.ts';

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireType = (typeof WireType)[keyof typeof WireType];

const MASK_32 = (1n << 32n) - 1n;

export class WireReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly limit: number;

  constructor(buf: Uint8Array, offset = 0, length = buf.length - offset) {
    if (offset < 0 || length < 0 || offset + length > buf.length) {
      throw new TruncatedMessageError('reader window', offset + length, buf.length);
    }
    this.buf = buf;
    this.pos = offset;
    this.limit = offset + length;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  get eof(): boolean {
    return this.pos >= this.limit;
  }

  private require(needed: number, context: string): void {
    if (needed > this.remaining) {
      throw new TruncatedMessageError(context, needed, this.remaining);
    }
  }

  readVarint(): bigint {
    const view = this.buf.subarray(this.pos, this.limit);
    const { value, bytesRead } = decodeVarint(view, 0);
    this.pos += bytesRead;
    return value;
  }

  readVarint32(): number {
    return Number(this.readVarint() & MASK_32);
  }

  readInt32(): number {
    return this.readVarint32() | 0;
  }

  readInt64(): bigint {
    return asSigned64(this.readVarint());
  }

  readUint32(): number {
    return this.readVarint32();
  }

  readUint64(): bigint {
    return this.readVarint();
  }

  readSint32(): number {
    return zigZagDecode32(this.readVarint32());
  }

  readSint64(): bigint {
    return zigZagDecode64(this.readVarint());
  }

  readBool(): boolean {
    return this.readVarint() !== 0n;
  }

  readFixed32(): number {
    this.require(4, 'fixed32');
    const p = this.pos;
    const value =
      (this.buf[p] |
        (this.buf[p + 1] << 8) |
        (this.buf[p + 2] << 16) |
        (this.buf[p + 3] << 24)) >>>
      0;
    this.pos += 4;
    return value;
  }

  readSfixed32(): number {
    return this.readFixed32() | 0;
  }

  readFixed64(): bigint {
    this.require(8, 'fixed64');
    const lo = BigInt(this.readFixed32Unchecked());
    const hi = BigInt(this.readFixed32Unchecked());
    return (hi << 32n) | lo;
  }

  readSfixed64(): bigint {
    return asSigned64(this.readFixed64());
  }

  private readFixed32Unchecked(): number {
    const p = this.pos;
    const value =
      (this.buf[p] |
        (this.buf[p + 1] << 8) |
        (this.buf[p + 2] << 16) |
        (this.buf[p + 3] << 24)) >>>
      0;
    this.pos += 4;
    return value;
  }

  /** Read a length-delimited payload as a raw slice (bounds-checked). */
  readBytes(): Uint8Array {
    const length = Number(this.readVarint());
    if (length < 0 || length > this.remaining) {
      throw new TruncatedMessageError('length-delimited payload', length, this.remaining);
    }
    const out = this.buf.subarray(this.pos, this.pos + length);
    this.pos += length;
    return out;
  }

  readString(): string {
    return new TextDecoder('utf-8', { fatal: true }).decode(this.readBytes());
  }

  /** Skip a field of the given wire type (used for unknown fields). */
  skip(wireType: WireType): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.require(8, 'fixed64 skip');
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const length = Number(this.readVarint());
        if (length > this.remaining) {
          throw new TruncatedMessageError('length-delimited skip', length, this.remaining);
        }
        this.pos += length;
        return;
      }
      case WireType.Fixed32:
        this.require(4, 'fixed32 skip');
        this.pos += 4;
        return;
      default:
        throw new ProtobufDecodeError(`invalid wire type ${wireType}`);
    }
  }
}

export class WireWriter {
  private chunks: Uint8Array[] = [];
  private size = 0;

  get length(): number {
    return this.size;
  }

  private push(bytes: Uint8Array): void {
    this.chunks.push(bytes);
    this.size += bytes.length;
  }

  writeVarint(value: bigint): this {
    this.push(encodeVarint(value));
    return this;
  }

  writeTag(fieldNumber: number, wireType: WireType): this {
    if (!Number.isInteger(fieldNumber) || fieldNumber < 1 || fieldNumber > 0x1fffffff) {
      throw new ProtobufDecodeError(`invalid field number ${fieldNumber}`);
    }
    return this.writeVarint(BigInt((fieldNumber << 3) | wireType));
  }

  writeFixed32(value: number): this {
    const out = new Uint8Array(4);
    const v = value >>> 0;
    out[0] = v & 0xff;
    out[1] = (v >>> 8) & 0xff;
    out[2] = (v >>> 16) & 0xff;
    out[3] = (v >>> 24) & 0xff;
    this.push(out);
    return this;
  }

  writeFixed64(value: bigint): this {
    const out = new Uint8Array(8);
    let v = value & ((1n << 64n) - 1n);
    for (let i = 0; i < 8; i += 1) {
      out[i] = Number(v & 0xffn);
      v >>= 8n;
    }
    this.push(out);
    return this;
  }

  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(BigInt(bytes.length));
    this.push(bytes);
    return this;
  }

  writeString(text: string): this {
    return this.writeBytes(new TextEncoder().encode(text));
  }

  /** Precomputed size helpers for packed fields. */
  static varintSize(value: bigint): number {
    return varintLength(value);
  }

  finish(): Uint8Array {
    const out = new Uint8Array(this.size);
    let offset = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, offset);
      offset += chunk.length;
    }
    return out;
  }
}

export { zigZagEncode32, zigZagDecode32, zigZagEncode64, zigZagDecode64 };
