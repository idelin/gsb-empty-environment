import { mkdirSync, readdirSync, unlinkSync } from 'node:fs';
import path from 'node:path';
import { MemTable } from './memtable.ts';
import { SsTable } from './sstable.ts';
import { WAL_OP_PUT, WalSegmentWriter, replaySegment, segmentName } from './wal.ts';

export const DEFAULT_MEMTABLE_THRESHOLD_BYTES = 4 * 1024 * 1024;

export interface LsmOptions {
  dir: string;
  memtableThresholdBytes?: number;
  fsync?: boolean;
  onRotate?: (stats: LsmStats) => void;
  onFlush?: (sstFile: string, stats: LsmStats) => void;
}

export interface LsmStats {
  rotations: number;
  flushesCompleted: number;
  flushesPending: number;
  immutableCount: number;
  l0FileCount: number;
  walSegmentCount: number;
  activeMemTableBytes: number;
}

// MemTable lifecycle state machine:
//   active --(size >= threshold)--> immutable --(scheduler)--> flushing --(sst written)--> flushed
// Foreground writes only ever touch the active memtable + its WAL segment;
// rotation is O(1) (swap table, open next WAL segment) so writes never stall.
export class LsmEngine {
  readonly dir: string;
  private readonly threshold: number;
  private readonly fsync: boolean;
  private readonly onRotate?: (stats: LsmStats) => void;
  private readonly onFlush?: (sstFile: string, stats: LsmStats) => void;

  private active!: MemTable;
  private wal!: WalSegmentWriter;
  private readonly immutables: MemTable[] = [];
  private readonly l0: SsTable[] = [];
  private readonly immutableWalSeq = new Map<number, number>();
  private memSeq = 0;
  private walSeq = 0;
  private sstSeq = 0;
  private flushChain: Promise<void> = Promise.resolve();
  private rotations = 0;
  private flushesCompleted = 0;
  private closed = false;

  private constructor(options: LsmOptions) {
    this.dir = options.dir;
    this.threshold = options.memtableThresholdBytes ?? DEFAULT_MEMTABLE_THRESHOLD_BYTES;
    this.fsync = options.fsync ?? false;
    this.onRotate = options.onRotate;
    this.onFlush = options.onFlush;
  }

  static async open(options: LsmOptions): Promise<LsmEngine> {
    const engine = new LsmEngine(options);
    mkdirSync(engine.walDir, { recursive: true });
    mkdirSync(engine.l0Dir, { recursive: true });
    await engine.recover();
    return engine;
  }

  get walDir(): string {
    return path.join(this.dir, 'wal');
  }

  get l0Dir(): string {
    return path.join(this.dir, 'l0');
  }

  put(key: string, value: Buffer | string): void {
    this.assertOpen();
    const buf = typeof value === 'string' ? Buffer.from(value) : value;
    this.wal.appendPut(key, buf);
    if (this.fsync) this.wal.sync();
    this.active.put(key, buf);
    if (this.active.sizeBytes >= this.threshold) this.rotate();
  }

  delete(key: string): void {
    this.assertOpen();
    this.wal.appendDelete(key);
    if (this.fsync) this.wal.sync();
    this.active.delete(key);
    if (this.active.sizeBytes >= this.threshold) this.rotate();
  }

  get(key: string): Buffer | undefined {
    this.assertOpen();
    const fromActive = this.active.get(key);
    if (fromActive !== undefined) return fromActive === null ? undefined : fromActive;
    for (let i = this.immutables.length - 1; i >= 0; i--) {
      const v = this.immutables[i].get(key);
      if (v !== undefined) return v === null ? undefined : v;
    }
    for (let i = this.l0.length - 1; i >= 0; i--) {
      const v = this.l0[i].get(key);
      if (v !== undefined) return v === null ? undefined : v;
    }
    return undefined;
  }

  getStats(): LsmStats {
    return {
      rotations: this.rotations,
      flushesCompleted: this.flushesCompleted,
      flushesPending: this.immutables.filter((m) => m.state !== 'flushed').length,
      immutableCount: this.immutables.length,
      l0FileCount: this.l0.length,
      walSegmentCount: readdirSync(this.walDir).filter((f) => f.endsWith('.log')).length,
      activeMemTableBytes: this.active.sizeBytes,
    };
  }

  async waitIdle(): Promise<void> {
    let chain: Promise<void>;
    do {
      chain = this.flushChain;
      await chain;
    } while (chain !== this.flushChain);
  }

  async close(): Promise<void> {
    if (this.closed) return;
    await this.waitIdle();
    this.wal.close();
    this.closed = true;
  }

  private rotate(): void {
    const frozen = this.active;
    frozen.state = 'immutable';
    this.immutables.push(frozen);
    this.immutableWalSeq.set(frozen.id, this.walSeq);
    const oldWal = this.wal;
    this.walSeq += 1;
    this.wal = new WalSegmentWriter(this.walDir, this.walSeq);
    this.active = new MemTable(this.memSeq++);
    this.rotations += 1;
    // fsync+close of the frozen WAL segment happens off the write path,
    // chained ahead of its flush so rotation stays O(1) for foreground writes.
    this.scheduleFlush(frozen, oldWal);
    this.onRotate?.(this.getStats());
  }

  // Single-threaded sequential scheduler: flushes are chained onto one
  // promise queue, so immutable memtables hit L0 strictly in FIFO order.
  private scheduleFlush(mt: MemTable, oldWal?: WalSegmentWriter): void {
    this.flushChain = this.flushChain.then(async () => {
      if (oldWal) await oldWal.closeAsync();
      await this.flush(mt);
    });
  }

  private async flush(mt: MemTable): Promise<void> {
    mt.state = 'flushing';
    const fileName = this.nextSstSeq().toString().padStart(6, '0') + '.sst';
    const filePath = path.join(this.l0Dir, fileName);
    await SsTable.write(filePath, mt.sortedEntries());
    this.l0.push(await SsTable.load(filePath));
    mt.state = 'flushed';
    const idx = this.immutables.indexOf(mt);
    if (idx >= 0) this.immutables.splice(idx, 1);
    const walSeq = this.immutableWalSeq.get(mt.id);
    if (walSeq !== undefined) {
      this.immutableWalSeq.delete(mt.id);
      try {
        unlinkSync(path.join(this.walDir, segmentName(walSeq)));
      } catch {
        // segment already gone (e.g. crash between steps); replay is idempotent
      }
    }
    this.flushesCompleted += 1;
    this.onFlush?.(fileName, this.getStats());
  }

  private nextSstSeq(): number {
    return ++this.sstSeq;
  }

  private async recover(): Promise<void> {
    const sstFiles = readdirSync(this.l0Dir).filter((f) => f.endsWith('.sst')).sort();
    for (const f of sstFiles) {
      this.l0.push(await SsTable.load(path.join(this.l0Dir, f)));
      this.sstSeq = Math.max(this.sstSeq, parseInt(f, 10));
    }
    const segments = readdirSync(this.walDir).filter((f) => f.endsWith('.log')).sort();
    if (segments.length === 0) {
      this.walSeq = 1;
      this.active = new MemTable(this.memSeq++);
      this.wal = new WalSegmentWriter(this.walDir, this.walSeq);
      return;
    }
    for (const [i, f] of segments.entries()) {
      const seq = parseInt(f, 10);
      const mt = new MemTable(this.memSeq++);
      replaySegment(path.join(this.walDir, f), (op, key, value) => {
        if (op === WAL_OP_PUT) mt.put(key, value);
        else mt.delete(key);
      });
      if (i === segments.length - 1) {
        this.active = mt;
        this.walSeq = seq;
        this.wal = new WalSegmentWriter(this.walDir, this.walSeq);
      } else {
        mt.state = 'immutable';
        this.immutables.push(mt);
        this.immutableWalSeq.set(mt.id, seq);
        this.scheduleFlush(mt);
      }
    }
  }

  private assertOpen(): void {
    if (this.closed) throw new Error('LsmEngine is closed');
  }
}
