import { LogType } from './types.ts';
import type { LSN } from './types.ts';
import type { WriteAheadLog } from './wal.ts';
import type { TransactionManager } from './txn.ts';
import type { BufferPool } from './storage.ts';

/**
 * Fuzzy checkpointing: a BEGIN_CHECKPOINT / END_CHECKPOINT pair where the END
 * record carries a consistent snapshot of the transaction table and the
 * dirty-page table. Recovery analysis starts from the most recent pair.
 */
export class CheckpointManager {
  private readonly wal: WriteAheadLog;
  private readonly txns: TransactionManager;
  private readonly pool: BufferPool;

  constructor(wal: WriteAheadLog, txns: TransactionManager, pool: BufferPool) {
    this.wal = wal;
    this.txns = txns;
    this.pool = pool;
  }

  /** Write a checkpoint and force the log. Returns the END_CHECKPOINT LSN. */
  checkpoint(): LSN {
    this.wal.append({ type: LogType.BEGIN_CHECKPOINT, txnId: null, prevLSN: null });
    const txnTable = this.txns.txnTableSnapshot();
    const dirtyPages = [...this.pool.dirtyPageTable()].map(([pageId, recLSN]) => ({
      pageId,
      recLSN,
    }));
    const end = this.wal.append({
      type: LogType.END_CHECKPOINT,
      txnId: null,
      prevLSN: null,
      txnTable,
      dirtyPages,
    });
    this.wal.flush();
    return end.lsn;
  }
}
