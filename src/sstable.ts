import * as fs from 'node:fs';
import { BlockBuilder, BlockReader } from './block.ts';
import { decodeVarint64, encodeVarint64, varintLength } from './varint.ts';
import { PrefixIterator } from './prefix_iterator.ts';

/**
 * SSTable file layout:
 *
 *   [data block 0][data block 1]...[data block N][index block][footer]
 *
 * index block : same block encoding; key = shortest separator for the data
 *               block, value = blockHandle(offset varint, size varint).
 * footer      : indexHandle(offset+size varint) padded to 20 bytes + 8B magic.
 */
export const DEFAULT_BLOCK_SIZE = 4096;
const FOOTER_SIZE = 28;
const MAGIC = 0x4c534d5052465858n; // "LSMPRFXX"

export interface BlockHandle {
  offset: number;
  size: number;
}

export interface IndexEntry extends BlockHandle {
  /** Shortest separator: >= every key in this block, < first key of next. */
  separator: Buffer;
}

function encodeHandle(handle: BlockHandle): Buffer {
  return Buffer.concat([encodeVarint64(handle.offset), encodeVarint64(handle.size)]);
}

function decodeHandle(buf: Buffer, offset: number): { handle: BlockHandle; bytesRead: number } {
  const off = decodeVarint64(buf, offset);
  const size = decodeVarint64(buf, offset + off.bytesRead);
  return {
    handle: { offset: off.value, size: size.value },
    bytesRead: off.bytesRead + size.bytesRead,
  };
}

/** Shortest key >= start and < limit (LevelDB FindShortestSeparator). */
export function shortestSeparator(start: Buffer, limit: Buffer): Buffer {
  const minLen = Math.min(start.length, limit.length);
  let diff = 0;
  while (diff < minLen && start[diff] === limit[diff]) diff++;
  if (diff < minLen) {
    const diffByte = start[diff];
    if (diffByte < 0xff && diffByte + 1 < limit[diff]) {
      return Buffer.concat([start.subarray(0, diff), Buffer.from([diffByte + 1])]);
    }
  }
  return Buffer.from(start);
}

/** Shortest key >= key (LevelDB FindShortSuccessor). */
export function shortestSuccessor(key: Buffer): Buffer {
  for (let i = 0; i < key.length; i++) {
    if (key[i] !== 0xff) {
      return Buffer.concat([key.subarray(0, i), Buffer.from([key[i] + 1])]);
    }
  }
  return Buffer.from(key);
}

export class SSTableBuilder {
  private chunks: Buffer[] = [];
  private offset = 0;
  private dataBlock = new BlockBuilder();
  private indexBlock = new BlockBuilder();
  private lastKeyOfPrevBlock: Buffer | null = null;
  private lastKey: Buffer | null = null;
  private pendingHandle: BlockHandle | null = null;
  private closed = false;
  private numEntries = 0;
  readonly filePath: string;
  private readonly blockSize: number;

  constructor(filePath: string, blockSize: number = DEFAULT_BLOCK_SIZE) {
    this.filePath = filePath;
    this.blockSize = blockSize;
  }

  /** Keys must be added in strictly increasing order. */
  add(key: Buffer | string, value: Buffer | string): void {
    if (this.closed) throw new Error('builder already finished');
    const k = typeof key === 'string' ? Buffer.from(key, 'utf8') : key;
    const v = typeof value === 'string' ? Buffer.from(value, 'utf8') : value;
    if (this.lastKey && Buffer.compare(k, this.lastKey) <= 0) {
      throw new Error(`keys must be added in increasing order: ${k.toString()}`);
    }
    if (this.pendingHandle && this.lastKeyOfPrevBlock) {
      const sep = shortestSeparator(this.lastKeyOfPrevBlock, k);
      this.indexBlock.add(sep, encodeHandle(this.pendingHandle));
      this.pendingHandle = null;
    }
    this.dataBlock.add(k, v);
    this.lastKey = Buffer.from(k);
    this.numEntries++;
    if (this.dataBlock.currentSize >= this.blockSize) this.flushDataBlock();
  }

  private flushDataBlock(): void {
    if (this.dataBlock.empty) return;
    const raw = this.dataBlock.finish();
    const handle: BlockHandle = { offset: this.offset, size: raw.length };
    this.chunks.push(raw);
    this.offset += raw.length;
    this.pendingHandle = handle;
    this.lastKeyOfPrevBlock = this.lastKey ? Buffer.from(this.lastKey) : null;
    this.dataBlock = new BlockBuilder();
  }

  /** Flush remaining data, write index block + footer. Returns entry count. */
  finish(): number {
    if (this.closed) throw new Error('builder already finished');
    this.flushDataBlock();
    if (this.pendingHandle && this.lastKeyOfPrevBlock) {
      this.indexBlock.add(shortestSuccessor(this.lastKeyOfPrevBlock), encodeHandle(this.pendingHandle));
      this.pendingHandle = null;
    }
    const indexRaw = this.indexBlock.finish();
    const indexHandle: BlockHandle = { offset: this.offset, size: indexRaw.length };
    this.chunks.push(indexRaw);
    this.offset += indexRaw.length;

    const footer = Buffer.alloc(FOOTER_SIZE);
    const handleEnc = encodeHandle(indexHandle);
    handleEnc.copy(footer, 0);
    footer.writeBigUInt64LE(MAGIC, FOOTER_SIZE - 8);
    this.chunks.push(footer);
    this.offset += footer.length;

    fs.writeFileSync(this.filePath, Buffer.concat(this.chunks));
    this.closed = true;
    return this.numEntries;
  }
}

export interface ReaderStats {
  /** Data blocks actually read from disk (proves irrelevant blocks skipped). */
  dataBlocksRead: number;
  indexLookups: number;
}

export class SSTableReader {
  private readonly fileBuf: Buffer;
  readonly indexEntries: IndexEntry[] = [];
  readonly stats: ReaderStats = { dataBlocksRead: 0, indexLookups: 0 };
  readonly filePath: string;

  private constructor(filePath: string) {
    this.filePath = filePath;
    this.fileBuf = fs.readFileSync(filePath);
    if (this.fileBuf.length < FOOTER_SIZE) throw new Error('not an sstable: too small');
    const magic = this.fileBuf.readBigUInt64LE(this.fileBuf.length - 8);
    if (magic !== MAGIC) throw new Error('not an sstable: bad magic');
    const { handle: indexHandle } = decodeHandle(
      this.fileBuf,
      this.fileBuf.length - FOOTER_SIZE,
    );
    const indexBlock = new BlockReader(
      this.fileBuf.subarray(indexHandle.offset, indexHandle.offset + indexHandle.size),
    );
    const it = indexBlock.iterator();
    for (it.seekToFirst(); it.valid(); it.next()) {
      const { handle } = decodeHandle(it.value(), 0);
      this.indexEntries.push({
        separator: Buffer.from(it.key()), // separators are tiny; own a copy
        offset: handle.offset,
        size: handle.size,
      });
    }
  }

  static open(filePath: string): SSTableReader {
    return new SSTableReader(filePath);
  }

  get numBlocks(): number {
    return this.indexEntries.length;
  }

  /** File offset where data blocks end and the index block begins. */
  get dataRegionEnd(): number {
    const last = this.indexEntries[this.indexEntries.length - 1];
    return last ? last.offset + last.size : 0;
  }

  /**
   * Binary-search the in-memory index block for the first data block whose
   * separator >= target, i.e. the only block that may contain `target`.
   * Returns the index position, or -1 if target is beyond the last block.
   */
  locateBlock(target: Buffer): number {
    this.stats.indexLookups++;
    let lo = 0;
    let hi = this.indexEntries.length - 1;
    let result = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      if (Buffer.compare(this.indexEntries[mid].separator, target) >= 0) {
        result = mid;
        hi = mid - 1;
      } else {
        lo = mid + 1;
      }
    }
    return result;
  }

  /** Zero-copy slice of the mmap-style in-memory file image. */
  readBlockAt(index: number): BlockReader {
    const entry = this.indexEntries[index];
    this.stats.dataBlocksRead++;
    return new BlockReader(this.fileBuf.subarray(entry.offset, entry.offset + entry.size));
  }

  /** Point lookup via index binary search + in-block seek. */
  get(key: Buffer | string): Buffer | null {
    const k = typeof key === 'string' ? Buffer.from(key, 'utf8') : key;
    const idx = this.locateBlock(k);
    if (idx < 0) return null;
    const it = this.readBlockAt(idx).iterator();
    it.seek(k);
    if (it.valid() && Buffer.compare(it.key(), k) === 0) return it.value();
    return null;
  }

  /**
   * Prefix range scan. Binary-searches the index block straight to the first
   * candidate data block, then streams entries while key starts with `prefix`,
   * stopping at the first out-of-range key (boundary truncation).
   */
  prefixSeek(prefix: Buffer | string): PrefixIterator {
    const p = typeof prefix === 'string' ? Buffer.from(prefix, 'utf8') : prefix;
    return new PrefixIterator(this, p);
  }
}
