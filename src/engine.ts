import { EventEmitter } from 'node:events';
import { mkdir, readdir, unlink } from 'node:fs/promises';
import path from 'node:path';
import { MemTable } from './memtable.ts';
import { Wal, replayWal } from './wal.ts';
import { SSTableReader, writeSSTable } from './sstable.ts';

export const DEFAULT_MEMTABLE_THRESHOLD_BYTES = 4 * 1024 * 1024; // 4 MB

export interface LsmEngineOptions {
  /** Freeze threshold for the active MemTable. Defaults to 4 MB. */
  memtableThresholdBytes?: number;
  /** Test/observability hook awaited inside the flush loop before each SSTable write. */
  flushHook?: (table: MemTable) => Promise<void> | void;
}

interface FrozenTable {
  table: MemTable;
  walPath: string;
}

const pad = (n: number): string => String(n).padStart(6, '0');

/**
 * LSM write path state machine.
 *
 *   put -> WAL append -> active MemTable
 *   active.size >= threshold
 *     -> seal WAL, freeze to Immutable, enqueue for flush   (O(1) swap)
 *     -> spawn fresh MemTable + fresh WAL segment           (writes never wait)
 *   background flusher (single sequential async chain)
 *     -> immutable -> L0 SSTable (fsync) -> delete sealed WAL segment
 */
export class LsmEngine extends EventEmitter {
  private active!: MemTable;
  private activeWal!: Wal;
  private activeWalPath!: string;
  private readonly immutables: FrozenTable[] = [];
  private readonly l0: SSTableReader[] = []; // newest first
  private flushing = false;
  private flushChain: Promise<void> = Promise.resolve();
  private walSeq = 0;
  private sstSeq = 0;
  private tableSeq = 0;
  private closed = false;
  readonly dir: string;
  private readonly options: LsmEngineOptions;

  private constructor(dir: string, options: LsmEngineOptions = {}) {
    super();
    this.dir = dir;
    this.options = options;
  }

  private get threshold(): number {
    return this.options.memtableThresholdBytes ?? DEFAULT_MEMTABLE_THRESHOLD_BYTES;
  }

  static async open(dir: string, options: LsmEngineOptions = {}): Promise<LsmEngine> {
    const engine = new LsmEngine(dir, options);
    await mkdir(dir, { recursive: true });
    await engine.recover();
    return engine;
  }

  /** Replays leftover WAL segments and loads existing L0 SSTables. */
  private async recover(): Promise<void> {
    const files = await readdir(this.dir);
    const walFiles = files.filter((f) => f.startsWith('wal-') && f.endsWith('.log')).sort();
    const sstFiles = files.filter((f) => f.startsWith('l0-') && f.endsWith('.sst')).sort();

    for (const f of sstFiles) {
      this.l0.unshift(await SSTableReader.load(path.join(this.dir, f)));
      this.sstSeq = Math.max(this.sstSeq, Number(f.slice(3, 9)) + 1);
    }

    for (const f of walFiles) {
      const entries = await replayWal(path.join(this.dir, f));
      const table = new MemTable(this.tableSeq++, this.threshold);
      for (const e of entries) table.apply(e.key.toString('utf8'), e.value);
      const walPath = path.join(this.dir, f);
      this.walSeq = Math.max(this.walSeq, Number(f.slice(4, 10)) + 1);
      if (f === walFiles[walFiles.length - 1]) {
        this.active = table;
        this.activeWalPath = walPath;
        this.activeWal = await Wal.create(walPath); // truncate & reuse tail segment
      } else {
        table.state = 'frozen';
        this.immutables.push({ table, walPath });
      }
    }

    if (!this.active) {
      this.active = new MemTable(this.tableSeq++, this.threshold);
      this.activeWalPath = path.join(this.dir, `wal-${pad(this.walSeq++)}.log`);
      this.activeWal = await Wal.create(this.activeWalPath);
    }
    if (this.immutables.length > 0) this.pump();
  }

  async put(key: string, value: Buffer | string): Promise<void> {
    this.assertOpen();
    const buf = typeof value === 'string' ? Buffer.from(value, 'utf8') : value;
    await this.activeWal.append(Buffer.from(key, 'utf8'), buf);
    this.active.apply(key, buf);
    await this.rotateIfNeeded();
  }

  async delete(key: string): Promise<void> {
    this.assertOpen();
    await this.activeWal.append(Buffer.from(key, 'utf8'), null);
    this.active.apply(key, null);
    await this.rotateIfNeeded();
  }

  /** Read path: active -> immutables (newest first) -> L0 SSTables (newest first). */
  get(key: string): Buffer | undefined {
    const hit = this.active.get(key);
    if (hit !== undefined) return hit === null ? undefined : hit;
    for (let i = this.immutables.length - 1; i >= 0; i--) {
      const v = this.immutables[i].table.get(key);
      if (v !== undefined) return v === null ? undefined : v;
    }
    for (const sst of this.l0) {
      const v = sst.get(key);
      if (v !== undefined) return v === null ? undefined : v;
    }
    return undefined;
  }

  /** O(1) rotation: freeze + swap. The flush itself never blocks this path. */
  private async rotateIfNeeded(): Promise<void> {
    if (!this.active.isFull) return;
    const frozen = this.active;
    frozen.state = 'frozen';
    const sealedWalPath = this.activeWalPath;
    await this.activeWal.seal(); // durable handoff of the frozen table
    this.immutables.push({ table: frozen, walPath: sealedWalPath });

    this.active = new MemTable(this.tableSeq++, this.threshold);
    this.activeWalPath = path.join(this.dir, `wal-${pad(this.walSeq++)}.log`);
    this.activeWal = await Wal.create(this.activeWalPath);

    this.emit('rotate', { tableId: frozen.id, sizeBytes: frozen.sizeBytes, walPath: sealedWalPath });
    this.pump();
  }

  /** Single-threaded sequential flusher: one async chain, FIFO order. */
  private pump(): void {
    if (this.flushing) return;
    this.flushing = true;
    this.flushChain = (async () => {
      try {
        while (this.immutables.length > 0) {
          const { table, walPath } = this.immutables[0];
          table.state = 'flushing';
          this.emit('flush:start', { tableId: table.id, entries: table.entryCount });
          if (this.options.flushHook) await this.options.flushHook(table);
          const sstPath = path.join(this.dir, `l0-${pad(this.sstSeq++)}.sst`);
          await writeSSTable(sstPath, table.sortedEntries());
          this.l0.unshift(await SSTableReader.load(sstPath));
          this.immutables.shift();
          table.state = 'flushed';
          await unlink(walPath); // safe: data is fsynced in L0 first
          this.emit('flush:end', { tableId: table.id, sstPath });
        }
      } finally {
        this.flushing = false;
      }
    })();
  }

  /** Resolves when every queued immutable has been flushed to L0. */
  async drain(): Promise<void> {
    while (this.flushing || this.immutables.length > 0) {
      await this.flushChain;
    }
  }

  /**
   * close({ flush: true })  — freeze + flush the active table too (clean shutdown).
   * close({ flush: false }) — finish in-flight flushes, seal the active WAL and
   *                           leave it on disk (crash-like; replayed on next open).
   */
  async close(opts: { flush?: boolean } = {}): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    const flush = opts.flush ?? true;
    if (flush && this.active.entryCount > 0) {
      this.active.sizeBytes = Math.max(this.active.sizeBytes, this.threshold); // force rotation
      await this.rotateIfNeeded();
    }
    await this.drain();
    if (this.active.entryCount === 0 || !flush) {
      await this.activeWal.seal();
      if (flush) await unlink(this.activeWalPath).catch(() => {});
    }
  }

  stats() {
    return {
      active: { id: this.active.id, sizeBytes: this.active.sizeBytes, entries: this.active.entryCount },
      immutableQueue: this.immutables.map((f) => ({ id: f.table.id, state: f.table.state })),
      l0Files: this.l0.map((s) => s.filePath),
      flushInProgress: this.flushing,
    };
  }

  private assertOpen(): void {
    if (this.closed) throw new Error('LsmEngine is closed');
  }
}
