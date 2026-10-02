import { OverflowChain } from './overflow.ts';

/**
 * A DataRef is how a B+ tree node refers to a key or a value:
 *  - inline   : the whole payload lives inside the node page (`prefix` = data)
 *  - overflow : only a short `prefix` is kept in the node page; the full
 *               payload lives in an overflow page chain headed at
 *               `overflowHead`. `totalLen` records the original byte length.
 *
 * Serialized form inside a node page:
 *   flag(1) | totalLen(4) | prefixLen(2) | prefix bytes | [overflowHead(4)]
 */
export interface DataRef {
  inline: boolean;
  prefix: Buffer;
  totalLen: number;
  overflowHead: number;
}

export function encodeRef(
  chain: OverflowChain,
  data: Buffer,
  maxInline: number,
  prefixLen: number,
): DataRef {
  if (data.length <= maxInline) {
    return { inline: true, prefix: Buffer.from(data), totalLen: data.length, overflowHead: 0 };
  }
  return {
    inline: false,
    prefix: Buffer.from(data.subarray(0, Math.min(prefixLen, data.length))),
    totalLen: data.length,
    overflowHead: chain.write(data),
  };
}

/** Transparently reassembles the full payload, following the overflow chain if needed. */
export function readRef(chain: OverflowChain, ref: DataRef): Buffer {
  if (ref.inline) return Buffer.from(ref.prefix);
  return chain.read(ref.overflowHead, ref.totalLen);
}

export function refSerializedSize(ref: DataRef): number {
  return 1 + 4 + 2 + ref.prefix.length + (ref.inline ? 0 : 4);
}

export function writeRef(target: Buffer, offset: number, ref: DataRef): number {
  target.writeUInt8(ref.inline ? 0 : 1, offset);
  offset += 1;
  target.writeUInt32LE(ref.totalLen, offset);
  offset += 4;
  target.writeUInt16LE(ref.prefix.length, offset);
  offset += 2;
  ref.prefix.copy(target, offset);
  offset += ref.prefix.length;
  if (!ref.inline) {
    target.writeUInt32LE(ref.overflowHead, offset);
    offset += 4;
  }
  return offset;
}

export function parseRef(source: Buffer, offset: number): { ref: DataRef; next: number } {
  const flag = source.readUInt8(offset);
  offset += 1;
  const totalLen = source.readUInt32LE(offset);
  offset += 4;
  const prefixLen = source.readUInt16LE(offset);
  offset += 2;
  const prefix = Buffer.from(source.subarray(offset, offset + prefixLen));
  offset += prefixLen;
  let overflowHead = 0;
  if (flag === 1) {
    overflowHead = source.readUInt32LE(offset);
    offset += 4;
  }
  return { ref: { inline: flag === 0, prefix, totalLen, overflowHead }, next: offset };
}
