import * as fs from 'node:fs';
import { BlockBuilder, BlockReader } from './block.ts';
import { encodeVarint, decodeVarint } from './varint.ts';

/**
 * SSTable file layout:
 *
 *   [data block 0][data block 1] ... [data block N][index block][footer]
 *
 * footer (24 bytes, fixed, at EOF):
 *   indexOffset : uint64LE
 *   indexSize   : uint64LE
 *   magic       : 8 bytes "LSMIDX01"
 *
 * The index block is itself a block (see block.ts): each entry maps
 *   separatorKey -> varint blockOffset | varint blockSize
 * where separatorKey is the shortest key >= every key in the data block and
 * < every key in the next one. Because the index is a searchable block, a
 * Seek costs one in-memory binary search over the index plus one binary
 * search inside a single data block -- no irrelevant block is ever read.
 */
const MAGIC = Buffer.from('LSMIDX01', 'ascii');
const FOOTER_SIZE = 24;

export interface BlockHandle {
  offset: number;
  size: number;
}

function encodeBlockHandle(handle: BlockHandle): Buffer {
  return Buffer.concat([encodeVarint(handle.offset), encodeVarint(handle.size)]);
}

function decodeBlockHandle(buf: Buffer): BlockHandle {
  const [offset, afterOffset] = decodeVarint(buf, 0);
  const [size] = decodeVarint(buf, afterOffset);
  return { offset, size };
}

/**
 * Shortest separator: smallest byte string that is >= `lastKeyOfBlock` and
 * still < `firstKeyOfNextBlock` (LevelDB-style index key shortening).
 */
export function shortestSeparator(lastKeyOfBlock: Buffer, firstKeyOfNextBlock: Buffer): Buffer {
  const shared = Math.min(lastKeyOfBlock.length, firstKeyOfNextBlock.length);
  let diff = 0;
  while (diff < shared && lastKeyOfBlock[diff] === firstKeyOfNextBlock[diff]) diff++;
  if (diff < shared) {
    const byte = lastKeyOfBlock[diff];
    if (byte < 0xff && byte + 1 < firstKeyOfNextBlock[diff]) {
      const sep = Buffer.allocUnsafe(diff + 1);
      lastKeyOfBlock.copy(sep, 0, 0, diff + 1);
      sep[diff] = byte + 1;
      return sep;
    }
  }
  return Buffer.from(lastKeyOfBlock);
}

export class SstableBuilder {
  private readonly dataChunks: Buffer[] = [];
  private readonly index = new BlockBuilder();
  private block = new BlockBuilder();
  private pendingHandle: BlockHandle | null = null;
  private lastKey: Buffer | null = null;
  private offset = 0;
  private entries = 0;
  private readonly blockSize: number;

  constructor(blockSize = 4096) {
    this.blockSize = blockSize;
  }

  /** Keys must be added in strictly increasing byte order. */
  add(key: Buffer, value: Buffer): void {
    if (this.pendingHandle !== null) {
      this.index.add(shortestSeparator(this.lastKey!, key), encodeBlockHandle(this.pendingHandle));
      this.pendingHandle = null;
    }
    this.block.add(key, value);
    this.lastKey = Buffer.from(key);
    this.entries++;
    if (this.block.currentSizeEstimate >= this.blockSize) this.flushBlock();
  }

  private flushBlock(): void {
    if (this.block.entryCount === 0) return;
    const raw = this.block.finish();
    this.dataChunks.push(raw);
    this.pendingHandle = { offset: this.offset, size: raw.length };
    this.offset += raw.length;
    this.block = new BlockBuilder();
  }

  /** Writes the table to `path` and returns basic stats. */
  finish(path: string): { entries: number; blocks: number; bytes: number } {
    if (this.entries === 0) throw new Error('cannot finish an empty SSTable');
    this.flushBlock();
    // The final block is indexed by its last key (no successor to shorten against).
    this.index.add(Buffer.from(this.lastKey!), encodeBlockHandle(this.pendingHandle!));
    const indexRaw = this.index.finish();
    const footer = Buffer.allocUnsafe(FOOTER_SIZE);
    footer.writeBigUInt64LE(BigInt(this.offset), 0);
    footer.writeBigUInt64LE(BigInt(indexRaw.length), 8);
    MAGIC.copy(footer, 16);
    fs.writeFileSync(path, Buffer.concat([...this.dataChunks, indexRaw, footer]));
    return {
      entries: this.entries,
      blocks: this.index.entryCount,
      bytes: this.offset + indexRaw.length + FOOTER_SIZE,
    };
  }
}

export interface SstableStats {
  /** Data blocks actually read from disk (driven purely by the iterator). */
  dataBlocksRead: number;
  dataBytesRead: number;
  indexBlocksRead: number;
}

export class SstableReader {
  readonly stats: SstableStats = { dataBlocksRead: 0, dataBytesRead: 0, indexBlocksRead: 0 };
  private readonly fd: number;
  private readonly index: BlockReader;
  readonly fileSize: number;
  readonly path: string;

  constructor(path: string) {
    this.path = path;
    this.fd = fs.openSync(path, 'r');
    this.fileSize = fs.fstatSync(this.fd).size;
    if (this.fileSize < FOOTER_SIZE) throw new Error('corrupt SSTable: too small');
    const footer = Buffer.allocUnsafe(FOOTER_SIZE);
    fs.readSync(this.fd, footer, 0, FOOTER_SIZE, this.fileSize - FOOTER_SIZE);
    if (!footer.subarray(16, 24).equals(MAGIC)) throw new Error('corrupt SSTable: bad magic');
    const indexOffset = Number(footer.readBigUInt64LE(0));
    const indexSize = Number(footer.readBigUInt64LE(8));
    const indexRaw = Buffer.allocUnsafe(indexSize);
    fs.readSync(this.fd, indexRaw, 0, indexSize, indexOffset);
    this.index = new BlockReader(indexRaw);
    this.stats.indexBlocksRead++;
  }

  get numBlocks(): number {
    return this.index.size;
  }

  blockHandleAt(index: number): BlockHandle {
    return decodeBlockHandle(this.index.valueAt(index));
  }

  /**
   * Index-block binary search: id of the first data block whose separator
   * key is >= target, i.e. the only block that may contain `target`.
   * Returns `numBlocks` if target is past the last key of the table.
   */
  findBlock(target: Buffer): number {
    return this.index.seek(target);
  }

  readBlock(handle: BlockHandle): Buffer {
    const buf = Buffer.allocUnsafe(handle.size);
    fs.readSync(this.fd, buf, 0, handle.size, handle.offset);
    this.stats.dataBlocksRead++;
    this.stats.dataBytesRead += handle.size;
    return buf;
  }

  close(): void {
    fs.closeSync(this.fd);
  }
}
