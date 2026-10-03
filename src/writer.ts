import { encodeVarintInto } from './varint.ts';

const textEncoder = new TextEncoder();
const scratchView = new DataView(new ArrayBuffer(8));

export class WireWriter {
  private buf: Uint8Array;
  private len = 0;

  constructor(initialCapacity = 256) {
    this.buf = new Uint8Array(Math.max(16, initialCapacity));
  }

  get length(): number {
    return this.len;
  }

  private ensure(extra: number): void {
    if (this.len + extra <= this.buf.length) return;
    let capacity = this.buf.length * 2;
    while (capacity < this.len + extra) capacity *= 2;
    const next = new Uint8Array(capacity);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  writeVarint(value: number | bigint): this {
    this.ensure(10);
    this.len += encodeVarintInto(this.buf, this.len, value);
    return this;
  }

  writeTag(fieldNumber: number, wireType: number): this {
    return this.writeVarint((fieldNumber << 3) | wireType);
  }

  writeFixed32(value: number): this {
    this.ensure(4);
    const v = value >>> 0;
    this.buf[this.len] = v & 0xff;
    this.buf[this.len + 1] = (v >>> 8) & 0xff;
    this.buf[this.len + 2] = (v >>> 16) & 0xff;
    this.buf[this.len + 3] = (v >>> 24) & 0xff;
    this.len += 4;
    return this;
  }

  writeFixed64(value: bigint): this {
    this.ensure(8);
    let v = BigInt.asUintN(64, value);
    for (let i = 0; i < 8; i += 1) {
      this.buf[this.len + i] = Number(v & 0xffn);
      v >>= 8n;
    }
    this.len += 8;
    return this;
  }

  writeFloat(value: number): this {
    this.ensure(4);
    scratchView.setFloat32(0, value, true);
    this.buf.set(new Uint8Array(scratchView.buffer, 0, 4), this.len);
    this.len += 4;
    return this;
  }

  writeDouble(value: number): this {
    this.ensure(8);
    scratchView.setFloat64(0, value, true);
    this.buf.set(new Uint8Array(scratchView.buffer, 0, 8), this.len);
    this.len += 8;
    return this;
  }

  writeBytes(data: Uint8Array): this {
    this.ensure(data.length);
    this.buf.set(data, this.len);
    this.len += data.length;
    return this;
  }

  writeString(value: string): this {
    return this.writeBytes(textEncoder.encode(value));
  }

  writeLengthDelimited(data: Uint8Array): this {
    this.writeVarint(data.length);
    return this.writeBytes(data);
  }

  finish(): Uint8Array {
    return this.buf.slice(0, this.len);
  }

  view(): Uint8Array {
    return this.buf.subarray(0, this.len);
  }
}
