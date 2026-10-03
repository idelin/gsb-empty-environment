/**
 * ARIES 三阶段崩溃恢复引擎：Analysis -> Redo -> Undo。
 *
 * 核心不变量：
 *  1. Analysis 从最后一个完整检查点出发，精确重建 TransactionTable 与
 *     DirtyPageTable；redoLSN = min(DPT.recLSN)。
 *  2. Redo 重复历史：对每条 UPDATE/CLR，当且仅当 page 在 DPT 中、
 *     record.lsn >= DPT[page] 且 page.pageLSN < record.lsn 时重放，
 *     幂等且对已落盘页零副作用。
 *  3. Undo 按 lastLSN 大根堆全局逆序回滚所有 loser 事务，每步先写
 *     CLR（undoNextLSN 指向被撤销记录的 prevLSN）再改页，保证
 *     崩溃重启后沿 undoNextLSN 续走，绝不重复撤销（日志拓扑约束）。
 */
import {
  cloneTxnTable,
  isPageModifying,
  LogType,
  TxnStatus,
  type ClrRecord,
  type DirtyPageTable,
  type LSN,
  type TransactionTable,
  type TxnId,
  type UpdateRecord,
} from './log-record.ts';
import { WalIndex, WalWriter, readWal } from './wal.ts';
import { DiskStorage, type PageImage } from './storage.ts';
import type { PageId } from './log-record.ts';

/** Undo/Redo 写入页的最小接口：磁盘存储与缓冲池均可充当。 */
export interface PageSink {
  readPage(pageId: PageId): PageImage;
  writePage(pageId: PageId, image: PageImage): void;
}

export interface AnalysisResult {
  txnTable: TransactionTable;
  dirtyPageTable: DirtyPageTable;
  /** Redo 扫描起点：DPT 中的最小 recLSN；DPT 为空时为日志末尾之后。 */
  redoLSN: LSN;
  /** 扫描起点（检查点 LSN 或日志头），供测试断言。 */
  scanStartLSN: LSN;
}

export function analyze(index: WalIndex): AnalysisResult {
  const txnTable: TransactionTable = new Map();
  const dirtyPageTable: DirtyPageTable = new Map();

  let startIndex = 0;
  let scanStartLSN: LSN = 1;
  const checkpoint = index.lastEndCheckpoint();
  if (checkpoint) {
    for (const entry of checkpoint.txnTable) {
      txnTable.set(entry.txnId, { ...entry });
    }
    for (const [pageId, recLSN] of checkpoint.dirtyPageTable) {
      dirtyPageTable.set(pageId, recLSN);
    }
    startIndex = index.firstIndexAtOrAfter(checkpoint.lsn) + 1;
    scanStartLSN = checkpoint.lsn;
  }

  for (let i = startIndex; i < index.records.length; i += 1) {
    const record = index.records[i];
    switch (record.type) {
      case LogType.Update:
      case LogType.Clr: {
        const txnId = record.txnId;
        if (txnId === null) throw new Error('page record without txnId');
        const entry = txnTable.get(txnId);
        if (entry) {
          entry.lastLSN = record.lsn;
        } else {
          txnTable.set(txnId, { txnId, status: TxnStatus.Running, lastLSN: record.lsn });
        }
        if (!dirtyPageTable.has(record.pageId)) {
          dirtyPageTable.set(record.pageId, record.lsn);
        }
        break;
      }
      case LogType.Commit: {
        const entry = txnTable.get(record.txnId as TxnId);
        if (!entry) throw new Error(`COMMIT for unknown txn ${record.txnId}`);
        entry.status = TxnStatus.Committed;
        entry.lastLSN = record.lsn;
        break;
      }
      case LogType.Abort: {
        const entry = txnTable.get(record.txnId as TxnId);
        if (!entry) throw new Error(`ABORT for unknown txn ${record.txnId}`);
        entry.status = TxnStatus.Aborting;
        entry.lastLSN = record.lsn;
        break;
      }
      case LogType.End: {
        txnTable.delete(record.txnId as TxnId);
        break;
      }
      case LogType.BeginCheckpoint:
      case LogType.EndCheckpoint:
        break;
    }
  }

  let redoLSN: LSN = index.records.length > 0
    ? index.records[index.records.length - 1].lsn + 1
    : 1;
  for (const recLSN of dirtyPageTable.values()) {
    if (recLSN < redoLSN) redoLSN = recLSN;
  }
  return { txnTable, dirtyPageTable, redoLSN, scanStartLSN };
}

export interface RedoResult {
  /** 实际重放的记录 LSN（按重放顺序）。 */
  redone: LSN[];
  /** 因 pageLSN/DPT 检查而跳过的可重放记录数。 */
  skipped: number;
}

/** Redo：历史重复。直接作用于磁盘页映像，幂等。 */
export function redo(
  index: WalIndex,
  dirtyPageTable: DirtyPageTable,
  redoLSN: LSN,
  storage: DiskStorage,
): RedoResult {
  const result: RedoResult = { redone: [], skipped: 0 };
  for (let i = index.firstIndexAtOrAfter(redoLSN); i < index.records.length; i += 1) {
    const record = index.records[i];
    if (!isPageModifying(record)) continue;
    const recLSN = dirtyPageTable.get(record.pageId);
    if (recLSN === undefined || record.lsn < recLSN) {
      result.skipped += 1;
      continue;
    }
    const page = storage.readPage(record.pageId);
    if (page.pageLSN >= record.lsn) {
      result.skipped += 1;
      continue;
    }
    storage.writePage(record.pageId, { value: record.after, pageLSN: record.lsn });
    result.redone.push(record.lsn);
  }
  return result;
}

export interface UndoContext {
  index: WalIndex;
  wal: WalWriter;
  storage: PageSink;
}

/** 撤销一条 UPDATE：先写 CLR（WAL 先行），再把 before 映像落到页上。 */
function undoOneUpdate(
  ctx: UndoContext,
  txnId: TxnId,
  record: UpdateRecord,
  prevLSN: LSN,
): ClrRecord {
  const page = ctx.storage.readPage(record.pageId);
  const clr = ctx.wal.append({
    type: LogType.Clr,
    txnId,
    prevLSN,
    pageId: record.pageId,
    before: page.value,
    after: record.before,
    undoNextLSN: record.prevLSN,
  }) as ClrRecord;
  ctx.storage.writePage(record.pageId, { value: record.before, pageLSN: clr.lsn });
  return clr;
}

/**
 * 回滚单个事务：从 fromLSN 沿 prevLSN 链逆走，UPDATE 逐条写 CLR 补偿，
 * CLR 经 undoNextLSN 跳过已补偿部分。
 * 事务管理器的运行时 ABORT 与恢复期的 Undo 共用此实现。
 */
export function rollbackTransaction(
  ctx: UndoContext,
  txnId: TxnId,
  fromLSN: LSN,
  prevOfLast: LSN | null,
  maxClrs = Number.POSITIVE_INFINITY,
): { lastLSN: LSN; clrsWritten: LSN[]; done: boolean } {
  const clrsWritten: LSN[] = [];
  let cursor: LSN | null = fromLSN;
  let lastLSN = prevOfLast ?? fromLSN;
  let done = false;

  while (cursor !== null) {
    const record = ctx.index.get(cursor);
    if (record.txnId !== txnId) {
      throw new Error(`prevLSN chain of txn ${txnId} crosses into txn ${record.txnId}`);
    }
    if (record.type === LogType.Update) {
      if (clrsWritten.length >= maxClrs) {
        return { lastLSN, clrsWritten, done };
      }
      const clr = undoOneUpdate(ctx, txnId, record, lastLSN);
      clrsWritten.push(clr.lsn);
      lastLSN = clr.lsn;
      cursor = record.prevLSN;
    } else if (record.type === LogType.Clr) {
      cursor = record.undoNextLSN;
    } else if (record.type === LogType.Abort) {
      cursor = record.prevLSN;
    } else {
      throw new Error(`unexpected record type ${record.type} in undo chain of txn ${txnId}`);
    }
  }
  done = true;
  return { lastLSN, clrsWritten, done };
}

export interface UndoResult {
  /** 每个 loser 事务被写入的 CLR LSN 序列。 */
  clrsByTxn: Map<TxnId, LSN[]>;
  /** 被写入 END 记录的事务（全部 loser）。 */
  endedTxns: TxnId[];
  /** 因 maxClrs 限制而提前停止（用于崩溃注入测试）。 */
  truncated: boolean;
}

/**
 * Undo：对所有 loser 事务按 lastLSN 大根堆全局逆序回滚。
 * 拓扑约束：每一步处理全局最大 LSN，保证跨事务的撤销顺序与
 * 原日志逆序一致，满足可恢复性。
 */
export function undo(
  ctx: UndoContext,
  txnTable: TransactionTable,
  maxClrs = Number.POSITIVE_INFINITY,
): UndoResult {
  const losers = new Map<TxnId, LSN>();
  for (const [txnId, entry] of txnTable) {
    if (entry.status !== TxnStatus.Committed) {
      losers.set(txnId, entry.lastLSN);
    }
  }

  const result: UndoResult = { clrsByTxn: new Map(), endedTxns: [], truncated: false };
  let clrBudget = maxClrs;
  // 每个 loser 在 WAL 中的最近记录 LSN：新写 CLR/END 的 prevLSN 来源。
  const lastWritten = new Map<TxnId, LSN>();
  for (const [txnId, entry] of txnTable) {
    if (entry.status !== TxnStatus.Committed) {
      lastWritten.set(txnId, entry.lastLSN);
    }
  }

  while (losers.size > 0) {
    // 取全局最大 LSN 的 loser（平局取较大 txnId，保证确定性）。
    let maxTxn: TxnId | null = null;
    let maxLsn: LSN = -1;
    for (const [txnId, lsn] of losers) {
      if (lsn > maxLsn || (lsn === maxLsn && (maxTxn === null || txnId > maxTxn))) {
        maxLsn = lsn;
        maxTxn = txnId;
      }
    }
    const txnId = maxTxn as TxnId;
    const record = ctx.index.get(maxLsn);

    if (record.type === LogType.Update) {
      if (clrBudget <= 0) {
        result.truncated = true;
        return result;
      }
      const clr = undoOneUpdate(ctx, txnId, record, lastWritten.get(txnId) as LSN);
      lastWritten.set(txnId, clr.lsn);
      const clrs = result.clrsByTxn.get(txnId) ?? [];
      clrs.push(clr.lsn);
      result.clrsByTxn.set(txnId, clrs);
      clrBudget -= 1;
      if (clr.undoNextLSN !== null) {
        losers.set(txnId, clr.undoNextLSN);
      } else {
        appendEnd(ctx, txnId, clr.lsn);
        losers.delete(txnId);
        result.endedTxns.push(txnId);
      }
    } else if (record.type === LogType.Clr) {
      if (record.undoNextLSN !== null) {
        losers.set(txnId, record.undoNextLSN);
      } else {
        appendEnd(ctx, txnId, lastWritten.get(txnId) as LSN);
        losers.delete(txnId);
        result.endedTxns.push(txnId);
      }
    } else if (record.type === LogType.Abort) {
      if (record.prevLSN !== null) {
        losers.set(txnId, record.prevLSN);
      } else {
        appendEnd(ctx, txnId, lastWritten.get(txnId) as LSN);
        losers.delete(txnId);
        result.endedTxns.push(txnId);
      }
    } else {
      throw new Error(`loser txn ${txnId} points at non-undoable record ${record.type}`);
    }
  }
  return result;
}

function appendEnd(ctx: UndoContext, txnId: TxnId, prevLSN: LSN): void {
  ctx.wal.append({ type: LogType.End, txnId, prevLSN });
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redo: RedoResult;
  undo: UndoResult;
  /** 恢复结束后事务表必须为空（无泄漏）。 */
  openTransactions: TxnId[];
}

export interface RecoveryOptions {
  /** 限制 Undo 写入的 CLR 总数，用于注入“恢复中再次崩溃”。 */
  maxUndoClrs?: number;
  /** 恢复完成后是否写收尾检查点（默认 true）。 */
  checkpointOnComplete?: boolean;
}

export class RecoveryEngine {
  readonly walPath: string;
  readonly storage: DiskStorage;
  #closed = false;

  constructor(walPath: string, storagePath: string) {
    this.walPath = walPath;
    this.storage = new DiskStorage(storagePath);
  }

  get isClosed(): boolean {
    return this.#closed;
  }

  recover(options: RecoveryOptions = {}): RecoveryReport {
    const index = new WalIndex(readWal(this.walPath));
    const analysis = analyze(index);

    const redoResult = redo(index, analysis.dirtyPageTable, analysis.redoLSN, this.storage);

    const wal = new WalWriter(this.walPath);
    const ctx: UndoContext = { index, wal, storage: this.storage };
    const undoResult = undo(ctx, cloneTxnTable(analysis.txnTable), options.maxUndoClrs);

    let openTransactions: TxnId[] = [];
    if (!undoResult.truncated) {
      // 收尾：空检查点标记恢复完成，保证再次恢复幂等。
      if (options.checkpointOnComplete !== false) {
        wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null });
        wal.append({
          type: LogType.EndCheckpoint,
          txnId: null,
          prevLSN: null,
          txnTable: [],
          dirtyPageTable: [],
        });
      }
      wal.flush();
    }
    wal.close();

    if (!undoResult.truncated) {
      const post = analyze(new WalIndex(readWal(this.walPath)));
      openTransactions = [...post.txnTable.keys()];
    }

    this.#closed = true;
    return { analysis, redo: redoResult, undo: undoResult, openTransactions };
  }
}
