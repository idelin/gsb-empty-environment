/** WireWriter：分块累积、finish() 一次性拼接并释放内部块（无残留引用）。 */

import { encodeVarint } from './varint.ts';
import type { WireTypeValue } from './reader.ts';

export class WireWriter {
  private chunks: Uint8Array[] = [];
  private byteLength = 0;

  private push(b: Uint8Array): this {
    this.chunks.push(b);
    this.byteLength += b.length;
    return this;
  }

  writeVarint(value: bigint | number): this {
    return this.push(encodeVarint(value));
  }

  writeTag(fieldNumber: number, wireType: WireTypeValue): this {
    return this.writeVarint((fieldNumber << 3) | wireType);
  }

  writeFixed32(value: number): this {
    const b = new Uint8Array(4);
    const v = value >>> 0;
    b[0] = v & 0xff;
    b[1] = (v >>> 8) & 0xff;
    b[2] = (v >>> 16) & 0xff;
    b[3] = (v >>> 24) & 0xff;
    return this.push(b);
  }

  writeFixed64(value: bigint): this {
    const lo = Number(value & 0xffffffffn);
    const hi = Number((value >> 32n) & 0xffffffffn);
    return this.writeFixed32(lo).writeFixed32(hi);
  }

  writeFloat(value: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setFloat32(0, value, true);
    return this.push(b);
  }

  writeDouble(value: number): this {
    const b = new Uint8Array(8);
    new DataView(b.buffer).setFloat64(0, value, true);
    return this.push(b);
  }

  writeBytes(b: Uint8Array): this {
    return this.push(b.slice());
  }

  writeLengthDelimited(b: Uint8Array): this {
    this.writeVarint(b.length);
    return this.push(b.slice());
  }

  /** 拼接输出并清空内部状态；重复调用返回空缓冲。 */
  finish(): Uint8Array {
    const out = new Uint8Array(this.byteLength);
    let offset = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, offset);
      offset += chunk.length;
    }
    this.chunks = [];
    this.byteLength = 0;
    return out;
  }
}
