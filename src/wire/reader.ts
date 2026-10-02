import { WireError } from './errors.ts';
import { decodeVarint, MAX_VARINT_BYTES } from './varint.ts';

export const WIRE_VARINT = 0;
export const WIRE_I64 = 1;
export const WIRE_LEN = 2;
export const WIRE_SGROUP = 3;
export const WIRE_EGROUP = 4;
export const WIRE_I32 = 5;

export const MAX_FIELD_NUMBER = 0x1fffffff; // 2^29 - 1

const utf8Decoder = new TextDecoder('utf-8', { fatal: true });

export class WireReader {
  readonly buf: Uint8Array;
  pos: number;
  readonly end: number;

  constructor(buf: Uint8Array, start = 0, end = buf.length) {
    if (start < 0 || end > buf.length || start > end) {
      throw new WireError('TRUNCATED', `invalid reader window [${start}, ${end}) for buffer of ${buf.length}`);
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

  private need(n: number, what: string): void {
    if (this.remaining < n) {
      throw new WireError(
        'TRUNCATED',
        `${what} needs ${n} byte(s) but only ${this.remaining} remain`,
        this.pos,
      );
    }
  }

  readVarint(): bigint {
    const { value, bytesRead } = decodeVarint(
      this.pos + MAX_VARINT_BYTES <= this.end
        ? this.buf
        : this.buf.subarray(this.pos, this.end),
      this.pos + MAX_VARINT_BYTES <= this.end ? this.pos : 0,
    );
    this.pos += bytesRead;
    return value;
  }

  readVarint32(): number {
    return Number(this.readVarint() & 0xffffffffn);
  }

  readTag(): { field: number; wire: number } | null {
    if (this.done) return null;
    const tagOffset = this.pos;
    const tag = this.readVarint32();
    const wire = tag & 0x7;
    const field = tag >>> 3;
    if (wire > WIRE_I32) {
      throw new WireError('INVALID_WIRE_TYPE', `invalid wire type ${wire}`, tagOffset);
    }
    if (field === 0 || field > MAX_FIELD_NUMBER) {
      throw new WireError('INVALID_TAG', `invalid field number ${field}`, tagOffset);
    }
    return { field, wire };
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const p = this.pos;
    const v =
      (this.buf[p] | (this.buf[p + 1] << 8) | (this.buf[p + 2] << 16) | (this.buf[p + 3] << 24)) >>> 0;
    this.pos += 4;
    return v;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    const lo = BigInt(this.readFixed32Raw());
    const hi = BigInt(this.readFixed32Raw());
    return (hi << 32n) | lo;
  }

  private readFixed32Raw(): number {
    const p = this.pos;
    const v =
      (this.buf[p] | (this.buf[p + 1] << 8) | (this.buf[p + 2] << 16) | (this.buf[p + 3] << 24)) >>> 0;
    this.pos += 4;
    return v;
  }

  readFloat(): number {
    this.need(4, 'float');
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4).getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  readDouble(): number {
    this.need(8, 'double');
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8).getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  readBytes(): Uint8Array {
    const lenOffset = this.pos;
    const len = this.readVarint32();
    if (len > this.remaining) {
      throw new WireError(
        'TRUNCATED',
        `length-delimited field declares ${len} byte(s) but only ${this.remaining} remain`,
        lenOffset,
      );
    }
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    const offset = this.pos;
    const bytes = this.readBytes();
    try {
      return utf8Decoder.decode(bytes);
    } catch {
      throw new WireError('INVALID_UTF8', 'string field is not valid UTF-8', offset);
    }
  }

  readSubreader(): WireReader {
    const offset = this.pos;
    const len = this.readVarint32();
    if (len > this.remaining) {
      throw new WireError(
        'TRUNCATED',
        `embedded message declares ${len} byte(s) but only ${this.remaining} remain`,
        offset,
      );
    }
    const sub = new WireReader(this.buf, this.pos, this.pos + len);
    this.pos += len;
    return sub;
  }

  skip(wire: number): void {
    switch (wire) {
      case WIRE_VARINT:
        this.readVarint();
        return;
      case WIRE_I64:
        this.need(8, 'fixed64 skip');
        this.pos += 8;
        return;
      case WIRE_LEN: {
        const lenOffset = this.pos;
        const len = this.readVarint32();
        if (len > this.remaining) {
          throw new WireError(
            'TRUNCATED',
            `skipped length-delimited field declares ${len} byte(s) but only ${this.remaining} remain`,
            lenOffset,
          );
        }
        this.pos += len;
        return;
      }
      case WIRE_I32:
        this.need(4, 'fixed32 skip');
        this.pos += 4;
        return;
      case WIRE_SGROUP: {
        for (;;) {
          const tag = this.readTag();
          if (tag === null) {
            throw new WireError('TRUNCATED', 'unterminated group', this.pos);
          }
          if (tag.wire === WIRE_EGROUP) return;
          this.skip(tag.wire);
        }
      }
      default:
        throw new WireError('INVALID_WIRE_TYPE', `cannot skip wire type ${wire}`, this.pos);
    }
  }
}
