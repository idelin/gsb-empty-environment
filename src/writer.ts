/** Incremental protobuf encoder; mirror of WireReader. */
import { encodeVarint, encodeFixed32, encodeFixed64, zigzagEncode } from './wire.ts';

export class WireWriter {
  private chunks: Uint8Array[] = [];

  writeVarint(value: number | bigint): this {
    this.chunks.push(encodeVarint(value));
    return this;
  }

  writeZigZag(value: number | bigint): this {
    return this.writeVarint(zigzagEncode(value));
  }

  writeFixed32(value: number): this {
    this.chunks.push(encodeFixed32(value));
    return this;
  }

  writeFixed64(value: number | bigint): this {
    this.chunks.push(encodeFixed64(value));
    return this;
  }

  writeTag(field: number, wire: number): this {
    return this.writeVarint((field << 3) | wire);
  }

  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    this.chunks.push(bytes);
    return this;
  }

  writeString(text: string): this {
    return this.writeBytes(new TextEncoder().encode(text));
  }

  get length(): number {
    return this.chunks.reduce((n, c) => n + c.length, 0);
  }

  toBytes(): Uint8Array {
    const out = new Uint8Array(this.length);
    let pos = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, pos);
      pos += chunk.length;
    }
    return out;
  }
}
