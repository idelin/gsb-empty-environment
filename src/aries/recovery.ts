/**
 * ARIES 崩溃恢复引擎：Analysis → Redo → Undo 三阶段。
 *
 * - Analysis：从最近 Checkpoint 起扫描，重建事务表（TT）与脏页表（DPT），
 *   确定 Redo 起点（DPT 最小 recLSN）与失败事务集合。
 * - Redo：历史重复。从 Redo 起点正向扫描，对所有 UPDATE/CLR 记录，
 *   若页不在 DPT 或 record.lsn < recLSN 或 page.pageLSN >= record.lsn 则跳过，
 *   否则重放 after 镜像并推进 pageLSN。
 * - Undo：对全部失败事务按 lastLSN 降序反向扫描，写 CLR 补偿日志，
 *   CLR 的 undoNextLSN 指向被撤销记录的 prevLSN，保证重启恢复不重复撤销。
 */
import type {
  LSN,
  LogRecord,
  TransactionTable,
  DirtyPageTable,
} from "./logRecord.ts";
import { LogType, TxnStatus } from "./logRecord.ts";
import { WriteAheadLog } from "./wal.ts";
import { DiskStorage } from "./storage.ts";

export interface AnalysisResult {
  transactionTable: TransactionTable;
  dirtyPageTable: DirtyPageTable;
  /** Redo 起点：DPT 中最小 recLSN；无脏页则为日志末尾 */
  redoLSN: LSN;
  /** 崩溃时未提交（需回滚）的事务 */
  loserTxns: number[];
}

export interface RecoveryStats {
  analysis: AnalysisResult;
  redoneLSNs: LSN[];
  undoneLSNs: LSN[];
  clrLSNs: LSN[];
}

export class RecoveryManager {
  private readonly wal: WriteAheadLog;
  private readonly disk: DiskStorage;

  constructor(wal: WriteAheadLog, disk: DiskStorage) {
    this.wal = wal;
    this.disk = disk;
  }

  /** 阶段一：分析 */
  analysis(): AnalysisResult {
    const transactionTable: TransactionTable = new Map();
    const dirtyPageTable: DirtyPageTable = new Map();

    const checkpoint = this.wal.lastCheckpoint();
    let startLSN: LSN = 1;
    if (checkpoint) {
      startLSN = checkpoint.lsn;
      for (const e of checkpoint.checkpoint!.transactionTable) {
        transactionTable.set(e.txnId, { ...e });
      }
      for (const e of checkpoint.checkpoint!.dirtyPageTable) {
        dirtyPageTable.set(e.pageId, { ...e });
      }
    }

    for (const rec of this.wal.scanFrom(startLSN)) {
      switch (rec.type) {
        case LogType.UPDATE:
        case LogType.CLR: {
          const entry = transactionTable.get(rec.txnId);
          if (entry) {
            entry.lastLSN = rec.lsn;
          } else {
            transactionTable.set(rec.txnId, {
              txnId: rec.txnId,
              status: TxnStatus.RUNNING,
              lastLSN: rec.lsn,
            });
          }
          if (rec.pageId !== undefined && !dirtyPageTable.has(rec.pageId)) {
            dirtyPageTable.set(rec.pageId, { pageId: rec.pageId, recLSN: rec.lsn });
          }
          break;
        }
        case LogType.COMMIT: {
          const entry = transactionTable.get(rec.txnId);
          if (entry) {
            entry.status = TxnStatus.COMMITTED;
            entry.lastLSN = rec.lsn;
          }
          break;
        }
        case LogType.ABORT: {
          const entry = transactionTable.get(rec.txnId);
          if (entry) {
            entry.status = TxnStatus.ABORTING;
            entry.lastLSN = rec.lsn;
          }
          break;
        }
        case LogType.END: {
          transactionTable.delete(rec.txnId);
          break;
        }
        case LogType.CHECKPOINT:
          break;
      }
    }

    let redoLSN: LSN = this.wal.lastLSN + 1;
    for (const { recLSN } of dirtyPageTable.values()) {
      if (recLSN < redoLSN) redoLSN = recLSN;
    }

    const loserTxns = [...transactionTable.values()]
      .filter((e) => e.status !== TxnStatus.COMMITTED)
      .map((e) => e.txnId)
      .sort((a, b) => a - b);

    return { transactionTable, dirtyPageTable, redoLSN, loserTxns };
  }

  /** 阶段二：重做（历史重复，含 CLR） */
  redo(analysis: AnalysisResult): LSN[] {
    const redone: LSN[] = [];
    for (const rec of this.wal.scanFrom(analysis.redoLSN)) {
      if (rec.type !== LogType.UPDATE && rec.type !== LogType.CLR) continue;
      const pageId = rec.pageId!;
      const dptEntry = analysis.dirtyPageTable.get(pageId);
      if (!dptEntry) continue; // 崩溃前已刷盘，不在脏页表
      if (rec.lsn < dptEntry.recLSN) continue; // 早于该页变脏点
      const page = this.disk.readPage(pageId);
      if (page.pageLSN >= rec.lsn) continue; // 已落盘
      page.data = rec.after!;
      page.pageLSN = rec.lsn;
      this.disk.writePage(page);
      redone.push(rec.lsn);
    }
    return redone;
  }

  /** 阶段三：撤销（反向扫描 + CLR 补偿） */
  undo(analysis: AnalysisResult): { undoneLSNs: LSN[]; clrLSNs: LSN[] } {
    const undoneLSNs: LSN[] = [];
    const clrLSNs: LSN[] = [];

    // 待处理 LSN 集合：按 LSN 降序取最大者处理（多事务交错的全局逆序）
    const toUndo = new Set<LSN>();
    for (const txnId of analysis.loserTxns) {
      const entry = analysis.transactionTable.get(txnId)!;
      if (entry.lastLSN > 0) toUndo.add(entry.lastLSN);
    }

    const logByLSN = new Map<LSN, LogRecord>();
    for (const rec of this.wal.scan()) logByLSN.set(rec.lsn, rec);

    while (toUndo.size > 0) {
      const current = Math.max(...toUndo);
      toUndo.delete(current);
      const rec = logByLSN.get(current);
      if (!rec) continue;

      if (rec.type === LogType.UPDATE) {
        // 写 CLR：撤销该更新，undoNextLSN 跳过已撤销部分
        const clr = this.wal.append({
          prevLSN: analysis.transactionTable.get(rec.txnId)?.lastLSN ?? null,
          txnId: rec.txnId,
          type: LogType.CLR,
          pageId: rec.pageId,
          before: rec.after,
          after: rec.before,
          undoNextLSN: rec.prevLSN,
        });
        logByLSN.set(clr.lsn, clr);
        const page = this.disk.readPage(rec.pageId!);
        page.data = rec.before!;
        page.pageLSN = clr.lsn;
        this.disk.writePage(page);
        undoneLSNs.push(rec.lsn);
        clrLSNs.push(clr.lsn);
        const entry = analysis.transactionTable.get(rec.txnId);
        if (entry) entry.lastLSN = clr.lsn;
        if (rec.prevLSN !== null) {
          toUndo.add(rec.prevLSN);
        } else {
          this.endTxn(rec.txnId, analysis, clr.lsn, logByLSN);
        }
      } else if (rec.type === LogType.CLR) {
        // 已补偿过，跳到 undoNextLSN，避免重复撤销
        if (rec.undoNextLSN !== null && rec.undoNextLSN !== undefined) {
          toUndo.add(rec.undoNextLSN);
        } else {
          this.endTxn(rec.txnId, analysis, rec.lsn, logByLSN);
        }
      } else {
        // ABORT 等事务性记录：沿 prevLSN 链继续
        if (rec.prevLSN !== null) {
          toUndo.add(rec.prevLSN);
        } else {
          this.endTxn(rec.txnId, analysis, rec.lsn, logByLSN);
        }
      }
    }

    return { undoneLSNs, clrLSNs };
  }

  private endTxn(
    txnId: number,
    analysis: AnalysisResult,
    prevLSN: LSN,
    logByLSN: Map<LSN, LogRecord>,
  ): void {
    const end = this.wal.append({
      prevLSN,
      txnId,
      type: LogType.END,
    });
    logByLSN.set(end.lsn, end);
    analysis.transactionTable.delete(txnId);
  }

  /** 完整三阶段恢复 */
  recover(): RecoveryStats {
    const analysis = this.analysis();
    const redoneLSNs = this.redo(analysis);
    const { undoneLSNs, clrLSNs } = this.undo(analysis);
    return { analysis, redoneLSNs, undoneLSNs, clrLSNs };
  }
}
