import { zigZagEncode32, zigZagEncode64 } from './zigzag.ts';

/** Growable encoder used by tests, the demo, and round-trip verification. */
export class BufferWriter {
  private bytes: number[] = [];

  get length(): number {
    return this.bytes.length;
  }

  writeByte(b: number): this {
    this.bytes.push(b & 0xff);
    return this;
  }

  writeVarint(value: bigint | number): this {
    let v = BigInt(value) & 0xffffffffffffffffn;
    while (v > 0x7fn) {
      this.bytes.push(Number(v & 0x7fn) | 0x80);
      v >>= 7n;
    }
    this.bytes.push(Number(v));
    return this;
  }

  writeZigZag32(value: number): this {
    return this.writeVarint(zigZagEncode32(value));
  }

  writeZigZag64(value: bigint): this {
    return this.writeVarint(zigZagEncode64(value));
  }

  writeFixed32(value: number): this {
    for (let i = 0; i < 4; i++) this.bytes.push((value >>> (i * 8)) & 0xff);
    return this;
  }

  writeFixed64(value: bigint): this {
    let v = BigInt.asUintN(64, value);
    for (let i = 0; i < 8; i++) {
      this.bytes.push(Number(v & 0xffn));
      v >>= 8n;
    }
    return this;
  }

  writeFloat(value: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setFloat32(0, value, true);
    for (const x of b) this.bytes.push(x);
    return this;
  }

  writeDouble(value: number): this {
    const b = new Uint8Array(8);
    new DataView(b.buffer).setFloat64(0, value, true);
    for (const x of b) this.bytes.push(x);
    return this;
  }

  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(bytes.byteLength);
    for (const b of bytes) this.bytes.push(b);
    return this;
  }

  writeString(value: string): this {
    return this.writeBytes(new TextEncoder().encode(value));
  }

  writeTag(fieldNumber: number, wireType: number): this {
    return this.writeVarint((fieldNumber << 3) | wireType);
  }

  finish(): Uint8Array {
    return Uint8Array.from(this.bytes);
  }
}
