import { encodeVarint } from './varint.ts';
import type { WireTypeValue } from './reader.ts';

export class WireWriter {
  private readonly bytes: number[] = [];

  writeVarint(value: bigint | number): this {
    for (const b of encodeVarint(value)) this.bytes.push(b);
    return this;
  }

  writeTag(field: number, wireType: WireTypeValue): this {
    if (!Number.isInteger(field) || field < 1 || field > 0x1fffffff) {
      throw new RangeError('非法字段号 ' + field);
    }
    return this.writeVarint((field << 3) | wireType);
  }

  writeFixed32(value: number): this {
    const v = value >>> 0;
    this.bytes.push(v & 0xff, (v >>> 8) & 0xff, (v >>> 16) & 0xff, (v >>> 24) & 0xff);
    return this;
  }

  writeFixed64(value: bigint | number): this {
    let v = BigInt.asUintN(64, BigInt(value));
    for (let i = 0; i < 8; i++) {
      this.bytes.push(Number(v & 0xffn));
      v >>= 8n;
    }
    return this;
  }

  writeFloat(value: number): this {
    const dv = new DataView(new ArrayBuffer(4));
    dv.setFloat32(0, value, true);
    for (let i = 0; i < 4; i++) this.bytes.push(dv.getUint8(i));
    return this;
  }

  writeDouble(value: number): this {
    const dv = new DataView(new ArrayBuffer(8));
    dv.setFloat64(0, value, true);
    for (let i = 0; i < 8; i++) this.bytes.push(dv.getUint8(i));
    return this;
  }

  writeLengthDelimited(data: Uint8Array): this {
    this.writeVarint(data.length);
    for (const b of data) this.bytes.push(b);
    return this;
  }

  get length(): number {
    return this.bytes.length;
  }

  finish(): Uint8Array {
    return Uint8Array.from(this.bytes);
  }
}
