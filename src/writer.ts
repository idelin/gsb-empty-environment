import { encodeVarint } from './varint.ts';
import { WireType, type WireTypeValue } from './reader.ts';

const textEncoder = new TextEncoder();

/** Growable protobuf wire-format writer. */
export class WireWriter {
  private chunks: Uint8Array[] = [];
  private length = 0;

  private push(bytes: Uint8Array): void {
    this.chunks.push(bytes);
    this.length += bytes.length;
  }

  writeByte(byte: number): this {
    this.push(Uint8Array.of(byte & 0xff));
    return this;
  }

  writeVarint(value: number | bigint): this {
    this.push(encodeVarint(value));
    return this;
  }

  writeTag(fieldNumber: number, wireType: WireTypeValue): this {
    return this.writeVarint((fieldNumber << 3) | wireType);
  }

  writeFixed32(value: number): this {
    const out = new Uint8Array(4);
    const v = value >>> 0;
    out[0] = v & 0xff;
    out[1] = (v >>> 8) & 0xff;
    out[2] = (v >>> 16) & 0xff;
    out[3] = (v >>> 24) & 0xff;
    this.push(out);
    return this;
  }

  writeFixed64(value: bigint): this {
    const out = new Uint8Array(8);
    let v = BigInt.asUintN(64, value);
    for (let i = 0; i < 8; i++) {
      out[i] = Number(v & 0xffn);
      v >>= 8n;
    }
    this.push(out);
    return this;
  }

  writeFloat(value: number): this {
    const out = new Uint8Array(4);
    new DataView(out.buffer).setFloat32(0, value, true);
    this.push(out);
    return this;
  }

  writeDouble(value: number): this {
    const out = new Uint8Array(8);
    new DataView(out.buffer).setFloat64(0, value, true);
    this.push(out);
    return this;
  }

  writeBytes(bytes: Uint8Array): this {
    this.push(bytes);
    return this;
  }

  writeLengthDelimited(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    this.push(bytes);
    return this;
  }

  writeString(value: string): this {
    return this.writeLengthDelimited(textEncoder.encode(value));
  }

  get size(): number {
    return this.length;
  }

  finish(): Uint8Array {
    const out = new Uint8Array(this.length);
    let offset = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, offset);
      offset += chunk.length;
    }
    this.chunks = [];
    this.length = 0;
    return out;
  }
}

export { WireType };
