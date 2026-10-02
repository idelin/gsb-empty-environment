import { existsSync, mkdirSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { Wal } from './wal.ts';
import { SSTable } from './sstable.ts';
import {
  ColumnFamilyState,
  type ColumnFamilyHandle,
} from './column-family.ts';

export interface Snapshot {
  readonly cf: ColumnFamilyHandle;
  readonly seq: number;
}

export interface BatchOp {
  cf: ColumnFamilyHandle;
  key: string;
  value: string | null; // null = delete
}

/**
 * One LSM instance hosting many logically isolated column families.
 * Each CF owns its MemTable and SSTable metadata; all CFs share one
 * physical WAL file, which is what makes multi-CF atomic writes and
 * aligned recovery possible.
 */
export class LSMEngine {
  private cfs = new Map<string, ColumnFamilyState>();
  private wal: Wal;
  private seq = 0;
  private sstDir: string;
  readonly dir: string;

  private constructor(dir: string) {
    this.dir = dir;
    this.sstDir = join(dir, 'sst');
    this.wal = new Wal(join(dir, 'wal.log'));
  }

  static open(dir: string): LSMEngine {
    mkdirSync(join(dir, 'sst'), { recursive: true });
    const engine = new LSMEngine(dir);
    engine.recover();
    return engine;
  }

  createColumnFamily(name: string): ColumnFamilyHandle {
    if (this.cfs.has(name)) throw new Error(`column family exists: ${name}`);
    const cf = new ColumnFamilyState(name);
    this.cfs.set(name, cf);
    return cf.handle;
  }

  dropColumnFamily(handle: ColumnFamilyHandle): void {
    if (!this.cfs.delete(handle.name)) {
      throw new Error(`unknown column family: ${handle.name}`);
    }
  }

  listColumnFamilies(): string[] {
    return [...this.cfs.keys()];
  }

  private stateOf(handle: ColumnFamilyHandle): ColumnFamilyState {
    const cf = this.cfs.get(handle.name);
    if (!cf) throw new Error(`unknown column family: ${handle.name}`);
    return cf;
  }

  put(handle: ColumnFamilyHandle, key: string, value: string): void {
    this.applyAutoCommit(handle, key, value);
  }

  delete(handle: ColumnFamilyHandle, key: string): void {
    this.applyAutoCommit(handle, key, null);
  }

  private applyAutoCommit(
    handle: ColumnFamilyHandle,
    key: string,
    value: string | null,
  ): void {
    const cf = this.stateOf(handle);
    const seq = ++this.seq;
    this.wal.append({ t: 'data', txn: null, cf: cf.handle.name, key, value, seq });
    cf.memtable.put(key, value, seq);
  }

  /**
   * Atomic multi-CF write: every op lands in the shared WAL under one
   * txn id followed by a commit marker. Either all ops become visible
   * (including after recovery) or none do.
   */
  writeBatch(ops: BatchOp[]): void {
    if (ops.length === 0) return;
    const txn = this.wal.beginTxn();
    const staged: Array<{ cf: ColumnFamilyState; key: string; value: string | null; seq: number }> = [];
    for (const op of ops) {
      const cf = this.stateOf(op.cf);
      const seq = ++this.seq;
      this.wal.append({ t: 'data', txn, cf: cf.handle.name, key: op.key, value: op.value, seq });
      staged.push({ cf, key: op.key, value: op.value, seq });
    }
    this.wal.append({ t: 'commit', txn });
    for (const s of staged) s.cf.memtable.put(s.key, s.value, s.seq);
  }

  get(
    handle: ColumnFamilyHandle,
    key: string,
    opts: { snapshot?: Snapshot } = {},
  ): string | undefined {
    const cf = this.stateOf(handle);
    const maxSeq = opts.snapshot ? opts.snapshot.seq : this.seq;
    if (opts.snapshot && opts.snapshot.cf.name !== handle.name) {
      throw new Error('snapshot belongs to a different column family');
    }
    const entry = cf.get(key, maxSeq);
    if (!entry || entry.value === null) return undefined;
    return entry.value;
  }

  /** Point-in-time, per-column-family snapshot (sequence-number based). */
  snapshot(handle: ColumnFamilyHandle): Snapshot {
    this.stateOf(handle);
    return { cf: handle, seq: this.seq };
  }

  /** Flush one CF's MemTable into a new SSTable; other CFs are untouched. */
  flush(handle: ColumnFamilyHandle): void {
    const cf = this.stateOf(handle);
    if (cf.memtable.size === 0) return;
    const id = cf.allocSstId();
    const file = join(this.sstDir, `${cf.handle.name}-${id}.sst`);
    const sst = SSTable.write(file, id, cf.handle.name, cf.memtable.sortedEntries());
    cf.sstables.unshift(sst);
    cf.memtable.clear();
  }

  sstableCount(handle: ColumnFamilyHandle): number {
    return this.stateOf(handle).sstables.length;
  }

  private recover(): void {
    // Rebuild per-CF SSTable metadata by scanning the shared sst dir.
    if (existsSync(this.sstDir)) {
      const files = readdirSync(this.sstDir)
        .filter((f) => f.endsWith('.sst'))
        .sort();
      for (const f of files) {
        const m = /^(.+)-(\d+)\.sst$/.exec(f);
        if (!m) continue;
        const [, cfName, idStr] = m;
        const id = Number(idStr);
        let cf = this.cfs.get(cfName);
        if (!cf) {
          cf = new ColumnFamilyState(cfName);
          this.cfs.set(cfName, cf);
        }
        cf.sstables.push(SSTable.load(join(this.sstDir, f), id, cfName));
        cf.observeSstId(id);
      }
      for (const cf of this.cfs.values()) {
        cf.sstables.sort((a, b) => b.meta.id - a.meta.id);
      }
    }

    // Replay the shared WAL: committed ops re-enter each CF's MemTable.
    const { ops, maxSeq, maxTxn } = this.wal.recover();
    for (const op of ops) {
      let cf = this.cfs.get(op.cf);
      if (!cf) {
        cf = new ColumnFamilyState(op.cf);
        this.cfs.set(op.cf, cf);
      }
      cf.memtable.put(op.key, op.value, op.seq);
    }
    this.seq = maxSeq;
    this.wal.restoreTxnCounter(maxTxn);
  }
}
