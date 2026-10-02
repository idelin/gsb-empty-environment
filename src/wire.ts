import { DecodeError, EncodeError } from './errors.ts';
import { decodeVarint, encodeVarint } from './varint.ts';

/** Protobuf wire types (low 3 bits of a tag). */
export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  StartGroup: 3,
  EndGroup: 4,
  Fixed32: 5,
} as const;
export type WireType = (typeof WireType)[keyof typeof WireType];

export interface Tag {
  fieldNumber: number;
  wireType: WireType;
}

/** Bounds-checked cursor over a protobuf binary buffer. Never reads out of range. */
export class WireReader {
  private readonly view: DataView;
  private readonly base: number;
  private readonly buf: Uint8Array;
  private pos: number;
  readonly length: number;

  constructor(buf: Uint8Array, offset = 0, length = buf.length - offset) {
    if (offset < 0 || length < 0 || offset + length > buf.length) {
      throw new DecodeError('reader window out of bounds', offset);
    }
    this.buf = buf;
    this.base = offset;
    this.view = new DataView(buf.buffer, buf.byteOffset + offset, length);
    this.pos = 0;
    this.length = length;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.length - this.pos;
  }

  get eof(): boolean {
    return this.pos >= this.length;
  }

  private need(n: number, what: string): void {
    if (this.remaining < n) {
      throw new DecodeError(`${what}: truncated (need ${n} byte(s), have ${this.remaining})`, this.pos);
    }
  }

  readVarint(): bigint {
    // Bound the view to this reader's window so nested sub-readers cannot
    // consume bytes belonging to the enclosing message.
    const { value, bytesRead } = decodeVarint(
      this.buf.subarray(this.base, this.base + this.length),
      this.pos,
    );
    this.pos += bytesRead;
    return value;
  }

  /** Zero-copy view of the window's bytes in [start, end) relative to this reader. */
  slice(start: number, end: number): Uint8Array {
    if (start < 0 || end < start || end > this.length) {
      throw new DecodeError('slice out of bounds', start);
    }
    return this.buf.subarray(this.base + start, this.base + end);
  }

  readTag(): Tag {
    const raw = this.readVarint();
    if (raw > 0xffff_ffffn) {
      throw new DecodeError('tag: exceeds 32 bits', this.pos);
    }
    const tag = Number(raw);
    const fieldNumber = tag >>> 3;
    const wireType = (tag & 0x7) as WireType;
    if (fieldNumber === 0) {
      throw new DecodeError('tag: field number 0 is invalid', this.pos);
    }
    if (wireType === WireType.StartGroup || wireType === WireType.EndGroup) {
      throw new DecodeError('tag: groups are not supported', this.pos);
    }
    return { fieldNumber, wireType };
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

  /** Read a length-delimited slice. The declared length is bounds-checked. */
  readBytes(): Uint8Array {
    const lenBig = this.readVarint();
    if (lenBig > BigInt(this.remaining)) {
      throw new DecodeError(
        `bytes: declared length ${lenBig} exceeds remaining ${this.remaining}`,
        this.pos,
      );
    }
    const len = Number(lenBig);
    const out = this.buf.subarray(this.base + this.pos, this.base + this.pos + len);
    this.pos += len;
    return out;
  }

  /** Read a length-delimited nested message as an independent reader. */
  readSubReader(): WireReader {
    const bytes = this.readBytes();
    return new WireReader(bytes);
  }

  /** Skip a field of the given wire type, with full bounds checking. */
  skip(wireType: WireType): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint();
        return;
      case WireType.Fixed64:
        this.need(8, 'skip fixed64');
        this.pos += 8;
        return;
      case WireType.LengthDelimited:
        this.readBytes();
        return;
      case WireType.Fixed32:
        this.need(4, 'skip fixed32');
        this.pos += 4;
        return;
      default:
        throw new DecodeError(`skip: unknown wire type ${wireType}`, this.pos);
    }
  }
}

/** Growable protobuf binary writer. */
export class WireWriter {
  private chunks: Uint8Array[] = [];
  private size = 0;

  writeVarint(value: bigint | number): this {
    return this.writeRaw(encodeVarint(value));
  }

  writeTag(fieldNumber: number, wireType: WireType): this {
    if (!Number.isInteger(fieldNumber) || fieldNumber < 1 || fieldNumber > 0x1fff_ffff) {
      throw new EncodeError(`invalid field number: ${fieldNumber}`);
    }
    return this.writeVarint((fieldNumber << 3) | wireType);
  }

  writeFixed32(value: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setUint32(0, value >>> 0, true);
    return this.writeRaw(b);
  }

  writeSFixed32(value: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setInt32(0, value | 0, true);
    return this.writeRaw(b);
  }

  writeFloat(value: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setFloat32(0, value, true);
    return this.writeRaw(b);
  }

  writeFixed64(value: bigint): this {
    const b = new Uint8Array(8);
    new DataView(b.buffer).setBigUint64(0, BigInt.asUintN(64, value), true);
    return this.writeRaw(b);
  }

  writeSFixed64(value: bigint): this {
    const b = new Uint8Array(8);
    new DataView(b.buffer).setBigInt64(0, BigInt.asIntN(64, value), true);
    return this.writeRaw(b);
  }

  writeDouble(value: number): this {
    const b = new Uint8Array(8);
    new DataView(b.buffer).setFloat64(0, value, true);
    return this.writeRaw(b);
  }

  writeBytes(data: Uint8Array): this {
    this.writeVarint(data.length);
    return this.writeRaw(data);
  }

  writeRaw(data: Uint8Array): this {
    this.chunks.push(data);
    this.size += data.length;
    return this;
  }

  finish(): Uint8Array {
    const out = new Uint8Array(this.size);
    let off = 0;
    for (const c of this.chunks) {
      out.set(c, off);
      off += c.length;
    }
    return out;
  }
}
