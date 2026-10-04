import type { DiskStorage } from './storage.ts';
import type { Wal } from './wal.ts';
import type {
  LSN,
  LogRecord,
  PageId,
  TxnId,
  TxnTableEntry,
} from './types.ts';
import { NULL_LSN } from './types.ts';

export interface AnalysisResult {
  transactionTable: Map<TxnId, TxnTableEntry>;
  /** pageId -> recLSN */
  dirtyPageTable: Map<PageId, LSN>;
  checkpointLSN: LSN | null;
}

export interface UndoResult {
  loserTxns: TxnId[];
  clrCount: number;
  endCount: number;
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redoneCount: number;
  undo: UndoResult;
  /** 恢复结束后事务表必须为空（无泄漏不变式） */
  finalTransactionTable: Map<TxnId, TxnTableEntry>;
}

/**
 * ARIES 三阶段崩溃恢复引擎。
 * 恢复直接作用于磁盘镜像（崩溃后缓冲池已丢失），
 * Redo 通过 pageLSN 比较保证幂等，Undo 通过 CLR 保证可重启。
 */
export class RecoveryEngine {
  private readonly wal: Wal;
  private readonly disk: DiskStorage;

  constructor(wal: Wal, disk: DiskStorage) {
    this.wal = wal;
    this.disk = disk;
  }

  /**
   * 阶段一 Analysis：从最近的检查点快照出发向前扫描，
   * 重建崩溃瞬间的 TransactionTable 与 DirtyPageTable。
   */
  analyze(): AnalysisResult {
    const log = this.wal.records;
    const transactionTable = new Map<TxnId, TxnTableEntry>();
    const dirtyPageTable = new Map<PageId, LSN>();

    let startIndex = 0;
    let checkpointLSN: LSN | null = null;
    for (let i = log.length - 1; i >= 0; i--) {
      const record = log[i];
      if (record.type === 'END_CHECKPOINT') {
        checkpointLSN = record.lsn;
        for (const entry of record.transactions) {
          transactionTable.set(entry.txnId, { ...entry });
        }
        for (const dirty of record.dirtyPages) {
          dirtyPageTable.set(dirty.pageId, dirty.recLSN);
        }
        // 从配对的 BEGIN_CHECKPOINT 处开始扫描（模糊检查点语义）
        startIndex = i;
        while (startIndex > 0 && log[startIndex - 1].type !== 'BEGIN_CHECKPOINT') startIndex--;
        startIndex--;
        break;
      }
    }

    for (let i = Math.max(startIndex, 0); i < log.length; i++) {
      const record = log[i];
      if (record.txnId === null) continue;
      switch (record.type) {
        case 'END':
          transactionTable.delete(record.txnId);
          break;
        case 'COMMIT': {
          const e = this.mustEntry(transactionTable, record.txnId, record.lsn);
          e.status = 'committed';
          e.lastLSN = record.lsn;
          break;
        }
        case 'ABORT': {
          const e = this.mustEntry(transactionTable, record.txnId, record.lsn);
          e.status = 'aborting';
          e.lastLSN = record.lsn;
          break;
        }
        case 'UPDATE':
        case 'CLR': {
          const e = this.mustEntry(transactionTable, record.txnId, record.lsn);
          e.lastLSN = record.lsn;
          if (!dirtyPageTable.has(record.pageId)) {
            dirtyPageTable.set(record.pageId, record.lsn);
          }
          break;
        }
      }
    }
    return { transactionTable, dirtyPageTable, checkpointLSN };
  }

  private mustEntry(
    table: Map<TxnId, TxnTableEntry>,
    txnId: TxnId,
    lsn: LSN,
  ): TxnTableEntry {
    let e = table.get(txnId);
    if (e === undefined) {
      e = { txnId, status: 'running', lastLSN: lsn };
      table.set(txnId, e);
    }
    return e;
  }

  /**
   * 阶段二 Redo：历史重复。从最小 recLSN 起扫描，
   * 仅当日志 LSN 大于磁盘 pageLSN 时才重放（幂等）。
   */
  redo(analysis: AnalysisResult): number {
    let redoLSN: LSN = NULL_LSN;
    for (const recLSN of analysis.dirtyPageTable.values()) {
      if (redoLSN === NULL_LSN || recLSN < redoLSN) redoLSN = recLSN;
    }
    let redone = 0;
    for (const record of this.wal.records) {
      if (record.lsn < redoLSN) continue;
      if (record.type !== 'UPDATE' && record.type !== 'CLR') continue;
      const recLSN = analysis.dirtyPageTable.get(record.pageId);
      if (recLSN === undefined || record.lsn < recLSN) continue;
      const page = this.disk.readPage(record.pageId);
      if (page === undefined) {
        throw new Error(`Redo 遇到悬挂页面引用: ${record.pageId} (LSN ${record.lsn})`);
      }
      if (page.pageLSN < record.lsn) {
        this.disk.writePage(record.pageId, record.after, record.lsn);
        redone++;
      }
    }
    return redone;
  }

  /**
   * 阶段三 Undo：以最大 LSN 优先的全局逆序扫描回滚所有败者事务，
   * 每撤销一个 UPDATE 写一条 CLR（undoNextLSN 指向下一个待撤销记录），
   * 已提交事务补写 END。全程可重启：重复恢复不会重复补偿。
   */
  async undo(analysis: AnalysisResult): Promise<UndoResult> {
    const table = analysis.transactionTable;
    const byLSN = new Map<LSN, LogRecord>(this.wal.records.map((r) => [r.lsn, r]));

    let endCount = 0;
    for (const [txnId, entry] of [...table.entries()]) {
      if (entry.status === 'committed') {
        await this.wal.append((lsn) => ({
          type: 'END',
          lsn,
          txnId,
          prevLSN: entry.lastLSN,
        }));
        table.delete(txnId);
        endCount++;
      }
    }

    const loserTxns = [...table.keys()];
    const pending: LSN[] = [...table.values()].map((e) => e.lastLSN);
    let clrCount = 0;

    while (pending.length > 0) {
      let maxIdx = 0;
      for (let i = 1; i < pending.length; i++) {
        if (pending[i] > pending[maxIdx]) maxIdx = i;
      }
      const lsn = pending.splice(maxIdx, 1)[0];
      const record = byLSN.get(lsn);
      if (record === undefined || record.txnId === null) {
        throw new Error(`Undo 遇到悬挂日志引用: LSN ${lsn}`);
      }
      const entry = table.get(record.txnId);
      if (entry === undefined) continue;

      let next: LSN = NULL_LSN;
      if (record.type === 'UPDATE') {
        const clr = await this.wal.append((next2) => ({
          type: 'CLR',
          lsn: next2,
          txnId: record.txnId!,
          prevLSN: entry.lastLSN,
          pageId: record.pageId,
          before: record.after,
          after: record.before,
          undoNextLSN: record.prevLSN,
        }));
        this.disk.writePage(record.pageId, record.before, clr.lsn);
        byLSN.set(clr.lsn, clr);
        entry.lastLSN = clr.lsn;
        clrCount++;
        next = record.prevLSN;
      } else if (record.type === 'CLR') {
        next = record.undoNextLSN;
      } else {
        next = record.prevLSN;
      }

      if (next !== NULL_LSN) {
        pending.push(next);
      } else {
        const end = await this.wal.append((next2) => ({
          type: 'END',
          lsn: next2,
          txnId: record.txnId!,
          prevLSN: entry.lastLSN,
        }));
        byLSN.set(end.lsn, end);
        table.delete(record.txnId);
        endCount++;
      }
    }
    await this.wal.drain();
    return { loserTxns, clrCount, endCount };
  }

  async recover(): Promise<RecoveryReport> {
    const analysis = this.analyze();
    const redoneCount = this.redo(analysis);
    const undo = await this.undo(analysis);
    return {
      analysis,
      redoneCount,
      undo,
      finalTransactionTable: new Map(
        [...analysis.transactionTable.entries()].map(([k, v]) => [k, { ...v }]),
      ),
    };
  }
}
