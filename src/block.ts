import { encodeVarint, decodeVarint } from './varint.ts';

/**
 * Block layout (shared by data blocks and the index block):
 *
 *   entry*    : varint keyLen | key bytes | varint valueLen | value bytes
 *   offsets   : uint32LE[numEntries]   -- start offset of every entry
 *   trailer   : uint32LE numEntries
 *
 * Keys are stored in full (no prefix compression) and every entry offset is
 * recorded, so a block is binary-searchable and every key/value can be
 * returned as a zero-copy `Buffer.subarray` view into the block buffer.
 */
export class BlockBuilder {
  private chunks: Buffer[] = [];
  private size = 0;
  private offsets: number[] = [];

  get entryCount(): number {
    return this.offsets.length;
  }

  get currentSizeEstimate(): number {
    return this.size + this.offsets.length * 4 + 4;
  }

  add(key: Buffer, value: Buffer): void {
    this.offsets.push(this.size);
    const keyLen = encodeVarint(key.length);
    const valueLen = encodeVarint(value.length);
    this.chunks.push(keyLen, key, valueLen, value);
    this.size += keyLen.length + key.length + valueLen.length + value.length;
  }

  finish(): Buffer {
    const trailer = Buffer.allocUnsafe(this.offsets.length * 4 + 4);
    for (let i = 0; i < this.offsets.length; i++) {
      trailer.writeUInt32LE(this.offsets[i], i * 4);
    }
    trailer.writeUInt32LE(this.offsets.length, this.offsets.length * 4);
    return Buffer.concat([...this.chunks, trailer]);
  }
}

export class BlockReader {
  /** The raw block buffer; keys/values are exposed as views into it. */
  readonly data: Buffer;
  private readonly numEntries: number;
  private readonly offsetsStart: number;

  constructor(block: Buffer) {
    if (block.length < 4) throw new Error('corrupt block: too small');
    this.data = block;
    this.numEntries = block.readUInt32LE(block.length - 4);
    this.offsetsStart = block.length - 4 - this.numEntries * 4;
    if (this.offsetsStart < 0) throw new Error('corrupt block: bad entry count');
  }

  get size(): number {
    return this.numEntries;
  }

  private entryOffset(index: number): number {
    return this.data.readUInt32LE(this.offsetsStart + index * 4);
  }

  /** Returns [keyStart, keyEnd, valueStart, valueEnd] within `data`. */
  private entryBounds(index: number): [number, number, number, number] {
    const [keyLen, afterKeyLen] = decodeVarint(this.data, this.entryOffset(index));
    const keyStart = afterKeyLen;
    const keyEnd = keyStart + keyLen;
    const [valueLen, valueStart] = decodeVarint(this.data, keyEnd);
    return [keyStart, keyEnd, valueStart, valueStart + valueLen];
  }

  /** Zero-copy view of the key at `index`. */
  keyAt(index: number): Buffer {
    const [keyStart, keyEnd] = this.entryBounds(index);
    return this.data.subarray(keyStart, keyEnd);
  }

  /** Zero-copy view of the value at `index`. */
  valueAt(index: number): Buffer {
    const [, , valueStart, valueEnd] = this.entryBounds(index);
    return this.data.subarray(valueStart, valueEnd);
  }

  /**
   * Binary search: index of the first entry whose key is >= target,
   * or `size` if every key in the block is smaller than target.
   */
  seek(target: Buffer): number {
    let lo = 0;
    let hi = this.numEntries;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (Buffer.compare(this.keyAt(mid), target) < 0) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }
}
