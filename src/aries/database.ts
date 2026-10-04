import type { MetricsRegistry } from '../metrics/registry.ts';
import { BufferPool, DiskStorage } from './storage.ts';
import { RecordType, TxnStatus, type TxnTableEntry } from './types.ts';
import { WalWriter } from './wal.ts';

interface TxnState {
  status: TxnStatus;
  lastLSN: number;
}

export interface DatabaseOptions {
  metrics?: MetricsRegistry;
  bufferCapacity?: number;
}

/**
 * Minimal transactional engine used to produce WAL files: page-granularity
 * physiological logging, steal/no-force buffer management, fuzzy checkpoints.
 * `crash()` drops the buffer pool without flushing, simulating a power loss.
 */
export class Database {
  private readonly wal: WalWriter;
  private readonly storage: DiskStorage;
  private buffer: BufferPool | null;
  private readonly txns = new Map<number, TxnState>();
  private nextTxnId = 1;
  private crashed = false;

  constructor(walPath: string, storagePath: string, options: DatabaseOptions = {}) {
    this.wal = new WalWriter(walPath);
    this.storage = new DiskStorage(storagePath);
    this.buffer = new BufferPool(
      this.storage,
      options.bufferCapacity ?? 128,
      options.metrics,
    );
  }

  private ensureAlive(): BufferPool {
    if (this.crashed || !this.buffer) {
      throw new Error('database has crashed; start a RecoveryEngine instead');
    }
    return this.buffer;
  }

  begin(): number {
    this.ensureAlive();
    const txnId = this.nextTxnId++;
    this.txns.set(txnId, { status: TxnStatus.Running, lastLSN: 0 });
    return txnId;
  }

  update(txnId: number, pageId: string, value: unknown): void {
    const buffer = this.ensureAlive();
    const txn = this.txns.get(txnId);
    if (!txn || txn.status !== TxnStatus.Running) {
      throw new Error(`transaction ${txnId} is not active`);
    }
    const page = buffer.fetch(pageId);
    const record = this.wal.append({
      type: RecordType.Update,
      txnId,
      prevLSN: txn.lastLSN,
      pageId,
      before: page.value,
      after: value,
    });
    page.value = value;
    page.pageLSN = record.lsn;
    buffer.markDirty(pageId, record.lsn);
    txn.lastLSN = record.lsn;
  }

  commit(txnId: number): void {
    this.ensureAlive();
    const txn = this.txns.get(txnId);
    if (!txn || txn.status !== TxnStatus.Running) {
      throw new Error(`transaction ${txnId} is not active`);
    }
    const commit = this.wal.append({
      type: RecordType.Commit,
      txnId,
      prevLSN: txn.lastLSN,
    });
    txn.status = TxnStatus.Committed;
    txn.lastLSN = commit.lsn;
    this.wal.append({
      type: RecordType.End,
      txnId,
      prevLSN: commit.lsn,
    });
    this.txns.delete(txnId);
  }

  /** Fuzzy checkpoint: snapshots the txn table and dirty page table. */
  checkpoint(): void {
    const buffer = this.ensureAlive();
    const begin = this.wal.append({ type: RecordType.BeginCheckpoint });
    const txnTable: TxnTableEntry[] = [...this.txns.entries()].map(
      ([txnId, state]) => ({
        txnId,
        status: state.status,
        lastLSN: state.lastLSN,
      }),
    );
    this.wal.append({
      type: RecordType.EndCheckpoint,
      beginLSN: begin.lsn,
      txnTable,
      dirtyPages: buffer.dirtyPages(),
    });
  }

  /** Test hook: force one page out to disk (steal). */
  flushPage(pageId: string): void {
    this.ensureAlive().flushPage(pageId);
  }

  /** Simulates a crash: the buffer pool is discarded without flushing. */
  crash(): void {
    this.buffer = null;
    this.crashed = true;
    this.txns.clear();
  }
}
