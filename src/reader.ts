import { InvalidWireTypeError, OverflowError, TruncatedError } from './errors.ts';
import { WireType } from './wire-type.ts';

/**
 * Bounds-checked cursor over a protobuf binary buffer.
 *
 * Zero-copy: constructed over a caller buffer (or a subarray view of one)
 * without cloning; `readBytes` returns views into the same memory, which is
 * what makes memory-mapped / nested-message parsing allocation-free.
 * Instances are single-use cursors and hold no shared mutable state, so
 * independent readers over the same buffer are safe to run concurrently.
 */
export class WireReader {
  readonly buf: Uint8Array;
  private readonly view: DataView;
  pos: number;
  private readonly end: number;

  constructor(buf: Uint8Array, start = 0, end = buf.length) {
    if (start < 0 || end < start || end > buf.length) {
      throw new TruncatedError(`invalid window [${start}, ${end}) for buffer of ${buf.length} bytes`, start);
    }
    this.buf = buf;
    this.view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
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
      throw new TruncatedError(
        `${what} needs ${n} byte(s) but only ${this.remaining} remain`, this.pos,
      );
    }
  }

  /** Unsigned LEB128 varint, at most 10 bytes / 64 payload bits. */
  varint64(): bigint {
    let result = 0n;
    let shift = 0n;
    for (let i = 0; i < 10; i++) {
      if (this.pos >= this.end) {
        throw new TruncatedError('varint truncated mid-sequence', this.pos);
      }
      const b = this.buf[this.pos++];
      if (i === 9 && b > 1) {
        throw new OverflowError('varint exceeds 64 bits', this.pos - 1);
      }
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) return result;
      shift += 7n;
    }
    throw new OverflowError('varint longer than 10 bytes', this.pos);
  }

  uint32(): number {
    return Number(BigInt.asUintN(32, this.varint64()));
  }

  int32(): number {
    return Number(BigInt.asIntN(32, this.varint64()));
  }

  uint64(): bigint {
    return BigInt.asUintN(64, this.varint64());
  }

  int64(): bigint {
    return BigInt.asInt64(64, this.varint64());
  }

  bool(): boolean {
    return this.varint64() !== 0n;
  }

  zigzag32(): number {
    const v = this.uint32();
    return (v >>> 1) ^ -(v & 1);
  }

  zigzag64(): bigint {
    const v = this.uint64();
    return BigInt.asIntN(64, (v >> 1n) ^ -(v & 1n));
  }

  fixed32(): number {
    this.need(4, 'fixed32');
    const v = this.view.getUint32(this.pos, true);
    this.pos += 4;
    return v;
  }

  sfixed32(): number {
    this.need(4, 'sfixed32');
    const v = this.view.getInt32(this.pos, true);
    this.pos += 4;
    return v;
  }

  float(): number {
    this.need(4, 'float');
    const v = this.view.getFloat32(this.pos, true);
    this.pos += 4;
    return v;
  }

  fixed64(): bigint {
    this.need(8, 'fixed64');
    const v = this.view.getBigUint64(this.pos, true);
    this.pos += 8;
    return v;
  }

  sfixed64(): bigint {
    this.need(8, 'sfixed64');
    const v = this.view.getBigInt64(this.pos, true);
    this.pos += 8;
    return v;
  }

  double(): number {
    this.need(8, 'double');
    const v = this.view.getFloat64(this.pos, true);
    this.pos += 8;
    return v;
  }

  /**
   * Length-delimited payload as a zero-copy view into the source buffer.
   * The declared length is validated against the remaining window before
   * any cursor movement, so a hostile length prefix cannot cause an
   * out-of-bounds read or a large allocation.
   */
  bytes(): Uint8Array {
    const len = this.uint32();
    this.need(len, `length-delimited field of ${len} byte(s)`);
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  string(): string {
    return new TextDecoder('utf-8', { fatal: true }).decode(this.bytes());
  }

  /** Field tag; returns [fieldNumber, wireType]. */
  tag(): [number, number] {
    const t = this.uint32();
    const fieldNo = t >>> 3;
    const wireType = t & 0x7;
    if (fieldNo < 1 || fieldNo > 0x1fffffff) {
      throw new OverflowError(`field number ${fieldNo} out of range`, this.pos);
    }
    return [fieldNo, wireType];
  }

  /** Skip one field of the given wire type (forward compatibility). */
  skip(wireType: number): void {
    switch (wireType) {
      case WireType.Varint:
        this.varint64();
        return;
      case WireType.Fixed64:
        this.need(8, 'fixed64 skip');
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = this.uint32();
        this.need(len, `length-delimited skip of ${len} byte(s)`);
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.need(4, 'fixed32 skip');
        this.pos += 4;
        return;
      default:
        throw new InvalidWireTypeError(`unsupported wire type ${wireType}`, this.pos);
    }
  }
}
