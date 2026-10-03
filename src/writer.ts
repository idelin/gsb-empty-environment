import { ProtobufEncodeError } from './errors.ts';
import { encodeVarint, zigzagEncode32, zigzagEncode64 } from './varint.ts';
import { WIRE_VARINT, WIRE_FIXED64, WIRE_LENGTH_DELIMITED, WIRE_FIXED32 } from './reader.ts';

const MASK_64 = (1n << 64n) - 1n;

/** 可增长的二进制写入器。 */
export class WireWriter {
  private buf: Uint8Array;
  private len = 0;

  constructor(capacity = 256) {
    this.buf = new Uint8Array(capacity);
  }

  get size(): number {
    return this.len;
  }

  private ensure(n: number): void {
    if (this.len + n <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < this.len + n) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  writeByte(b: number): this {
    this.ensure(1);
    this.buf[this.len++] = b & 0xff;
    return this;
  }

  writeRaw(bytes: Uint8Array): this {
    this.ensure(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
    return this;
  }

  writeVarint(value: number | bigint): this {
    return this.writeRaw(encodeVarint(value));
  }

  writeSVarint32(value: number): this {
    return this.writeVarint(zigzagEncode32(value));
  }

  writeSVarint64(value: bigint): this {
    return this.writeVarint(zigzagEncode64(value));
  }

  writeTag(fieldNumber: number, wireType: number): this {
    if (fieldNumber <= 0 || !Number.isInteger(fieldNumber)) {
      throw new ProtobufEncodeError(`Invalid field number ${fieldNumber}`);
    }
    return this.writeVarint((fieldNumber << 3) | wireType);
  }

  writeFixed32(value: number): this {
    this.ensure(4);
    const v = value >>> 0;
    this.buf[this.len++] = v & 0xff;
    this.buf[this.len++] = (v >>> 8) & 0xff;
    this.buf[this.len++] = (v >>> 16) & 0xff;
    this.buf[this.len++] = (v >>> 24) & 0xff;
    return this;
  }

  writeFixed64(value: bigint): this {
    this.ensure(8);
    let v = value & MASK_64;
    for (let i = 0; i < 8; i++) {
      this.buf[this.len++] = Number(v & 0xffn);
      v >>= 8n;
    }
    return this;
  }

  writeFloat(value: number): this {
    this.ensure(4);
    new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 4).setFloat32(0, value, true);
    this.len += 4;
    return this;
  }

  writeDouble(value: number): this {
    this.ensure(8);
    new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 8).setFloat64(0, value, true);
    this.len += 8;
    return this;
  }

  /** 写入 length-delimited 段。 */
  writeLengthDelimited(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    return this.writeRaw(bytes);
  }

  finish(): Uint8Array {
    return this.buf.slice(0, this.len);
  }
}

export { WIRE_VARINT, WIRE_FIXED64, WIRE_LENGTH_DELIMITED, WIRE_FIXED32 };
