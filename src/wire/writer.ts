import { WireType, type WireTypeValue } from "./reader.ts";

/**
 * Complementary encoder for the wire format. Used by tests, the demo, and
 * anyone who needs to produce buffers for the reader to parse.
 */
export class WireWriter {
  private chunks: number[] = [];

  get length(): number {
    return this.chunks.length;
  }

  writeVarint(value: bigint | number): this {
    let v = BigInt.asUintN(64, BigInt(value));
    do {
      let byte = Number(v & 0x7fn);
      v >>= 7n;
      if (v !== 0n) byte |= 0x80;
      this.chunks.push(byte);
    } while (v !== 0n);
    return this;
  }

  writeZigZag32(value: number): this {
    return this.writeVarint(((value << 1) ^ (value >> 31)) >>> 0);
  }

  writeZigZag64(value: bigint): this {
    return this.writeVarint((value << 1n) ^ (value >> 63n));
  }

  writeFixed32(value: number): this {
    const v = value >>> 0;
    this.chunks.push(v & 0xff, (v >>> 8) & 0xff, (v >>> 16) & 0xff, (v >>> 24) & 0xff);
    return this;
  }

  writeFixed64(value: bigint | number): this {
    let v = BigInt.asUintN(64, BigInt(value));
    for (let i = 0; i < 8; i++) {
      this.chunks.push(Number(v & 0xffn));
      v >>= 8n;
    }
    return this;
  }

  writeFloat(value: number): this {
    const buf = new Uint8Array(4);
    new DataView(buf.buffer).setFloat32(0, value, true);
    return this.writeRawBytes(buf);
  }

  writeDouble(value: number): this {
    const buf = new Uint8Array(8);
    new DataView(buf.buffer).setFloat64(0, value, true);
    return this.writeRawBytes(buf);
  }

  writeRawBytes(bytes: Uint8Array): this {
    for (const b of bytes) this.chunks.push(b);
    return this;
  }

  /** Write a length prefix followed by the payload. */
  writeBytes(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    return this.writeRawBytes(bytes);
  }

  writeString(text: string): this {
    return this.writeBytes(new TextEncoder().encode(text));
  }

  writeTag(field: number, wireType: WireTypeValue): this {
    return this.writeVarint((field << 3) | wireType);
  }

  /** Write a complete field: tag + value encoded by the given wire type. */
  writeField(field: number, wireType: WireTypeValue, write: (w: WireWriter) => void): this {
    this.writeTag(field, wireType);
    write(this);
    return this;
  }

  finish(): Uint8Array {
    return Uint8Array.from(this.chunks);
  }
}

export function encode(...build: Array<(w: WireWriter) => void>): Uint8Array {
  const w = new WireWriter();
  for (const fn of build) fn(w);
  return w.finish();
}

export { WireType };
