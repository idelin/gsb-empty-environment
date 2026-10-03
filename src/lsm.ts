import { promises as fs } from "node:fs";
import type { Entry } from "./entry.ts";
import { compareKeys, isTombstone, mergeEntries, TOMBSTONE } from "./entry.ts";
import { MemTable } from "./memtable.ts";
import { SSTable } from "./sstable.ts";

export interface LSMOptions {
  memtableFlushThreshold?: number;
  l0CompactionThreshold?: number;
  levelCapacities?: number[];
  maxLevels?: number;
}

interface ResolvedOptions {
  memtableFlushThreshold: number;
  l0CompactionThreshold: number;
  levelCapacities: number[];
  maxLevels: number;
}

export interface LevelView {
  level: number;
  files: { id: number; entries: Entry[] }[];
}

const DEFAULT_OPTIONS: ResolvedOptions = {
  memtableFlushThreshold: 4,
  l0CompactionThreshold: 2,
  levelCapacities: [8, 32, 128],
  maxLevels: 4,
};

export class LSMTree {
  private readonly dir: string;
  private readonly opts: ResolvedOptions;
  private readonly memtable = new MemTable();
  private readonly levels: SSTable[][];
  private seq = 0;
  private nextSstId = 1;

  private constructor(dir: string, opts: ResolvedOptions) {
    this.dir = dir;
    this.opts = opts;
    this.levels = Array.from({ length: opts.maxLevels }, () => []);
  }

  static async open(dir: string, options: LSMOptions = {}): Promise<LSMTree> {
    await fs.mkdir(dir, { recursive: true });
    const opts: ResolvedOptions = { ...DEFAULT_OPTIONS, ...options };
    return new LSMTree(dir, opts);
  }

  async put(key: string, value: string): Promise<void> {
    this.memtable.put(key, value, ++this.seq);
    await this.maybeFlush();
  }

  async delete(key: string): Promise<void> {
    this.memtable.put(key, TOMBSTONE, ++this.seq);
    await this.maybeFlush();
  }

  async get(key: string): Promise<string | undefined> {
    const fromMemtable = this.memtable.get(key);
    if (fromMemtable) {
      return isTombstone(fromMemtable) ? undefined : (fromMemtable.value as string);
    }
    for (let level = 0; level < this.levels.length; level++) {
      const files = this.levels[level];
      for (let i = files.length - 1; i >= 0; i--) {
        const entry = files[i].get(key);
        if (entry) {
          return isTombstone(entry) ? undefined : (entry.value as string);
        }
      }
    }
    return undefined;
  }

  async scan(start?: string, end?: string): Promise<[string, string][]> {
    const newest = new Map<string, Entry>();
    const absorb = (entry: Entry): void => {
      const current = newest.get(entry.key);
      if (!current || entry.seq > current.seq) {
        newest.set(entry.key, entry);
      }
    };
    for (const level of this.levels) {
      for (const sst of level) {
        for (const entry of sst.all()) {
          absorb(entry);
        }
      }
    }
    for (const entry of this.memtable.sortedEntries()) {
      absorb(entry);
    }
    return [...newest.values()]
      .filter((entry) => !isTombstone(entry))
      .filter(
        (entry) =>
          (start === undefined || entry.key >= start) &&
          (end === undefined || entry.key <= end),
      )
      .sort(compareKeys)
      .map((entry) => [entry.key, entry.value as string]);
  }

  async flush(): Promise<void> {
    if (this.memtable.size === 0) {
      return;
    }
    const sst = await SSTable.write(this.dir, this.nextSstId++, this.memtable.sortedEntries());
    this.memtable.clear();
    this.levels[0].push(sst);
    await this.maybeCompact();
  }

  async compact(): Promise<void> {
    if (this.levels[0].length > 0) {
      await this.compactLevel(0);
    }
    for (let level = 1; level < this.levels.length - 1; level++) {
      if (this.levelSize(level) > this.capacity(level)) {
        await this.compactLevel(level);
      }
    }
  }

  async compactLevel(level: number): Promise<void> {
    if (level < 0 || level >= this.levels.length - 1) {
      throw new RangeError(`cannot compact level ${level}`);
    }
    const moving = this.levels[level];
    if (moving.length === 0) {
      return;
    }
    const target = level + 1;
    const inputs = [...moving, ...this.levels[target]];
    const dropTombstones = this.isBottomMost(target);
    const merged = mergeEntries(
      inputs.map((sst) => sst.all()),
      dropTombstones,
    );
    for (const sst of inputs) {
      await sst.destroy();
    }
    this.levels[level] = [];
    if (merged.length > 0) {
      const sst = await SSTable.write(this.dir, this.nextSstId++, merged);
      this.levels[target] = [sst];
    } else {
      this.levels[target] = [];
    }
  }

  inspect(): LevelView[] {
    return this.levels.map((files, level) => ({
      level,
      files: files.map((sst) => ({ id: sst.id, entries: sst.all() })),
    }));
  }

  findOnDisk(key: string): { level: number; fileId: number; entry: Entry }[] {
    const hits: { level: number; fileId: number; entry: Entry }[] = [];
    this.levels.forEach((files, level) => {
      for (const sst of files) {
        const entry = sst.get(key);
        if (entry) {
          hits.push({ level, fileId: sst.id, entry });
        }
      }
    });
    return hits;
  }

  diskEntryCount(): number {
    let total = 0;
    for (const level of this.levels) {
      for (const sst of level) {
        total += sst.size;
      }
    }
    return total;
  }

  private isBottomMost(level: number): boolean {
    if (level === this.levels.length - 1) {
      return true;
    }
    for (let deeper = level + 1; deeper < this.levels.length; deeper++) {
      if (this.levels[deeper].length > 0) {
        return false;
      }
    }
    return true;
  }

  private levelSize(level: number): number {
    return this.levels[level].reduce((sum, sst) => sum + sst.size, 0);
  }

  private capacity(level: number): number {
    const caps = this.opts.levelCapacities;
    const idx = level - 1;
    if (idx < caps.length) {
      return caps[idx];
    }
    return caps[caps.length - 1] * 4 ** (idx - caps.length + 1);
  }

  private async maybeFlush(): Promise<void> {
    if (this.memtable.size >= this.opts.memtableFlushThreshold) {
      await this.flush();
    }
  }

  private async maybeCompact(): Promise<void> {
    while (this.levels[0].length >= this.opts.l0CompactionThreshold) {
      await this.compactLevel(0);
    }
    for (let level = 1; level < this.levels.length - 1; level++) {
      while (this.levelSize(level) > this.capacity(level)) {
        await this.compactLevel(level);
      }
    }
  }
}
