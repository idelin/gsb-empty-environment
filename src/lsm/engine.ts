import { createHash } from 'node:crypto';
import * as fs from 'node:fs';
import * as path from 'node:path';

/** Metadata describing one immutable SSTable file on disk. */
export interface SSTableMeta {
  /** File name relative to the data directory, e.g. "000007.sst". */
  file: string;
  /** Number of key/value entries stored in the table. */
  entries: number;
  /** File size in bytes. */
  size: number;
  /** SHA-256 hex digest of the file content, computed at flush time. */
  sha256: string;
}

export interface LsmOptions {
  /** Flush the memtable to a new SSTable after this many puts. Default 1000. */
  flushThreshold?: number;
  /** Trigger compaction when live SSTable count reaches this value. Default 4. */
  compactThreshold?: number;
}

function sha256File(filePath: string): string {
  return createHash('sha256').update(fs.readFileSync(filePath)).digest('hex');
}

function encodeEntry(key: string, value: string): string {
  return `${Buffer.from(key).toString('base64')}\t${Buffer.from(value).toString('base64')}\n`;
}

function decodeEntry(line: string): [string, string] {
  const tab = line.indexOf('\t');
  return [
    Buffer.from(line.slice(0, tab), 'base64').toString(),
    Buffer.from(line.slice(tab + 1), 'base64').toString(),
  ];
}

/**
 * A minimal but real LSM-tree storage engine.
 *
 * Writes land in an in-memory sorted memtable. When the memtable reaches
 * `flushThreshold` entries it is frozen and written out as a new immutable
 * SSTable file. Once an SSTable is flushed its bytes never change; the only
 * lifecycle events are "created by flush" and "unlinked by compaction".
 * That immutability is what makes zero-copy hard-link snapshots safe.
 */
export class LsmEngine {
  readonly dir: string;
  private memtable = new Map<string, string>();
  private tables: SSTableMeta[] = []; // oldest -> newest
  private nextFileId = 1;
  private readonly flushThreshold: number;
  private readonly compactThreshold: number;

  private constructor(dir: string, options: LsmOptions) {
    this.dir = dir;
    this.flushThreshold = options.flushThreshold ?? 1000;
    this.compactThreshold = options.compactThreshold ?? 4;
  }

  /** Open (or create) an engine rooted at `dir`, recovering live SSTables from disk. */
  static open(dir: string, options: LsmOptions = {}): LsmEngine {
    fs.mkdirSync(dir, { recursive: true });
    const engine = new LsmEngine(dir, options);
    const files = fs.readdirSync(dir).filter((f) => f.endsWith('.sst')).sort();
    for (const file of files) {
      const full = path.join(dir, file);
      const entries = fs.readFileSync(full, 'utf8').split('\n').filter(Boolean).length;
      engine.tables.push({
        file,
        entries,
        size: fs.statSync(full).size,
        sha256: sha256File(full),
      });
      engine.nextFileId = Math.max(engine.nextFileId, parseInt(file, 10) + 1);
    }
    return engine;
  }

  put(key: string, value: string): void {
    this.memtable.set(key, value);
    if (this.memtable.size >= this.flushThreshold) this.flush();
  }

  get(key: string): string | undefined {
    const cached = this.memtable.get(key);
    if (cached !== undefined) return cached;
    for (let i = this.tables.length - 1; i >= 0; i--) {
      const found = this.readTable(this.tables[i]).get(key);
      if (found !== undefined) return found;
    }
    return undefined;
  }

  /** Freeze the memtable into a new immutable SSTable. */
  flush(): SSTableMeta | undefined {
    if (this.memtable.size === 0) return undefined;
    const sorted = [...this.memtable.entries()].sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
    const content = sorted.map(([k, v]) => encodeEntry(k, v)).join('');
    const meta = this.writeTable(content);
    this.memtable.clear();
    this.tables.push(meta);
    if (this.tables.length >= this.compactThreshold) this.compact();
    return meta;
  }

  /**
   * Merge all live SSTables (plus the current memtable) into one new table,
   * then unlink the superseded files. Newest write wins per key.
   */
  compact(): SSTableMeta {
    const merged = new Map<string, string>();
    for (const table of this.tables) {
      for (const [k, v] of this.readTable(table)) merged.set(k, v);
    }
    for (const [k, v] of this.memtable) merged.set(k, v);

    const sorted = [...merged.entries()].sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
    const content = sorted.map(([k, v]) => encodeEntry(k, v)).join('');
    const meta = this.writeTable(content);

    // Publish the new table first, only then remove the obsolete files.
    const obsolete = this.tables;
    this.tables = [meta];
    this.memtable.clear();
    for (const old of obsolete) {
      // If a snapshot holds a hard link, unlink only drops one name;
      // the snapshot's inode keeps the bytes alive.
      fs.unlinkSync(path.join(this.dir, old.file));
    }
    return meta;
  }

  /** Live SSTables, oldest first. The returned array is a defensive copy. */
  listSSTables(): SSTableMeta[] {
    return [...this.tables];
  }

  private writeTable(content: string): SSTableMeta {
    const file = `${String(this.nextFileId++).padStart(6, '0')}.sst`;
    const full = path.join(this.dir, file);
    const tmp = `${full}.tmp`;
    fs.writeFileSync(tmp, content);
    fs.renameSync(tmp, full); // atomic publish
    return {
      file,
      entries: content === '' ? 0 : content.split('\n').length - 1,
      size: Buffer.byteLength(content),
      sha256: createHash('sha256').update(content).digest('hex'),
    };
  }

  private readTable(meta: SSTableMeta): Map<string, string> {
    const out = new Map<string, string>();
    const lines = fs.readFileSync(path.join(this.dir, meta.file), 'utf8').split('\n');
    for (const line of lines) {
      if (line === '') continue;
      const [k, v] = decodeEntry(line);
      out.set(k, v);
    }
    return out;
  }
}
