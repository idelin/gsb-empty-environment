import * as fs from 'node:fs';
import * as path from 'node:path';
import { SstableBuilder, SstableReader, type SstableStats } from './sstable.ts';
import {
  MemTableIterator,
  MergeIterator,
  PrefixIterator,
  SstableIterator,
  type RawIterator,
} from './iterator.ts';

export interface PrefixScanStats extends SstableStats {
  tables: number;
  totalBlocks: number;
}

/**
 * Minimal LSM engine: an in-memory memtable plus a stack of immutable
 * SSTables (newest first). `prefixSeek` is the prefix range-scan locator:
 * it positions every source at the prefix via its index and merges them,
 * returning matching keys in order with boundary truncation.
 */
export class LsmEngine {
  private mem = new Map<string, Buffer>();
  private tables: SstableReader[] = []; // newest first
  private sequence = 0;
  private readonly dir: string;
  private readonly blockSize: number;

  constructor(dir: string, blockSize = 4096) {
    this.dir = dir;
    this.blockSize = blockSize;
    fs.mkdirSync(dir, { recursive: true });
  }

  put(key: string, value: string | Buffer): void {
    this.mem.set(key, typeof value === 'string' ? Buffer.from(value) : value);
  }

  get memtableSize(): number {
    return this.mem.size;
  }

  /** Flushes the memtable into a new sorted SSTable on disk. */
  flush(): { entries: number; blocks: number; bytes: number } | null {
    if (this.mem.size === 0) return null;
    const file = path.join(this.dir, `sst-${String(++this.sequence).padStart(6, '0')}.sst`);
    const builder = new SstableBuilder(this.blockSize);
    const keys = [...this.mem.keys()].sort();
    for (const key of keys) builder.add(Buffer.from(key), this.mem.get(key)!);
    const info = builder.finish(file);
    this.tables.unshift(new SstableReader(file));
    this.mem.clear();
    return info;
  }

  /**
   * Prefix range scan. Seeks directly to the first block that can contain
   * `prefix`, streams every matching key/value in order (zero-copy views),
   * and stops at the first key outside the prefix.
   */
  prefixSeek(prefix: string): PrefixIterator {
    const target = Buffer.from(prefix);
    const sources: RawIterator[] = [];
    if (this.mem.size > 0) sources.push(new MemTableIterator(this.mem));
    for (const table of this.tables) sources.push(new SstableIterator(table));
    const merged: RawIterator = sources.length === 1 ? sources[0] : new MergeIterator(sources);
    return new PrefixIterator(merged, target);
  }

  stats(): PrefixScanStats {
    const aggregate: PrefixScanStats = {
      dataBlocksRead: 0,
      dataBytesRead: 0,
      indexBlocksRead: 0,
      tables: this.tables.length,
      totalBlocks: 0,
    };
    for (const table of this.tables) {
      aggregate.dataBlocksRead += table.stats.dataBlocksRead;
      aggregate.dataBytesRead += table.stats.dataBytesRead;
      aggregate.indexBlocksRead += table.stats.indexBlocksRead;
      aggregate.totalBlocks += table.numBlocks;
    }
    return aggregate;
  }

  resetStats(): void {
    for (const table of this.tables) {
      table.stats.dataBlocksRead = 0;
      table.stats.dataBytesRead = 0;
    }
  }

  close(): void {
    for (const table of this.tables) table.close();
    this.tables = [];
  }
}
