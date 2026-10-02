import { MalformedMessageError, TruncatedMessageError } from './errors.ts';
import { decodeVarint } from './varint.ts';

/** Protobuf wire types (lower 3 bits of every tag). */
export const WireType = {
  VARINT: 0,
  FIXED64: 1,
  LEN: 2,
  SGROUP: 3,
  EGROUP: 4,
  FIXED32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export interface Tag {
  fieldNumber: number;
  wireType: WireTypeValue;
}

export const MAX_FIELD_NUMBER = (1 << 29) - 1;

const textDecoder = new TextDecoder('utf-8', { fatal: true });

/**
 * Bounds-checked cursor over a protobuf binary buffer.
 * Every read validates remaining capacity BEFORE touching bytes,
 * so a truncated field can never cause an out-of-bounds access.
 */
export class WireReader {
  readonly buf: Uint8Array;
  pos: number;
  readonly end: number;

  constructor(buf: Uint8Array, start = 0, end = buf.length) {
    if (start < 0 || end < start || end > buf.length) {
      throw new RangeError(`invalid window [${start}, ${end}) for buffer of ${buf.length} bytes`);
    }
    this.buf = buf;
    this.pos = start;
    this.end = end;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.end;
  }

  private require(needed: number, what: string): void {
    if (needed > this.remaining) {
      throw new TruncatedMessageError(this.pos, needed, this.remaining, what);
    }
  }

  readByte(): number {
    this.require(1, 'byte');
    return this.buf[this.pos++];
  }

  readVarint(): bigint {
    const view = this.buf.subarray(0, this.end);
    const { value, bytesRead } = decodeVarint(view, this.pos);
    this.pos += bytesRead;
    return value;
  }

  /** Varint as a JS number; refuses values beyond the safe-integer range. */
  readVarintNumber(): number {
    const v = this.readVarint();
    if (v > BigInt(Number.MAX_SAFE_INTEGER)) {
      throw new MalformedMessageError(`varint ${v} exceeds Number.MAX_SAFE_INTEGER`, this.pos);
    }
    return Number(v);
  }

  readFixed32(): number {
    this.require(4, 'fixed32');
    const p = this.pos;
    this.pos += 4;
    return (
      (this.buf[p] | (this.buf[p + 1] << 8) | (this.buf[p + 2] << 16) | (this.buf[p + 3] << 24)) >>> 0
    );
  }

  readFixed64(): bigint {
    this.require(8, 'fixed64');
    let v = 0n;
    for (let i = 0; i < 8; i++) v |= BigInt(this.buf[this.pos + i]) << BigInt(8 * i);
    this.pos += 8;
    return v;
  }

  readFloat(): number {
    this.require(4, 'float');
    const dv = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4);
    this.pos += 4;
    return dv.getFloat32(0, true);
  }

  readDouble(): number {
    this.require(8, 'double');
    const dv = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8);
    this.pos += 8;
    return dv.getFloat64(0, true);
  }

  readBytes(length: number): Uint8Array {
    if (!Number.isInteger(length) || length < 0) {
      throw new MalformedMessageError(`invalid byte length ${length}`, this.pos);
    }
    this.require(length, 'bytes');
    const out = this.buf.subarray(this.pos, this.pos + length);
    this.pos += length;
    return out;
  }

  readLengthDelimited(): Uint8Array {
    const length = this.readVarintNumber();
    return this.readBytes(length);
  }

  readString(): string {
    const bytes = this.readLengthDelimited();
    try {
      return textDecoder.decode(bytes);
    } catch {
      throw new MalformedMessageError('invalid UTF-8 in string field', this.pos - bytes.length);
    }
  }

  readTag(): Tag {
    const raw = this.readVarintNumber();
    const wireType = (raw & 0x7) as WireTypeValue;
    const fieldNumber = raw >>> 3;
    if (fieldNumber < 1 || fieldNumber > MAX_FIELD_NUMBER) {
      throw new MalformedMessageError(`invalid field number ${fieldNumber}`, this.pos);
    }
    if (
      wireType === WireType.SGROUP ||
      wireType === WireType.EGROUP ||
      wireType > WireType.FIXED32
    ) {
      throw new MalformedMessageError(`unsupported wire type ${wireType}`, this.pos);
    }
    return { fieldNumber, wireType };
  }

  /** Skip a field of the given wire type (used for unknown fields). */
  skipField(wireType: WireTypeValue): void {
    switch (wireType) {
      case WireType.VARINT:
        this.readVarint();
        return;
      case WireType.FIXED64:
        this.require(8, 'fixed64');
        this.pos += 8;
        return;
      case WireType.LEN: {
        const length = this.readVarintNumber();
        this.require(length, 'length-delimited payload');
        this.pos += length;
        return;
      }
      case WireType.FIXED32:
        this.require(4, 'fixed32');
        this.pos += 4;
        return;
      default:
        throw new MalformedMessageError(`cannot skip wire type ${wireType}`, this.pos);
    }
  }
}
