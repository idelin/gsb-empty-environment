/**
 * Fault-tolerant persistent store for geospatial cell indexes.
 *
 * Durability model:
 *  - every mutation is a transaction: WAL records are appended and
 *    fsynced BEFORE the in-memory state is applied;
 *  - if the WAL append fails (e.g. ENOSPC), the WAL is truncated back to
 *    the pre-transaction offset and memory is left untouched
 *    (state-safe rollback);
 *  - on open, the snapshot checksum is verified and the WAL is replayed;
 *    a torn/corrupt WAL tail (sudden power loss) is truncated
 *    (self-healing) and corrupt snapshots are quarantined;
 *  - commits are serialised through an async mutex so concurrent writers
 *    cannot interleave WAL records or lose updates.
 */

import { createHash } from 'node:crypto';
import { promises as fs } from 'node:fs';
import path from 'node:path';

/* ------------------------------------------------------------------ */
/* injectable file IO (fault injectors wrap this interface)            */
/* ------------------------------------------------------------------ */

export interface FileIO {
  readFile(p: string): Promise<Buffer>;
  writeFile(p: string, data: string | Buffer): Promise<void>;
  appendFile(p: string, data: string | Buffer): Promise<void>;
  rename(from: string, to: string): Promise<void>;
  unlink(p: string): Promise<void>;
  readdir(p: string): Promise<string[]>;
  mkdir(p: string): Promise<void>;
  sync(p: string): Promise<void>;
  truncate(p: string, len: number): Promise<void>;
  stat(p: string): Promise<{ size: number }>;
  exists(p: string): Promise<boolean>;
}

export const nodeFileIO: FileIO = {
  readFile: (p) => fs.readFile(p),
  writeFile: (p, d) => fs.writeFile(p, d),
  appendFile: (p, d) => fs.appendFile(p, d),
  rename: (a, b) => fs.rename(a, b),
  unlink: (p) => fs.unlink(p),
  readdir: (p) => fs.readdir(p),
  mkdir: (p) => fs.mkdir(p, { recursive: true }).then(() => {}),
  sync: async (p) => {
    const h = await fs.open(p, 'r+');
    try {
      await h.sync();
    } finally {
      await h.close();
    }
  },
  truncate: (p, len) => fs.truncate(p, len),
  stat: async (p) => {
    const s = await fs.stat(p);
    return { size: s.size };
  },
  exists: async (p) => {
    try {
      await fs.access(p);
      return true;
    } catch {
      return false;
    }
  },
};

/* ------------------------------------------------------------------ */
/* records                                                             */
/* ------------------------------------------------------------------ */

export type OpType = 'put' | 'del';

export interface WalRecord {
  seq: number;
  opId: string;
  type: OpType;
  cell: string;
  payload: string | null;
  crc: string;
}

export interface StoreEntry {
  cell: string;
  payload: string;
}

function checksum(text: string): string {
  return createHash('sha256').update(text).digest('hex').slice(0, 16);
}

function recordBody(rec: Omit<WalRecord, 'crc'>): string {
  return `${rec.seq}|${rec.opId}|${rec.type}|${rec.cell}|${rec.payload ?? ''}`;
}

export function serializeRecord(rec: Omit<WalRecord, 'crc'>): string {
  return JSON.stringify({ ...rec, crc: checksum(recordBody(rec)) }) + '\n';
}

export function parseRecord(line: string): WalRecord {
  const rec = JSON.parse(line) as WalRecord;
  if (
    typeof rec.seq !== 'number' ||
    typeof rec.opId !== 'string' ||
    (rec.type !== 'put' && rec.type !== 'del') ||
    typeof rec.cell !== 'string' ||
    checksum(recordBody(rec)) !== rec.crc
  ) {
    throw new Error('corrupt WAL record');
  }
  return rec;
}

export class StoreError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'StoreError';
  }
}

/* ------------------------------------------------------------------ */
/* store                                                               */
/* ------------------------------------------------------------------ */

const SNAPSHOT_FILE = 'snapshot.json';
const WAL_FILE = 'wal.log';

export class GridIndexStore {
  private data = new Map<string, string>();
  private appliedOpIds = new Set<string>();
  private nextSeq = 0;
  private opCounter = 0;
  private readonly instanceId = `${process.pid}:${Date.now()}:${Math.random()
    .toString(36)
    .slice(2, 10)}`;
  private queue: Promise<unknown> = Promise.resolve();
  private closed = false;

  private readonly dir: string;
  private readonly io: FileIO;

  private constructor(dir: string, io: FileIO) {
    this.dir = dir;
    this.io = io;
  }

  /** Open (and recover) a store on the given directory. */
  static async open(dir: string, io: FileIO = nodeFileIO): Promise<GridIndexStore> {
    const store = new GridIndexStore(dir, io);
    await store.recover();
    return store;
  }

  private get walPath(): string {
    return path.join(this.dir, WAL_FILE);
  }

  private get snapshotPath(): string {
    return path.join(this.dir, SNAPSHOT_FILE);
  }

  private async recover(): Promise<void> {
    await this.io.mkdir(this.dir);
    // 1. snapshot (checksum-verified; corrupt snapshots are quarantined)
    let snapshotSeq = -1;
    if (await this.io.exists(this.snapshotPath)) {
      try {
        const raw = (await this.io.readFile(this.snapshotPath)).toString('utf8');
        const snap = JSON.parse(raw) as {
          seq: number;
          entries: [string, string][];
          opIds: string[];
          crc: string;
        };
        const body = JSON.stringify({
          seq: snap.seq,
          entries: snap.entries,
          opIds: snap.opIds,
        });
        if (checksum(body) !== snap.crc) throw new Error('snapshot crc mismatch');
        snapshotSeq = snap.seq;
        for (const [cell, payload] of snap.entries) this.data.set(cell, payload);
        for (const id of snap.opIds) this.appliedOpIds.add(id);
      } catch {
        const quarantine = `${this.snapshotPath}.corrupt-${Date.now()}`;
        await this.io.rename(this.snapshotPath, quarantine);
        this.data.clear();
        this.appliedOpIds.clear();
        snapshotSeq = -1;
      }
    }
    // 2. WAL replay; torn tail is truncated (self-healing)
    if (await this.io.exists(this.walPath)) {
      const raw = await this.io.readFile(this.walPath);
      let offset = 0;
      let validEnd = 0;
      let maxSeq = snapshotSeq;
      while (offset < raw.length) {
        const nl = raw.indexOf(0x0a, offset);
        if (nl < 0) break; // no trailing newline: torn record
        const line = raw.subarray(offset, nl).toString('utf8');
        if (line.length > 0) {
          try {
            const rec = parseRecord(line);
            if (rec.seq > snapshotSeq && !this.appliedOpIds.has(rec.opId)) {
              this.apply(rec);
              this.appliedOpIds.add(rec.opId);
            }
            if (rec.seq > maxSeq) maxSeq = rec.seq;
          } catch {
            break; // corrupt record: everything from here is torn tail
          }
        }
        offset = nl + 1;
        validEnd = offset;
      }
      if (validEnd < raw.length) {
        await this.io.truncate(this.walPath, validEnd);
      }
      this.nextSeq = maxSeq + 1;
    } else {
      this.nextSeq = snapshotSeq + 1;
    }
  }

  private apply(rec: Pick<WalRecord, 'type' | 'cell' | 'payload'>): void {
    if (rec.type === 'put') this.data.set(rec.cell, rec.payload ?? '');
    else this.data.delete(rec.cell);
  }

  /** Serialise commits through an async mutex. */
  private locked<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.queue.then(fn);
    this.queue = run.catch(() => {});
    return run;
  }

  private ensureOpen(): void {
    if (this.closed) throw new StoreError('store is closed');
  }

  /**
   * Commit a batch of mutations atomically: WAL first (append + fsync),
   * then memory.  On IO failure the WAL is rolled back to the
   * pre-transaction offset and memory is never touched.
   */
  async commit(
    ops: { type: OpType; cell: string; payload?: string }[],
  ): Promise<void> {
    if (ops.length === 0) return;
    return this.locked(async () => {
      this.ensureOpen();
      const records: WalRecord[] = ops.map((op) => ({
        seq: this.nextSeq++,
        opId: `${this.instanceId}:${this.opCounter++}`,
        type: op.type,
        cell: op.cell,
        payload: op.payload ?? null,
        crc: '',
      }));
      const bytes = records.map((r) => serializeRecord(r)).join('');
      let preSize = 0;
      try {
        preSize = (await this.io.stat(this.walPath)).size;
      } catch {
        preSize = 0; // WAL does not exist yet
      }
      try {
        await this.io.appendFile(this.walPath, bytes);
        await this.io.sync(this.walPath);
      } catch (err) {
        // rollback: best-effort truncate back to the pre-transaction
        // offset; memory state was never modified.
        this.nextSeq -= records.length;
        try {
          await this.io.truncate(this.walPath, preSize);
        } catch {
          // power loss during rollback: recovery will discard the tail
        }
        throw new StoreError('commit failed; transaction rolled back', {
          cause: err,
        });
      }
      for (const rec of records) {
        this.apply(rec);
        this.appliedOpIds.add(rec.opId);
      }
    });
  }

  async put(cell: string, payload: string): Promise<void> {
    return this.commit([{ type: 'put', cell, payload }]);
  }

  async delete(cell: string): Promise<void> {
    return this.commit([{ type: 'del', cell }]);
  }

  /**
   * Persist a checksum-verified snapshot and reset the WAL.  The
   * snapshot is written to a temp file and atomically renamed; on
   * failure (e.g. ENOSPC) the temp file is removed and the previous
   * snapshot + WAL remain fully valid.
   */
  async checkpoint(): Promise<void> {
    return this.locked(async () => {
      this.ensureOpen();
      const body = JSON.stringify({
        seq: this.nextSeq - 1,
        entries: [...this.data.entries()].sort(),
        opIds: [...this.appliedOpIds].sort(),
      });
      const tmp = `${this.snapshotPath}.tmp`;
      try {
        await this.io.writeFile(tmp, JSON.stringify({ ...JSON.parse(body), crc: checksum(body) }));
        await this.io.sync(tmp);
        await this.io.rename(tmp, this.snapshotPath);
        await this.io.truncate(this.walPath, 0);
        await this.io.sync(this.walPath);
      } catch (err) {
        try {
          await this.io.unlink(tmp);
        } catch {
          // temp file may not exist; nothing to clean
        }
        throw new StoreError('checkpoint failed; previous state intact', {
          cause: err,
        });
      }
    });
  }

  /* ---------------- read API (consistent in-memory view) ---------- */

  get(cell: string): string | undefined {
    return this.data.get(cell);
  }

  has(cell: string): boolean {
    return this.data.has(cell);
  }

  get size(): number {
    return this.data.size;
  }

  entries(): StoreEntry[] {
    return [...this.data.entries()]
      .map(([cell, payload]) => ({ cell, payload }))
      .sort((a, b) => (a.cell < b.cell ? -1 : 1));
  }

  /** Idempotent application of a replicated operation. */
  applyReplicated(rec: Pick<WalRecord, 'opId' | 'type' | 'cell' | 'payload'>): boolean {
    if (this.appliedOpIds.has(rec.opId)) return false;
    this.appliedOpIds.add(rec.opId);
    this.apply(rec);
    return true;
  }

  async close(): Promise<void> {
    return this.locked(async () => {
      this.closed = true;
    });
  }
}
