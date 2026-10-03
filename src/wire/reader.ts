import { DecodeError, TruncatedFieldError } from './errors.ts';
import { decodeVarint } from './varint.ts';
import { WireType, isValidWireType, wireTypeName } from './types.ts';

export interface Tag {
  fieldNumber: number;
  wireType: WireType;
}

export class WireReader {
  pos: number;
  readonly end: number;
  readonly buf: Uint8Array;

  constructor(buf: Uint8Array, start = 0, end: number = buf.length) {
    if (start < 0 || end > buf.length || start > end) {
      throw new TruncatedFieldError('reader window', start, end - start, buf.length - start);
    }
    this.buf = buf;
    this.pos = start;
    this.end = end;
  }

  static of(buf: Uint8Array): WireReader {
    return new WireReader(buf);
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get done(): boolean {
    return this.pos >= this.end;
  }

  private need(n: number, what: string): void {
    if (this.remaining < n) {
      throw new TruncatedFieldError(what, this.pos, n, this.remaining);
    }
  }

  readVarint(): bigint {
    const { value, bytesRead } = decodeVarint(this.buf, this.pos, this.end);
    this.pos += bytesRead;
    return value;
  }

  readTag(): Tag {
    const raw = this.readVarint();
    const fieldNumber = Number(raw >> 3n);
    const wireType = Number(raw & 7n);
    if (fieldNumber <= 0) {
      throw new DecodeError(`invalid field number ${fieldNumber} at offset ${this.pos}`);
    }
    if (!isValidWireType(wireType)) {
      throw new DecodeError(
        `unsupported wire type ${wireType} for field ${fieldNumber} at offset ${this.pos}`,
      );
    }
    return { fieldNumber, wireType };
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const b = this.buf;
    const p = this.pos;
    this.pos += 4;
    return (b[p] | (b[p + 1] << 8) | (b[p + 2] << 16) | (b[p + 3] << 24)) >>> 0;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    const lo = BigInt(this.readFixed32());
    const hi = BigInt(this.readFixed32());
    return (hi << 32n) | lo;
  }

  readFloat(): number {
    this.need(4, 'float');
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4);
    this.pos += 4;
    return view.getFloat32(0, true);
  }

  readDouble(): number {
    this.need(8, 'double');
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8);
    this.pos += 8;
    return view.getFloat64(0, true);
  }

  readBytes(n: number): Uint8Array {
    if (!Number.isInteger(n) || n < 0) {
      throw new DecodeError(`invalid byte length ${n} at offset ${this.pos}`);
    }
    this.need(n, 'bytes');
    const out = this.buf.subarray(this.pos, this.pos + n);
    this.pos += n;
    return out;
  }

  readLengthDelimited(): Uint8Array {
    const len = this.readVarint();
    if (len > BigInt(this.remaining)) {
      throw new TruncatedFieldError(
        'length-delimited payload',
        this.pos,
        Number(len),
        this.remaining,
      );
    }
    return this.readBytes(Number(len));
  }

  readSubreader(): WireReader {
    const payload = this.readLengthDelimited();
    return new WireReader(payload, 0, payload.length);
  }

  skipField(wireType: WireType): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.need(8, 'fixed64 skip');
        this.pos += 8;
        return;
      case WireType.LengthDelimited:
        this.readLengthDelimited();
        return;
      case WireType.Fixed32:
        this.need(4, 'fixed32 skip');
        this.pos += 4;
        return;
      default:
        throw new DecodeError(`cannot skip wire type ${wireTypeName(wireType)}`);
    }
  }
}
