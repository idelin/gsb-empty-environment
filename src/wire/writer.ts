import { encodeVarint } from './varint.ts';

export class WireWriter {
  private chunks: Uint8Array[] = [];
  private length = 0;

  writeVarint(value: bigint | number): this {
    return this.writeBytes(encodeVarint(value));
  }

  writeTag(field: number, wire: number): this {
    return this.writeVarint((field << 3) | wire);
  }

  writeUint32(value: number): this {
    const b = new Uint8Array(4);
    b[0] = value & 0xff;
    b[1] = (value >>> 8) & 0xff;
    b[2] = (value >>> 16) & 0xff;
    b[3] = (value >>> 24) & 0xff;
    return this.writeBytes(b);
  }

  writeFixed32(value: number): this {
    return this.writeUint32(value >>> 0);
  }

  writeFixed64(value: bigint): this {
    const v = value & 0xffffffffffffffffn;
    this.writeUint32(Number(v & 0xffffffffn));
    this.writeUint32(Number((v >> 32n) & 0xffffffffn));
    return this;
  }

  writeFloat(value: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setFloat32(0, value, true);
    return this.writeBytes(b);
  }

  writeDouble(value: number): this {
    const b = new Uint8Array(8);
    new DataView(b.buffer).setFloat64(0, value, true);
    return this.writeBytes(b);
  }

  writeLengthDelimited(payload: Uint8Array): this {
    this.writeVarint(payload.length);
    return this.writeBytes(payload);
  }

  writeString(value: string): this {
    return this.writeLengthDelimited(new TextEncoder().encode(value));
  }

  writeBytes(bytes: Uint8Array): this {
    this.chunks.push(bytes);
    this.length += bytes.length;
    return this;
  }

  finish(): Uint8Array {
    const out = new Uint8Array(this.length);
    let offset = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, offset);
      offset += chunk.length;
    }
    return out;
  }
}
