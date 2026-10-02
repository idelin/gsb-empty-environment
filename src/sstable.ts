import { readFileSync, writeFileSync } from 'node:fs';
import type { MemEntry } from './memtable.ts';

export interface SSTableMeta {
  id: number;
  cf: string;
  file: string;
  count: number;
  minSeq: number;
  maxSeq: number;
}

interface SSTableFile {
  cf: string;
  entries: Array<[string, MemEntry]>;
}

/** Immutable sorted string table; one file per flush, per column family. */
export class SSTable {
  readonly meta: SSTableMeta;
  private entries: Array<[string, MemEntry]>;

  private constructor(
    meta: SSTableMeta,
    entries: Array<[string, MemEntry]>,
  ) {
    this.meta = meta;
    this.entries = entries;
  }

  static write(
    file: string,
    id: number,
    cf: string,
    entries: Array<[string, MemEntry]>,
  ): SSTable {
    const seqs = entries.map(([, e]) => e.seq);
    const meta: SSTableMeta = {
      id,
      cf,
      file,
      count: entries.length,
      minSeq: Math.min(...seqs),
      maxSeq: Math.max(...seqs),
    };
    const payload: SSTableFile = { cf, entries };
    writeFileSync(file, JSON.stringify(payload));
    return new SSTable(meta, entries);
  }

  static load(file: string, id: number, cf: string): SSTable {
    const payload = JSON.parse(readFileSync(file, 'utf8')) as SSTableFile;
    const seqs = payload.entries.map(([, e]) => e.seq);
    const meta: SSTableMeta = {
      id,
      cf,
      file,
      count: payload.entries.length,
      minSeq: seqs.length ? Math.min(...seqs) : 0,
      maxSeq: seqs.length ? Math.max(...seqs) : 0,
    };
    return new SSTable(meta, payload.entries);
  }

  get(key: string, maxSeq: number): MemEntry | undefined {
    if (this.meta.minSeq > maxSeq) return undefined;
    let lo = 0;
    let hi = this.entries.length - 1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const [k] = this.entries[mid];
      if (k === key) {
        // Versions of a key are stored newest first; walk to the
        // first version visible at maxSeq.
        let i = mid;
        while (i > 0 && this.entries[i - 1][0] === key) i--;
        for (; i < this.entries.length && this.entries[i][0] === key; i++) {
          if (this.entries[i][1].seq <= maxSeq) return this.entries[i][1];
        }
        return undefined;
      }
      if (k < key) lo = mid + 1;
      else hi = mid - 1;
    }
    return undefined;
  }
}
