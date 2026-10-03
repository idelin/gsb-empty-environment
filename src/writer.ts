import { encodeVarintInto, varintByteLength, zigzagEncode } from './encoding.ts';
import { WireType, type WireTypeValue } from './reader.ts';

export class ProtoWriter {
  private buf: Uint8Array;
  private len = 0;

  constructor(initialCapacity = 256) {
    this.buf = new Uint8Array(initialCapacity);
  }

  get byteLength(): number {
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

  writeRawBytes(bytes: Uint8Array): this {
    this.ensure(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
    return this;
  }

  writeTag(fieldNumber: number, wireType: WireTypeValue): this {
    return this.writeVarint(BigInt(fieldNumber << 3 | wireType));
  }

  writeVarint(value: bigint | number): this {
    const v = typeof value === 'number' ? BigInt(value) : value;
    this.ensure(10);
    this.len += encodeVarintInto(this.buf, this.len, v);
    return this;
  }

  writeZigZag(value: bigint): this {
    return this.writeVarint(zigzagEncode(value));
  }

  writeFixed32(value: number): this {
    this.ensure(4);
    this.buf[this.len] = value & 0xff;
    this.buf[this.len + 1] = (value >>> 8) & 0xff;
    this.buf[this.len + 2] = (value >>> 16) & 0xff;
    this.buf[this.len + 3] = (value >>> 24) & 0xff;
    this.len += 4;
    return this;
  }

  writeFixed64(value: bigint): this {
    this.writeFixed32(Number(value & 0xffffffffn));
    this.writeFixed32(Number((value >> 32n) & 0xffffffffn));
    return this;
  }

  writeFloat(value: number): this {
    this.ensure(4);
    new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 4).setFloat32(
      0,
      value,
      true,
    );
    this.len += 4;
    return this;
  }

  writeDouble(value: number): this {
    this.ensure(8);
    new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 8).setFloat64(
      0,
      value,
      true,
    );
    this.len += 8;
    return this;
  }

  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    return this.writeRawBytes(bytes);
  }

  writeString(text: string): this {
    return this.writeBytes(new TextEncoder().encode(text));
  }

  writeVarintField(fieldNumber: number, value: bigint | number): this {
    return this.writeTag(fieldNumber, WireType.Varint).writeVarint(value);
  }

  writeZigZagField(fieldNumber: number, value: bigint): this {
    return this.writeTag(fieldNumber, WireType.Varint).writeZigZag(value);
  }

  writeFixed32Field(fieldNumber: number, value: number): this {
    return this.writeTag(fieldNumber, WireType.Fixed32).writeFixed32(value);
  }

  writeFixed64Field(fieldNumber: number, value: bigint): this {
    return this.writeTag(fieldNumber, WireType.Fixed64).writeFixed64(value);
  }

  writeBytesField(fieldNumber: number, bytes: Uint8Array): this {
    return this.writeTag(fieldNumber, WireType.LengthDelimited).writeBytes(bytes);
  }

  writeStringField(fieldNumber: number, text: string): this {
    return this.writeTag(fieldNumber, WireType.LengthDelimited).writeString(text);
  }

  writeMessageField(fieldNumber: number, message: Uint8Array): this {
    return this.writeBytesField(fieldNumber, message);
  }

  writePackedVarintField(fieldNumber: number, values: Array<bigint | number>): this {
    const inner = new ProtoWriter(values.length * 2);
    for (const v of values) inner.writeVarint(v);
    return this.writeBytesField(fieldNumber, inner.finish());
  }

  static varintLength(value: bigint | number): number {
    return varintByteLength(typeof value === 'number' ? BigInt(value) : value);
  }

  finish(): Uint8Array {
    return this.buf.slice(0, this.len);
  }
}
