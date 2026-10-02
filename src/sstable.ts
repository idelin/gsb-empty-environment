import { readFileSync, writeFileSync } from "node:fs";
import type { Probe, VersionedValue } from "./memtable.ts";

interface SsTableFile {
  entries: Array<[string, VersionedValue]>;
}

/**
 * Immutable sorted table on disk. Each column family tracks its own
 * list of SSTable files (its own metadata); files are never shared.
 */
export class SsTable {
  readonly filePath: string;
  readonly maxSeq: number;
  private readonly entries: Array<[string, VersionedValue]>;

  private constructor(filePath: string, entries: Array<[string, VersionedValue]>) {
    this.filePath = filePath;
    this.entries = entries;
    this.maxSeq = entries.reduce((m, [, v]) => Math.max(m, v.seq), 0);
  }

  static write(filePath: string, entries: Array<[string, VersionedValue]>): SsTable {
    const file: SsTableFile = { entries };
    writeFileSync(filePath, JSON.stringify(file));
    return new SsTable(filePath, entries);
  }

  static load(filePath: string): SsTable {
    const file = JSON.parse(readFileSync(filePath, "utf8")) as SsTableFile;
    return new SsTable(filePath, file.entries);
  }

  probe(key: string, atSeq: number): Probe {
    let lo = 0;
    let hi = this.entries.length - 1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const [k, v] = this.entries[mid];
      if (k === key) {
        if (v.seq > atSeq) return { found: false, value: null };
        return { found: true, value: v.value };
      }
      if (k < key) lo = mid + 1;
      else hi = mid - 1;
    }
    return { found: false, value: null };
  }

  get(key: string, atSeq: number): string | undefined {
    const p = this.probe(key, atSeq);
    return p.found && p.value !== null ? p.value : undefined;
  }

  keys(atSeq: number): string[] {
    const out: string[] = [];
    for (const [k, v] of this.entries) {
      if (v.seq <= atSeq && v.value !== null) out.push(k);
    }
    return out;
  }
}
