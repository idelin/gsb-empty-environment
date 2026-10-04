import { writeVarint } from './varint.ts';
import { WireType, type WireTypeValue } from './reader.ts';

/** Growable buffer for hand-rolled protobuf encoding (test fixtures + demo). */
export class WireWriter {
  private out: number[] = [];

  private push(bs: ArrayLike<number>): void {
    for (let i = 0; i < bs.length; i++) this.out.push(bs[i]);
  }

  tag(fieldNumber: number, wireType: WireTypeValue): this {
    this.push(writeVarint((fieldNumber << 3) | wireType));
    return this;
  }

  varint(value: number | bigint): this {
    this.push(writeVarint(value));
    return this;
  }

  varintField(fieldNumber: number, value: number | bigint): this {
    return this.tag(fieldNumber, WireType.Varint).varint(value);
  }

  fixed32(value: number): this {
    this.out.push(value & 0xff, (value >>> 8) & 0xff, (value >>> 16) & 0xff, (value >>> 24) & 0xff);
    return this;
  }

  fixed32Field(fieldNumber: number, value: number): this {
    return this.tag(fieldNumber, WireType.Fixed32).fixed32(value);
  }

  fixed64(value: bigint): this {
    let v = value & 0xffffffffffffffffn;
    for (let i = 0; i < 8; i++) {
      this.out.push(Number(v & 0xffn));
      v >>= 8n;
    }
    return this;
  }

  fixed64Field(fieldNumber: number, value: bigint): this {
    return this.tag(fieldNumber, WireType.Fixed64).fixed64(value);
  }

  bytes(data: Uint8Array): this {
    this.push(writeVarint(data.length));
    this.push(data);
    return this;
  }

  bytesField(fieldNumber: number, data: Uint8Array): this {
    return this.tag(fieldNumber, WireType.LengthDelimited).bytes(data);
  }

  stringField(fieldNumber: number, text: string): this {
    return this.bytesField(fieldNumber, new TextEncoder().encode(text));
  }

  /** Embed a nested message as a length-delimited field. */
  messageField(fieldNumber: number, message: Uint8Array): this {
    return this.bytesField(fieldNumber, message);
  }

  /** Write a packed repeated field from pre-encoded element bytes. */
  packedField(fieldNumber: number, encodedElements: Uint8Array): this {
    return this.bytesField(fieldNumber, encodedElements);
  }

  finish(): Uint8Array {
    return Uint8Array.from(this.out);
  }
}

export function concatBytes(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}
