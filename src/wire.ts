/**
 * Protobuf wire-format reader/writer with strict bounds checking.
 * Supports wire types 0 (Varint), 1 (Fixed64), 2 (Length-delimited), 5 (Fixed32).
 * Every read is guarded: a truncated field always raises TruncatedFieldError
 * and the reader never touches memory beyond the buffer end.
 */

import {
  decodeFixed32,
  decodeFixed64,
  decodeVarint,
  encodeFixed32,
  encodeFixed64,
  encodeVarint,
  TruncatedFieldError,
  zigzagDecode,
} from './varint.ts';

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export class MalformedTagError extends Error {
  constructor(fieldNumber: number, wireType: number) {
    super(`malformed tag: fieldNumber=${fieldNumber} wireType=${wireType}`);
    this.name = 'MalformedTagError';
  }
}

export class WireReader {
  private pos = 0;
  private readonly buf: Uint8Array;
  private readonly end: number;

  constructor(buf: Uint8Array, end: number = buf.length) {
    if (end > buf.length) {
      throw new TruncatedFieldError('message window', end, buf.length);
    }
    this.buf = buf;
    this.end = end;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.end;
  }

  private need(n: number, what: string): void {
    if (this.pos + n > this.end) {
      throw new TruncatedFieldError(what, n, this.end - this.pos);
    }
  }

  /** Read a tag; returns { fieldNumber, wireType }. */
  readTag(): { fieldNumber: number; wireType: WireTypeValue } {
    const { value, bytesRead } = decodeVarint(this.view(), 0);
    this.pos += bytesRead;
    const tag = Number(value & 0xffffffffn);
    const fieldNumber = tag >>> 3;
    const wireType = tag & 0x7;
    if (fieldNumber === 0 || (wireType !== 0 && wireType !== 1 && wireType !== 2 && wireType !== 5)) {
      throw new MalformedTagError(fieldNumber, wireType);
    }
    return { fieldNumber, wireType: wireType as WireTypeValue };
  }

  readVarint(): bigint {
    const { value, bytesRead } = decodeVarint(this.view(), 0);
    this.pos += bytesRead;
    return value;
  }

  readSVarint(): bigint {
    return zigzagDecode(this.readVarint());
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const v = decodeFixed32(this.buf, this.pos);
    this.pos += 4;
    return v;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    const v = decodeFixed64(this.buf, this.pos);
    this.pos += 8;
    return v;
  }

  /** Read a length-delimited field as a raw byte slice (zero-copy view). */
  readBytes(): Uint8Array {
    const len = Number(this.readVarint());
    if (len < 0 || !Number.isSafeInteger(len)) {
      throw new MalformedTagError(-1, WireType.LengthDelimited);
    }
    this.need(len, 'length-delimited payload');
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return new TextDecoder('utf-8', { fatal: true }).decode(this.readBytes());
  }

  /** Skip a field of the given wire type (for unknown fields). */
  skip(wireType: WireTypeValue): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.need(8, 'fixed64');
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = Number(this.readVarint());
        this.need(len, 'length-delimited payload');
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.need(4, 'fixed32');
        this.pos += 4;
        return;
      default:
        throw new MalformedTagError(-1, wireType);
    }
  }

  private view(): Uint8Array {
    if (this.pos >= this.end) {
      throw new TruncatedFieldError('varint', 1, 0);
    }
    return this.buf.subarray(this.pos, this.end);
  }
}

export class WireWriter {
  private chunks: Uint8Array[] = [];
  private size = 0;

  private push(bytes: Uint8Array): void {
    this.chunks.push(bytes);
    this.size += bytes.length;
  }

  writeTag(fieldNumber: number, wireType: WireTypeValue): this {
    this.push(encodeVarint((fieldNumber << 3) | wireType));
    return this;
  }

  writeVarint(value: bigint | number): this {
    this.push(encodeVarint(value));
    return this;
  }

  writeFixed32(value: number): this {
    this.push(encodeFixed32(value));
    return this;
  }

  writeFixed64(value: bigint | number): this {
    this.push(encodeFixed64(value));
    return this;
  }

  writeBytes(bytes: Uint8Array): this {
    this.push(encodeVarint(bytes.length));
    this.push(bytes);
    return this;
  }

  writeString(text: string): this {
    return this.writeBytes(new TextEncoder().encode(text));
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
