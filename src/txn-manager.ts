import {
  LogRecordType,
  NULL_LSN,
  TxnStatus,
  type LSN,
  type LogRecord,
  type PageId,
  type PageValue,
  type TxnId,
  type TxnStatus as TxnStatusType,
  type TxnTableEntrySnapshot,
} from "./log-record.ts";
import type { BufferPool } from "./storage.ts";
import type { WriteAheadLog } from "./wal.ts";

interface ActiveTxn {
  txnId: TxnId;
  status: TxnStatusType;
  lastLSN: LSN;
}

/**
 * Pre-crash transaction manager. Generates the WAL workload (BEGIN / UPDATE /
 * COMMIT / ABORT / CLR / END / CHECKPOINT) and applies updates to the buffer
 * pool. The crash-recovery engine in recovery.ts later replays/undoes this.
 */
export class TransactionManager {
  private nextTxnId = 1;
  private readonly txns = new Map<TxnId, ActiveTxn>();
  private readonly dirtyPages = new Map<PageId, LSN>(); // pageId -> recLSN

  private readonly wal: WriteAheadLog;
  private readonly pool: BufferPool;

  constructor(wal: WriteAheadLog, pool: BufferPool) {
    this.wal = wal;
    this.pool = pool;
  }

  async begin(): Promise<TxnId> {
    const txnId = this.nextTxnId++;
    const record = await this.append({ type: LogRecordType.BEGIN, txnId, prevLSN: NULL_LSN });
    this.txns.set(txnId, { txnId, status: TxnStatus.RUNNING, lastLSN: record.lsn });
    return txnId;
  }

  async update(txnId: TxnId, pageId: PageId, after: PageValue): Promise<LSN> {
    const txn = this.requireTxn(txnId);
    const page = this.pool.fetchPage(pageId);
    const before = page.data;
    const record = await this.append({
      type: LogRecordType.UPDATE,
      txnId,
      prevLSN: txn.lastLSN,
      pageId,
      before,
      after,
    });
    this.pool.applyLogged(pageId, after, record.lsn);
    txn.lastLSN = record.lsn;
    if (!this.dirtyPages.has(pageId)) this.dirtyPages.set(pageId, record.lsn);
    return record.lsn;
  }

  /** WAL protocol: force log up to the commit record before acknowledging. */
  async commit(txnId: TxnId): Promise<void> {
    const txn = this.requireTxn(txnId);
    const record = await this.append({
      type: LogRecordType.COMMIT,
      txnId,
      prevLSN: txn.lastLSN,
    });
    txn.lastLSN = record.lsn;
    txn.status = TxnStatus.COMMITTED;
    this.wal.flush(record.lsn);
    await this.end(txnId);
  }

  /** Runtime rollback: walk the txn chain backwards, emitting CLRs. */
  async abort(txnId: TxnId): Promise<void> {
    const txn = this.requireTxn(txnId);
    const abortRecord = await this.append({
      type: LogRecordType.ABORT,
      txnId,
      prevLSN: txn.lastLSN,
    });
    txn.status = TxnStatus.ABORTING;
    txn.lastLSN = abortRecord.lsn;
    await this.rollbackChain(txnId, abortRecord.lsn);
    await this.end(txnId);
  }

  /** Shared undo walk used by abort(); mirrors recovery-phase undo logic. */
  private async rollbackChain(txnId: TxnId, fromLSN: LSN): Promise<void> {
    const txn = this.requireTxn(txnId);
    let current: LogRecord | undefined = this.wal
      .allRecords()
      .find((r) => r.lsn === fromLSN);
    while (current && current.txnId === txnId) {
      if (current.type === LogRecordType.UPDATE) {
        const clr = await this.append({
          type: LogRecordType.CLR,
          txnId,
          prevLSN: txn.lastLSN,
          pageId: current.pageId,
          before: current.after,
          after: current.before,
          undoNextLSN: current.prevLSN,
        });
        this.pool.applyLogged(current.pageId, current.before, clr.lsn);
        txn.lastLSN = clr.lsn;
        current = this.wal.allRecords().find((r) => r.lsn === current!.prevLSN);
      } else if (current.type === LogRecordType.CLR) {
        current =
          current.undoNextLSN === NULL_LSN
            ? undefined
            : this.wal.allRecords().find((r) => r.lsn === current!.undoNextLSN);
      } else if (current.type === LogRecordType.BEGIN) {
        break;
      } else {
        current = this.wal.allRecords().find((r) => r.lsn === current!.prevLSN);
      }
    }
  }

  private async end(txnId: TxnId): Promise<void> {
    const txn = this.requireTxn(txnId);
    const record = await this.append({
      type: LogRecordType.END,
      txnId,
      prevLSN: txn.lastLSN,
    });
    txn.lastLSN = record.lsn;
    this.txns.delete(txnId);
    this.wal.flush(record.lsn);
  }

  /** Fuzzy checkpoint: snapshot txn table + dirty page table into the WAL. */
  async checkpoint(): Promise<LSN> {
    const txnTable: TxnTableEntrySnapshot[] = [...this.txns.values()].map((t) => ({
      txnId: t.txnId,
      status: t.status,
      lastLSN: t.lastLSN,
    }));
    const dirtyPageTable = [...this.dirtyPages.entries()].map(([pageId, recLSN]) => ({
      pageId,
      recLSN,
    }));
    const record = await this.append({
      type: LogRecordType.CHECKPOINT,
      txnId: -1 as TxnId,
      prevLSN: NULL_LSN,
      txnTable,
      dirtyPageTable,
    });
    this.wal.flush(record.lsn);
    return record.lsn;
  }

  /** Flush WAL tail without ending transactions (partial durability). */
  flushLog(): void {
    this.wal.flush();
  }

  activeTxnIds(): TxnId[] {
    return [...this.txns.keys()];
  }

  private async append(
    record: Omit<LogRecord, "lsn">,
  ): Promise<LogRecord> {
    return this.wal.append(record);
  }

  private requireTxn(txnId: TxnId): ActiveTxn {
    const txn = this.txns.get(txnId);
    if (!txn) throw new Error(`transaction ${txnId} is not active`);
    return txn;
  }
}
