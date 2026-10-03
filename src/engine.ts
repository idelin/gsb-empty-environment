import * as fs from 'node:fs';
import * as path from 'node:path';
import { MemTable } from './memtable.ts';
import { SSTableBuilder, SSTableReader, DEFAULT_BLOCK_SIZE } from './sstable.ts';
import type { PrefixEntry } from './prefix_iterator.ts';

export interface EngineOptions {
  dir: string;
  /** Flush the memtable to an SSTable once it grows past this many bytes. */
  memtableFlushBytes?: number;
  /** SSTable data block size in bytes. */
  blockSize?: number;
}

interface Source {
  next(): PrefixEntry | null;
  current: PrefixEntry | null;
  priority: number; // lower wins (newer data)
}

/**
 * Minimal LSM engine: writes land in a MemTable, which is flushed to an
 * immutable SSTable when full. Prefix scans merge the memtable with all
 * SSTables (newest wins on duplicate keys) and stop at the prefix boundary.
 */
export class LSMEngine {
  private memtable = new MemTable();
  private readers: SSTableReader[] = []; // oldest -> newest
  private sequence = 0;
  private readonly flushBytes: number;
  private readonly blockSize: number;
  private readonly options: EngineOptions;

  constructor(options: EngineOptions) {
    this.options = options;
    fs.mkdirSync(options.dir, { recursive: true });
    this.flushBytes = options.memtableFlushBytes ?? 4 * 1024 * 1024;
    this.blockSize = options.blockSize ?? DEFAULT_BLOCK_SIZE;
    for (const file of fs.readdirSync(options.dir).sort()) {
      if (file.endsWith('.sst')) {
        this.readers.push(SSTableReader.open(path.join(options.dir, file)));
        this.sequence++;
      }
    }
  }

  put(key: string, value: string | Buffer): void {
    const v = typeof value === 'string' ? Buffer.from(value, 'utf8') : value;
    this.memtable.put(Buffer.from(key, 'utf8'), v);
    if (this.memtable.approximateBytes >= this.flushBytes) this.flush();
  }

  get(key: string): Buffer | null {
    const k = Buffer.from(key, 'utf8');
    const fromMem = this.memtable.get(k);
    if (fromMem) return fromMem;
    for (let i = this.readers.length - 1; i >= 0; i--) {
      const v = this.readers[i].get(k);
      if (v) return v;
    }
    return null;
  }

  /** Flush the memtable into a new immutable SSTable. */
  flush(): void {
    if (this.memtable.size === 0) return;
    const filePath = path.join(this.options.dir, `sst-${String(this.sequence++).padStart(6, '0')}.sst`);
    const builder = new SSTableBuilder(filePath, this.blockSize);
    for (const { key, value } of this.memtable.entries()) builder.add(key, value);
    builder.finish();
    this.readers.push(SSTableReader.open(filePath));
    this.memtable.clear();
  }

  get sstables(): readonly SSTableReader[] {
    return this.readers;
  }

  /**
   * Merge memtable + SSTables into one ordered prefix stream.
   * Newest source wins when the same key exists in several sources.
   */
  *prefixSeek(prefix: string): Generator<PrefixEntry> {
    const p = Buffer.from(prefix, 'utf8');
    const sources: Source[] = [];
    const memIter = this.memtable.entries(p);
    sources.push({
      priority: 0,
      current: null,
      next: () => memIter.next().value ?? null,
    });
    this.readers.forEach((reader, i) => {
      // readers are oldest->newest; newer must win, so invert priority
      const it = reader.prefixSeek(p);
      sources.push({
        priority: this.readers.length - i,
        current: null,
        next: () => it.next().value ?? null,
      });
    });
    for (const s of sources) s.current = s.next();

    for (;;) {
      let best: Buffer | null = null;
      for (const s of sources) {
        if (s.current && (best === null || Buffer.compare(s.current.key, best) < 0)) {
          best = Buffer.from(s.current.key);
        }
      }
      if (best === null) return;
      let winner: Source | null = null;
      for (const s of sources) {
        if (s.current && Buffer.compare(s.current.key, best) === 0) {
          if (!winner || s.priority < winner.priority) winner = s;
        }
      }
      const out: PrefixEntry = { key: Buffer.from(best), value: winner!.current!.value };
      for (const s of sources) {
        if (s.current && Buffer.compare(s.current.key, best) === 0) s.current = s.next();
      }
      yield out;
    }
  }

  close(): void {
    this.readers.length = 0;
    this.memtable.clear();
  }
}
