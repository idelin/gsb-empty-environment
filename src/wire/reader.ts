import { DecodeError } from './errors.ts';
import { zigZagDecode32, zigZagDecode64 } from './zigzag.ts';

export const MAX_VARINT_BYTES = 10;

/**
 * Bounds-checked cursor over a protobuf wire-format byte buffer.
 * Every read validates remaining capacity and reports ERR_TRUNCATED
 * instead of ever reading out of bounds.
 */
export class BufferReader {
  private buf: Uint8Array | null;
  private view: DataView;
  private pos: number;
  private readonly limit: number;
  private readonly start: number;

  constructor(buf: Uint8Array, offset = 0, length?: number) {
    const len = length ?? buf.byteLength - offset;
    if (offset < 0 || len < 0 || offset + len > buf.byteLength) {
      throw new DecodeError('ERR_TRUNCATED', 'reader window exceeds buffer', offset);
    }
    this.buf = buf;
    this.start = offset;
    this.view = new DataView(buf.buffer, buf.byteOffset + offset, len);
    this.pos = 0;
    this.limit = len;
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

  /** Drop the reference to the underlying buffer (leak guard). */
  release(): void {
    this.buf = null;
  }

  get released(): boolean {
    return this.buf === null;
  }

  private need(n: number, what: string): void {
    if (this.pos + n > this.limit) {
      throw new DecodeError(
        'ERR_TRUNCATED',
        `${what}: need ${n} byte(s), only ${this.limit - this.pos} remain`,
        this.pos,
      );
    }
  }

  readByte(): number {
    this.need(1, 'byte');
    return this.view.getUint8(this.pos++);
  }

  /** 7-bit-per-group varint, up to 64 bits. Returns bigint to stay lossless. */
  readVarint(): bigint {
    let result = 0n;
    for (let shift = 0n; shift < 70n; shift += 7n) {
      if (this.pos >= this.limit) {
        throw new DecodeError('ERR_TRUNCATED', 'varint truncated mid-sequence', this.pos);
      }
      const b = this.view.getUint8(this.pos++);
      if (shift === 63n) {
        // 10th byte: only the lowest bit may be set for a 64-bit value.
        if (b > 1) {
          throw new DecodeError('ERR_VARINT_OVERFLOW', 'varint exceeds 64 bits', this.pos - 1);
        }
        result |= BigInt(b) << shift;
        return result;
      }
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) {
        return result;
      }
    }
    throw new DecodeError('ERR_VARINT_OVERFLOW', 'varint exceeds 10 bytes', this.pos);
  }

  readVarint32(): number {
    return Number(this.readVarint() & 0xffffffffn) >>> 0;
  }

  readSVarint32(): number {
    return zigZagDecode32(this.readVarint32());
  }

  readSVarint64(): bigint {
    return zigZagDecode64(this.readVarint());
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const v = this.view.getUint32(this.pos, true);
    this.pos += 4;
    return v;
  }

  readSFixed32(): number {
    this.need(4, 'sfixed32');
    const v = this.view.getInt32(this.pos, true);
    this.pos += 4;
    return v;
  }

  readFloat(): number {
    this.need(4, 'float');
    const v = this.view.getFloat32(this.pos, true);
    this.pos += 4;
    return v;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    const v = this.view.getBigUint64(this.pos, true);
    this.pos += 8;
    return v;
  }

  readSFixed64(): bigint {
    this.need(8, 'sfixed64');
    const v = this.view.getBigInt64(this.pos, true);
    this.pos += 8;
    return v;
  }

  readDouble(): number {
    this.need(8, 'double');
    const v = this.view.getFloat64(this.pos, true);
    this.pos += 8;
    return v;
  }

  /** Length-delimited payload; returns a zero-copy subarray view. */
  readBytes(): Uint8Array {
    const start = this.pos;
    const lenBig = this.readVarint();
    if (lenBig > BigInt(Number.MAX_SAFE_INTEGER)) {
      throw new DecodeError('ERR_NEGATIVE_LENGTH', 'length prefix not representable', start);
    }
    const len = Number(lenBig);
    this.need(len, `length-delimited payload of ${len} bytes`);
    const out = (this.buf as Uint8Array).subarray(this.start + this.pos, this.start + this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    const bytes = this.readBytes();
    const td = new TextDecoder('utf-8', { fatal: true });
    try {
      return td.decode(bytes);
    } catch {
      throw new DecodeError('ERR_INVALID_UTF8', 'invalid UTF-8 in string field', this.pos - bytes.byteLength);
    }
  }

  /** Bounds-checked skip of an unknown field by wire type. */
  skip(wireType: number): void {
    switch (wireType) {
      case 0: {
        const before = this.pos;
        this.readVarint();
        void before;
        return;
      }
      case 1:
        this.need(8, 'skip fixed64');
        this.pos += 8;
        return;
      case 2: {
        const start = this.pos;
        const len = Number(this.readVarint());
        if (this.pos + len > this.limit) {
          throw new DecodeError('ERR_TRUNCATED', `skip: ${len} bytes exceed remaining`, start);
        }
        this.pos += len;
        return;
      }
      case 5:
        this.need(4, 'skip fixed32');
        this.pos += 4;
        return;
      default:
        throw new DecodeError('ERR_INVALID_WIRE_TYPE', `cannot skip wire type ${wireType}`, this.pos);
    }
  }
}
