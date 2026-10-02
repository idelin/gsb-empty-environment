/**
 * ARIES 崩溃恢复引擎：Analysis -> Redo（历史重复）-> Undo（补偿日志 CLR）。
 */

import { WriteAheadLog, type LSN, type TxnTableEntry } from './log.ts';
import { BufferPool } from './storage.ts';

export interface AnalysisResult {
  txnTable: Map<number, TxnTableEntry>;
  dirtyPageTable: Map<number, LSN>;
  redoLSN: LSN;
}

export interface RecoveryStats {
  analysis: AnalysisResult;
  redoCount: number;
  loserTxns: number[];
  clrCount: number;
}

export class ARIESRecovery {
  private readonly wal: WriteAheadLog;
  private readonly pool: BufferPool;

  constructor(wal: WriteAheadLog, pool: BufferPool) {
    this.wal = wal;
    this.pool = pool;
  }

  /** 阶段一：从最近检查点正向扫描，重建事务表与脏页表。 */
  analyze(): AnalysisResult {
    const txnTable = new Map<number, TxnTableEntry>();
    const dirtyPageTable = new Map<number, LSN>();
    let startLSN: LSN = 1;

    const checkpointLSN = this.wal.lastCheckpointLSN();
    if (checkpointLSN !== 0) {
      const checkpoint = this.wal.record(checkpointLSN);
      if (checkpoint.type !== 'CHECKPOINT') throw new Error('corrupt checkpoint record');
      for (const entry of checkpoint.txnTable) txnTable.set(entry.txnId, { ...entry });
      for (const [pageId, recLSN] of checkpoint.dirtyPageTable) dirtyPageTable.set(pageId, recLSN);
      startLSN = checkpointLSN + 1;
    }

    for (const record of this.wal.scanFrom(startLSN)) {
      switch (record.type) {
        case 'UPDATE':
        case 'CLR': {
          const entry = txnTable.get(record.txnId);
          txnTable.set(record.txnId, {
            txnId: record.txnId,
            status: entry?.status ?? 'RUNNING',
            lastLSN: record.lsn,
          });
          if (!dirtyPageTable.has(record.pageId)) dirtyPageTable.set(record.pageId, record.lsn);
          break;
        }
        case 'COMMIT': {
          const entry = txnTable.get(record.txnId);
          if (entry === undefined) throw new Error(`COMMIT for unknown txn ${record.txnId}`);
          entry.status = 'COMMITTED';
          entry.lastLSN = record.lsn;
          break;
        }
        case 'ABORT': {
          const entry = txnTable.get(record.txnId);
          if (entry === undefined) throw new Error(`ABORT for unknown txn ${record.txnId}`);
          entry.status = 'ABORTING';
          entry.lastLSN = record.lsn;
          break;
        }
        case 'END':
          txnTable.delete(record.txnId);
          break;
        case 'CHECKPOINT':
          break;
      }
    }

    const redoLSN = dirtyPageTable.size === 0
      ? this.wal.length + 1
      : Math.min(...dirtyPageTable.values());
    return { txnTable, dirtyPageTable, redoLSN };
  }

  /** 阶段二：历史重复。从最小 recLSN 起重做全部 UPDATE/CLR（含未提交事务）。 */
  redo(analysis: AnalysisResult): number {
    let redone = 0;
    for (const record of this.wal.scanFrom(analysis.redoLSN)) {
      if (record.type !== 'UPDATE' && record.type !== 'CLR') continue;
      const recLSN = analysis.dirtyPageTable.get(record.pageId);
      if (recLSN === undefined || recLSN > record.lsn) continue;
      const page = this.pool.fetchPage(record.pageId);
      if (page.pageLSN >= record.lsn) continue; // 已在磁盘上，幂等跳过
      page.write(record.offset, record.after);
      page.pageLSN = record.lsn;
      this.pool.markDirty(record.pageId, record.lsn);
      redone++;
    }
    return redone;
  }

  /** 阶段三：按 LSN 降序反向回滚全部败者事务，每步写 CLR。 */
  undo(analysis: AnalysisResult): { loserTxns: number[]; clrCount: number } {
    const losers = [...analysis.txnTable.values()].filter((e) => e.status !== 'COMMITTED');
    const txnLastLSN = new Map<number, LSN>(losers.map((e) => [e.txnId, e.lastLSN]));
    const pending: LSN[] = losers.map((e) => e.lastLSN).filter((lsn) => lsn !== 0);
    let clrCount = 0;

    while (pending.length > 0) {
      let maxIdx = 0;
      for (let i = 1; i < pending.length; i++) {
        if (pending[i] > pending[maxIdx]) maxIdx = i;
      }
      const lsn = pending.splice(maxIdx, 1)[0];
      const record = this.wal.record(lsn);

      if (record.type === 'UPDATE') {
        const page = this.pool.fetchPage(record.pageId);
        const clr = this.wal.append({
          type: 'CLR', lsn: 0, txnId: record.txnId,
          prevLSN: txnLastLSN.get(record.txnId)!,
          pageId: record.pageId, offset: record.offset,
          after: record.before, undoNextLSN: record.prevLSN,
        });
        page.write(record.offset, record.before);
        page.pageLSN = clr.lsn;
        this.pool.markDirty(record.pageId, clr.lsn);
        txnLastLSN.set(record.txnId, clr.lsn);
        clrCount++;
        if (record.prevLSN !== 0) {
          pending.push(record.prevLSN);
        } else {
          const end = this.wal.append({ type: 'END', lsn: 0, txnId: record.txnId, prevLSN: clr.lsn });
          txnLastLSN.set(record.txnId, end.lsn);
        }
      } else if (record.type === 'CLR') {
        // 已部分回滚：沿 undoNextLSN 跳过已补偿的记录
        if (record.undoNextLSN !== 0) {
          pending.push(record.undoNextLSN);
        } else {
          const end = this.wal.append({ type: 'END', lsn: 0, txnId: record.txnId, prevLSN: lsn });
          txnLastLSN.set(record.txnId, end.lsn);
        }
      } else {
        // ABORT 等非数据记录：跳到 prevLSN 继续
        if (record.prevLSN !== 0) {
          pending.push(record.prevLSN);
        } else {
          const end = this.wal.append({ type: 'END', lsn: 0, txnId: record.txnId, prevLSN: lsn });
          txnLastLSN.set(record.txnId, end.lsn);
        }
      }
    }
    return { loserTxns: losers.map((e) => e.txnId).sort((a, b) => a - b), clrCount };
  }

  /** 完整重启恢复：三阶段扫描后落盘。 */
  recover(): RecoveryStats {
    const analysis = this.analyze();
    const redoCount = this.redo(analysis);
    const { loserTxns, clrCount } = this.undo(analysis);
    this.pool.flushAll();
    return { analysis, redoCount, loserTxns, clrCount };
  }
}
