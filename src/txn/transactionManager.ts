import { EngineError, ErrorCodes } from "../errors.ts";
import type { MetricsRegistry } from "../metrics/registry.ts";
import type { BufferPool } from "../storage/bufferPool.ts";
import { LogType, type CheckpointTTEntry, type TxnStatus } from "../wal/record.ts";
import type { WalWriter } from "../wal/wal.ts";

interface ActiveTxn {
  txnId: number;
  status: TxnStatus;
  lastLSN: number;
}

/**
 * Transaction manager used to build a WAL workload before the simulated
 * crash. Maintains the in-memory transaction table mirrored into checkpoints.
 */
export class TransactionManager {
  private nextTxnId = 1;
  private readonly table = new Map<number, ActiveTxn>();
  private readonly wal: WalWriter;
  private readonly bufferPool: BufferPool;
  private readonly metrics: MetricsRegistry;

  constructor(wal: WalWriter, bufferPool: BufferPool, metrics: MetricsRegistry) {
    this.wal = wal;
    this.bufferPool = bufferPool;
    this.metrics = metrics;
  }

  begin(): number {
    const txnId = this.nextTxnId++;
    this.table.set(txnId, { txnId, status: "RUNNING", lastLSN: 0 });
    this.metrics.counter("txn.begun").inc();
    return txnId;
  }

  private mustGet(txnId: number): ActiveTxn {
    const txn = this.table.get(txnId);
    if (txn === undefined || txn.status !== "RUNNING") {
      throw new EngineError(ErrorCodes.TXN_INVALID_STATE, `txn ${txnId} is not running`);
    }
    return txn;
  }

  update(txnId: number, pageId: number, beforeImage: string, afterImage: string): number {
    const txn = this.mustGet(txnId);
    const record = this.wal.append({
      prevLSN: txn.lastLSN,
      txnId,
      type: LogType.UPDATE,
      pageId,
      beforeImage,
      afterImage,
    });
    txn.lastLSN = record.lsn;
    this.bufferPool.updatePage(pageId, afterImage, record.lsn);
    return record.lsn;
  }

  commit(txnId: number): void {
    const txn = this.mustGet(txnId);
    const commit = this.wal.append({ prevLSN: txn.lastLSN, txnId, type: LogType.COMMIT });
    txn.lastLSN = commit.lsn;
    txn.status = "COMMITTED";
    this.wal.flush(); // commit durability barrier: log before data
    // END is written once the commit is fully processed; txn leaves the table.
    this.wal.append({ prevLSN: txn.lastLSN, txnId, type: LogType.END });
    this.table.delete(txnId);
    this.metrics.counter("txn.committed").inc();
  }

  /**
   * Fuzzy checkpoint: BEGIN_CHECKPOINT followed by END_CHECKPOINT carrying a
   * snapshot of the transaction table and the dirty page table
   * (pageId -> pageLSN of the oldest update that dirtied the page).
   */
  checkpoint(): void {
    this.wal.append({ prevLSN: 0, txnId: -1, type: LogType.BEGIN_CHECKPOINT });
    const checkpointTT: CheckpointTTEntry[] = [...this.table.values()].map((t) => ({
      txnId: t.txnId,
      status: t.status,
      lastLSN: t.lastLSN,
    }));
    const checkpointDPT: Record<string, number> = {};
    for (const [pageId, pageLSN] of this.bufferPool.dirtyPages()) {
      checkpointDPT[String(pageId)] = pageLSN;
    }
    this.wal.append({
      prevLSN: 0,
      txnId: -1,
      type: LogType.END_CHECKPOINT,
      checkpointDPT,
      checkpointTT,
    });
    this.metrics.counter("txn.checkpoints").inc();
  }

  /** Simulated crash: WAL stays on disk, volatile state is simply dropped. */
  crash(): void {
    this.wal.close();
    this.table.clear();
  }
}
