import { ProtobufDecodeError } from './errors.ts';
import { readVarint } from './varint.ts';

/** Protobuf wire types (3-bit tag suffix). */
export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  StartGroup: 3, // deprecated, rejected by this parser
  EndGroup: 4, // deprecated, rejected by this parser
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

/**
 * Bounds-checked cursor over a protobuf binary buffer. Every read validates
 * the remaining length before touching memory, so truncated fields surface as
 * ProtobufDecodeError instead of out-of-bounds access.
 */
export class WireReader {
  private readonly buf: Uint8Array;
  private readonly limit: number;
  private pos: number;

  constructor(buf: Uint8Array, offset = 0, length?: number) {
    const end = length === undefined ? buf.length : offset + length;
    if (offset < 0 || end > buf.length || end < offset) {
      throw new ProtobufDecodeError(
        `invalid reader window [${offset}, ${end}) for buffer of length ${buf.length}`,
        offset,
      );
    }
    this.buf = buf;
    this.pos = offset;
    this.limit = end;
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

  private need(bytes: number, what: string): void {
    if (this.remaining < bytes) {
      throw new ProtobufDecodeError(
        `truncated ${what}: need ${bytes} byte(s), only ${this.remaining} left`,
        this.pos,
      );
    }
  }

  /** Read a tag; returns field number (>0) and wire type. */
  readTag(): { fieldNumber: number; wireType: WireTypeValue } {
    const { value, offset } = readVarint(this.buf, this.pos);
    this.pos = offset;
    const raw = Number(value & 0xffffffffn);
    const wireType = (raw & 0x7) as WireTypeValue;
    const fieldNumber = raw >>> 3;
    if (fieldNumber === 0) {
      throw new ProtobufDecodeError('invalid tag: field number 0 is reserved', this.pos);
    }
    if (fieldNumber > 0x1fffffff) {
      throw new ProtobufDecodeError(`invalid tag: field number ${fieldNumber} exceeds 2^29-1`, this.pos);
    }
    if (wireType === WireType.StartGroup || wireType === WireType.EndGroup) {
      throw new ProtobufDecodeError(`unsupported wire type ${wireType} (groups are deprecated)`, this.pos);
    }
    if (wireType > WireType.Fixed32) {
      throw new ProtobufDecodeError(`invalid wire type ${wireType}`, this.pos);
    }
    return { fieldNumber, wireType };
  }

  readVarint(): bigint {
    const { value, offset } = readVarint(this.buf, this.pos);
    this.pos = offset;
    return value;
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const v =
      this.buf[this.pos] |
      (this.buf[this.pos + 1] << 8) |
      (this.buf[this.pos + 2] << 16) |
      (this.buf[this.pos + 3] << 24);
    this.pos += 4;
    return v >>> 0;
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    let v = 0n;
    for (let i = 7; i >= 0; i--) {
      v = (v << 8n) | BigInt(this.buf[this.pos + i]);
    }
    this.pos += 8;
    return v;
  }

  /** Read a length-delimited slice (zero-copy view into the source buffer). */
  readBytes(): Uint8Array {
    const len = Number(this.readVarint());
    if (len > 0x7fffffff) {
      throw new ProtobufDecodeError(`length-delimited field too large: ${len}`, this.pos);
    }
    this.need(len, 'length-delimited field');
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  /** Skip a field of the given wire type. Returns false for groups (unsupported). */
  skipField(wireType: WireTypeValue): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.need(8, 'fixed64 (skip)');
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = Number(this.readVarint());
        this.need(len, 'length-delimited field (skip)');
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.need(4, 'fixed32 (skip)');
        this.pos += 4;
        return;
      default:
        throw new ProtobufDecodeError(`cannot skip wire type ${wireType}`, this.pos);
    }
  }
}
