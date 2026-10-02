import { appendFileSync, closeSync, existsSync, fsyncSync, mkdirSync, openSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { ColumnFamily } from "./column-family.ts";
import { SsTable } from "./sstable.ts";
import { Wal, type WalOp } from "./wal.ts";

type ManifestRecord =
  | { type: "create_cf"; name: string }
  | { type: "flush"; cf: string; file: string };

/** Read handle pinning one column family at a fixed sequence number. */
export class Snapshot {
  readonly cfName: string;
  readonly seq: number;
  private readonly engine: Engine;

  constructor(cfName: string, seq: number, engine: Engine) {
    this.cfName = cfName;
    this.seq = seq;
    this.engine = engine;
  }

  get(key: string): string | undefined {
    return this.engine.get(this.cfName, key, this.seq);
  }

  keys(): string[] {
    return this.engine.keys(this.cfName, this.seq);
  }
}

/**
 * Single LSM instance hosting multiple logically isolated column families.
 * - Each CF: private MemTable + private SSTable metadata.
 * - All CFs share ONE physical WAL; a multi-CF batch is one WAL record,
 *   which makes cross-CF writes atomic and recovery aligned.
 */
export class Engine {
  private readonly dir: string;
  private readonly walPath: string;
  private readonly manifestPath: string;
  private wal: Wal;
  private manifestFd: number;
  private cfs = new Map<string, ColumnFamily>();
  private seq = 0;

  private constructor(dir: string) {
    this.dir = dir;
    this.walPath = join(dir, "LOG.wal");
    this.manifestPath = join(dir, "MANIFEST");
    mkdirSync(dir, { recursive: true });
    this.manifestFd = openSync(this.manifestPath, "a");
    this.wal = new Wal(this.walPath);
  }

  /** Open (or create) an instance, replaying MANIFEST + WAL for recovery. */
  static open(dir: string): Engine {
    const engine = new Engine(dir);

    // 1. Rebuild column families and their SSTable metadata from MANIFEST.
    if (existsSync(engine.manifestPath)) {
      for (const line of readFileSync(engine.manifestPath, "utf8").split("\n")) {
        if (line === "") continue;
        const rec = JSON.parse(line) as ManifestRecord;
        if (rec.type === "create_cf") {
          engine.cfs.set(rec.name, new ColumnFamily(rec.name));
        } else {
          engine.cf(rec.cf).attachSsTable(SsTable.load(join(engine.dir, rec.file)));
        }
      }
    }

    // 2. Replay the shared WAL: each record is one atomic multi-CF batch,
    //    so recovered families are always aligned at batch boundaries.
    for (const batch of Wal.replay(engine.walPath)) {
      for (const op of batch.ops) {
        engine.cf(op.cf).applyPut(op.key, batch.seq, op.value);
      }
      engine.seq = Math.max(engine.seq, batch.seq);
    }
    return engine;
  }

  createColumnFamily(name: string): ColumnFamily {
    if (this.cfs.has(name)) throw new Error(`column family exists: ${name}`);
    const cf = new ColumnFamily(name);
    this.cfs.set(name, cf);
    this.appendManifest({ type: "create_cf", name });
    return cf;
  }

  listColumnFamilies(): string[] {
    return [...this.cfs.keys()];
  }

  /**
   * Atomically write ops spanning any number of column families:
   * one sequence number, one WAL record, one fsync — all or nothing.
   */
  writeBatch(ops: WalOp[]): number {
    if (ops.length === 0) return this.seq;
    for (const op of ops) this.cf(op.cf); // validate CFs exist before logging
    const seq = ++this.seq;
    this.wal.appendBatch({ seq, ops });
    for (const op of ops) this.cf(op.cf).applyPut(op.key, seq, op.value);
    return seq;
  }

  put(cfName: string, key: string, value: string): number {
    return this.writeBatch([{ cf: cfName, key, value }]);
  }

  delete(cfName: string, key: string): number {
    return this.writeBatch([{ cf: cfName, key, value: null }]);
  }

  get(cfName: string, key: string, atSeq: number = this.seq): string | undefined {
    return this.cf(cfName).get(key, atSeq);
  }

  keys(cfName: string, atSeq: number = this.seq): string[] {
    return this.cf(cfName).keys(atSeq);
  }

  /** Independent snapshot of a single column family. */
  snapshot(cfName: string): Snapshot {
    this.cf(cfName);
    return new Snapshot(cfName, this.seq, this);
  }

  /** Flush one CF's MemTable into its own SSTable (metadata is per-CF). */
  flush(cfName: string): void {
    const cf = this.cf(cfName);
    const file = `${cfName}-${String(this.seq).padStart(8, "0")}-${cf.sstableCount}.sst`;
    cf.flush(join(this.dir, file));
    this.appendManifest({ type: "flush", cf: cfName, file });
  }

  columnFamily(name: string): ColumnFamily {
    return this.cf(name);
  }

  close(): void {
    this.wal.close();
    closeSync(this.manifestFd);
  }

  private cf(name: string): ColumnFamily {
    const cf = this.cfs.get(name);
    if (!cf) throw new Error(`unknown column family: ${name}`);
    return cf;
  }

  private appendManifest(rec: ManifestRecord): void {
    appendFileSync(this.manifestFd, JSON.stringify(rec) + "\n");
    fsyncSync(this.manifestFd);
  }
}
