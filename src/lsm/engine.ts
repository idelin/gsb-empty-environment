import { existsSync, mkdirSync, readdirSync, statSync, unlinkSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { decodeSSTable, encodeSSTable, type Entry } from './sstable.ts';

export interface SSTableMeta {
  name: string;
  path: string;
  size: number;
  seq: number;
  createdAt: number;
}

export interface CompactResult {
  merged: SSTableMeta;
  removed: string[];
}

// Minimal but real LSM-tree engine: an in-memory sorted memtable is flushed
// to immutable SSTable files; compaction merges tables and UNLINKS the old
// files. Immutability of SSTables is what makes hard-link snapshots safe.
export class LSMEngine {
  readonly dir: string;
  private readonly flushThreshold: number;
  private mem = new Map<string, string | null>();
  private tables: SSTableMeta[] = []; // oldest -> newest
  private seq = 0;

  constructor(dir: string, opts: { flushThreshold?: number } = {}) {
    this.dir = dir;
    this.flushThreshold = opts.flushThreshold ?? 1000;
    mkdirSync(dir, { recursive: true });
    // Recover table registry from a previous run in the same directory.
    for (const name of readdirSync(dir).sort()) {
      const match = /^sst-(\d+)\.sst$/.exec(name);
      if (!match) continue;
      const path = join(dir, name);
      const seq = Number(match[1]);
      this.tables.push({ name, path, size: statSync(path).size, seq, createdAt: statSync(path).mtimeMs });
      this.seq = Math.max(this.seq, seq);
    }
  }

  get sequence(): number {
    return this.seq;
  }

  get memtableSize(): number {
    return this.mem.size;
  }

  put(key: string, value: string): void {
    this.mem.set(key, value);
    if (this.mem.size >= this.flushThreshold) this.flush();
  }

  delete(key: string): void {
    this.mem.set(key, null);
    if (this.mem.size >= this.flushThreshold) this.flush();
  }

  get(key: string): string | undefined {
    if (this.mem.has(key)) return this.mem.get(key) ?? undefined;
    for (let i = this.tables.length - 1; i >= 0; i--) {
      const { entries } = decodeSSTable(readFileSync(this.tables[i].path));
      const hit = entries.find((e) => e.key === key);
      if (hit) return hit.value ?? undefined;
    }
    return undefined;
  }

  // Flush the memtable to a new immutable SSTable. Returns null if empty.
  flush(): SSTableMeta | null {
    if (this.mem.size === 0) return null;
    const entries: Entry[] = [...this.mem.entries()].map(([key, value]) => ({ key, value }));
    this.mem.clear();
    return this.writeTable(entries);
  }

  // Merge ALL live SSTables into one, then delete the old files from the
  // live directory. Snapshots holding hard links keep the bytes alive.
  compact(): CompactResult | null {
    this.flush();
    if (this.tables.length <= 1) return null;
    const merged = new Map<string, string | null>();
    for (const table of this.tables) {
      for (const e of decodeSSTable(readFileSync(table.path)).entries) {
        merged.set(e.key, e.value);
      }
    }
    // Tombstones are dropped at the bottommost merge.
    const entries: Entry[] = [...merged.entries()]
      .filter(([, v]) => v !== null)
      .map(([key, value]) => ({ key, value }));
    const old = [...this.tables];
    const meta = this.writeTable(entries);
    const removed: string[] = [];
    for (const table of old) {
      unlinkSync(table.path); // old file disappears from the live dir
      removed.push(table.name);
    }
    this.tables = [meta];
    return { merged: meta, removed };
  }

  // Immutable view of the currently live SSTables (a "checkpoint").
  listSSTables(): SSTableMeta[] {
    return this.tables.map((t) => ({ ...t }));
  }

  // Full merged view of every live key (memtable + tables).
  readAll(): Map<string, string> {
    const merged = new Map<string, string | null>();
    for (const table of this.tables) {
      for (const e of decodeSSTable(readFileSync(table.path)).entries) {
        merged.set(e.key, e.value);
      }
    }
    for (const [k, v] of this.mem) merged.set(k, v);
    const out = new Map<string, string>();
    for (const [k, v] of merged) if (v !== null) out.set(k, v);
    return out;
  }

  private writeTable(entries: Entry[]): SSTableMeta {
    this.seq += 1;
    const name = `sst-${String(this.seq).padStart(6, '0')}.sst`;
    const path = join(this.dir, name);
    const buf = encodeSSTable(entries);
    writeFileSync(path, buf);
    const meta: SSTableMeta = { name, path, size: buf.length, seq: this.seq, createdAt: Date.now() };
    this.tables.push(meta);
    return meta;
  }
}

export function tableExists(meta: SSTableMeta): boolean {
  return existsSync(meta.path);
}
