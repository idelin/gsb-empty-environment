import { encodeVarint } from './varint.ts';
import { WireType } from './types.ts';

export class WireWriter {
  private chunks: Uint8Array[] = [];
  private length = 0;

  writeVarint(value: bigint): this {
    return this.writeBytes(encodeVarint(value));
  }

  writeTag(fieldNumber: number, wireType: WireType): this {
    return this.writeVarint((BigInt(fieldNumber) << 3n) | BigInt(wireType));
  }

  writeFixed32(value: number): this {
    const out = new Uint8Array(4);
    const v = value >>> 0;
    out[0] = v & 0xff;
    out[1] = (v >>> 8) & 0xff;
    out[2] = (v >>> 16) & 0xff;
    out[3] = (v >>> 24) & 0xff;
    return this.writeBytes(out);
  }

  writeFixed64(value: bigint): this {
    const out = new Uint8Array(8);
    let v = value & 0xffffffffffffffffn;
    for (let i = 0; i < 8; i++) {
      out[i] = Number(v & 0xffn);
      v >>= 8n;
    }
    return this.writeBytes(out);
  }

  writeFloat(value: number): this {
    const out = new Uint8Array(4);
    new DataView(out.buffer).setFloat32(0, value, true);
    return this.writeBytes(out);
  }

  writeDouble(value: number): this {
    const out = new Uint8Array(8);
    new DataView(out.buffer).setFloat64(0, value, true);
    return this.writeBytes(out);
  }

  writeBytes(bytes: Uint8Array): this {
    this.chunks.push(bytes);
    this.length += bytes.length;
    return this;
  }

  writeLengthDelimited(bytes: Uint8Array): this {
    this.writeVarint(BigInt(bytes.length));
    return this.writeBytes(bytes);
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
