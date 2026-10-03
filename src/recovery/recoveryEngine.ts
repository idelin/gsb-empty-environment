import type {
  ClrRecord,
  DirtyPageEntry,
  LogRecord,
  LSN,
  PageId,
  TxnId,
  TxnTableEntry,
  UpdateRecord,
} from '../log/records.ts';
import { LogManager } from '../log/logManager.ts';
import { BufferPool } from '../storage/bufferPool.ts';

export interface AnalysisResult {
  txnTable: Map<TxnId, TxnTableEntry>;
  dirtyPageTable: Map<PageId, DirtyPageEntry>;
  /** redo 扫描起点 = DPT 中最小 recLSN */
  redoLSN: LSN;
  /** 崩溃时未结束的事务（败者事务候选） */
  loserTxnIds: TxnId[];
}

export interface RecoveryStats {
  analysis: AnalysisResult;
  redoneCount: number;
  skippedRedoCount: number;
  undoneCount: number;
  clrCount: number;
}

/**
 * ARIES 崩溃恢复引擎：Analysis → Redo（重复历史）→ Undo（反向扫描 + CLR）。
 */
export class RecoveryEngine {
  private readonly log: LogManager;
  private readonly pool: BufferPool;
  private readonly walPath: string;

  constructor(walPath: string, log: LogManager, pool: BufferPool) {
    this.walPath = walPath;
    this.log = log;
    this.pool = pool;
  }

  /** ---------------- Analysis ---------------- */
  analysis(): AnalysisResult {
    const records = LogManager.readAll(this.walPath);

    // 定位最近一次 END_CHECKPOINT（模糊检查点快照）
    let startIdx = 0;
    let checkpoint: Extract<LogRecord, { type: 'END_CHECKPOINT' }> | null = null;
    for (let i = records.length - 1; i >= 0; i--) {
      if (records[i].type === 'END_CHECKPOINT') {
        checkpoint = records[i] as Extract<LogRecord, { type: 'END_CHECKPOINT' }>;
        startIdx = i;
        break;
      }
    }

    const txnTable = new Map<TxnId, TxnTableEntry>();
    const dirtyPageTable = new Map<PageId, DirtyPageEntry>();
    if (checkpoint) {
      for (const t of checkpoint.txnTable) txnTable.set(t.txnId, { ...t });
      for (const d of checkpoint.dirtyPageTable) dirtyPageTable.set(d.pageId, { ...d });
    }

    // 自检查点向前扫描，重放事务表与脏页表的状态迁移
    for (let i = startIdx; i < records.length; i++) {
      const rec = records[i];
      if (rec.txnId !== 0) {
        const entry = txnTable.get(rec.txnId);
        if (!entry) {
          txnTable.set(rec.txnId, { txnId: rec.txnId, status: 'RUNNING', lastLSN: rec.lsn });
        } else {
          entry.lastLSN = rec.lsn;
        }
        if (rec.type === 'COMMIT' || rec.type === 'ABORT') {
          txnTable.get(rec.txnId)!.status = rec.type === 'COMMIT' ? 'COMMITTING' : 'ABORTING';
        } else if (rec.type === 'END') {
          txnTable.delete(rec.txnId);
        }
      }
      if (rec.type === 'UPDATE' || rec.type === 'CLR') {
        if (!dirtyPageTable.has(rec.pageId)) {
          dirtyPageTable.set(rec.pageId, { pageId: rec.pageId, recLSN: rec.lsn });
        }
      }
    }

    let redoLSN: LSN = Number.MAX_SAFE_INTEGER;
    for (const d of dirtyPageTable.values()) redoLSN = Math.min(redoLSN, d.recLSN);
    if (redoLSN === Number.MAX_SAFE_INTEGER) redoLSN = records.length > 0 ? records[0].lsn : 1;

    const loserTxnIds = [...txnTable.values()]
      .filter((t) => t.status === 'RUNNING' || t.status === 'ABORTING')
      .map((t) => t.txnId);

    return { txnTable, dirtyPageTable, redoLSN, loserTxnIds };
  }

  /** ---------------- Redo（重复历史，含 CLR） ---------------- */
  redo(analysis: AnalysisResult): { redone: number; skipped: number } {
    const records = LogManager.readAll(this.walPath);
    let redone = 0;
    let skipped = 0;
    for (const rec of records) {
      if (rec.lsn < analysis.redoLSN) continue;
      if (rec.type !== 'UPDATE' && rec.type !== 'CLR') continue;
      const dptEntry = analysis.dirtyPageTable.get(rec.pageId);
      if (!dptEntry) {
        skipped++; // 页面在 recLSN 之前已干净，无需重做
        continue;
      }
      if (dptEntry.recLSN > rec.lsn) {
        skipped++; // 该记录早于页面变脏点，效果已落盘
        continue;
      }
      const pageLSN = this.pool.getPageLSN(rec.pageId);
      if (pageLSN >= rec.lsn) {
        skipped++; // 页面已包含该日志的效果（幂等保证）
        continue;
      }
      this.pool.redoApply(rec.pageId, rec.offset, rec.afterImage, rec.lsn);
      redone++;
    }
    return { redone, skipped };
  }

  /** ---------------- Undo（反向扫描，写 CLR 补偿） ---------------- */
  undo(analysis: AnalysisResult, hooks?: { afterClr?: (clrCount: number) => void }): { undone: number; clrs: number } {
    const records = LogManager.readAll(this.walPath);
    const byLSN = new Map<LSN, LogRecord>(records.map((r) => [r.lsn, r]));

    // 最大堆（按 LSN）：每次取各败者事务中 LSN 最大的待处理记录
    const heap: LSN[] = [];
    const push = (lsn: LSN) => {
      if (lsn <= 0) return;
      heap.push(lsn);
      let i = heap.length - 1;
      while (i > 0) {
        const p = (i - 1) >> 1;
        if (heap[p] >= heap[i]) break;
        [heap[p], heap[i]] = [heap[i], heap[p]];
        i = p;
      }
    };
    const popMax = (): LSN | undefined => {
      if (heap.length === 0) return undefined;
      const top = heap[0];
      const last = heap.pop()!;
      if (heap.length > 0) {
        heap[0] = last;
        let i = 0;
        for (;;) {
          const l = 2 * i + 1;
          const r = l + 1;
          let m = i;
          if (l < heap.length && heap[l] > heap[m]) m = l;
          if (r < heap.length && heap[r] > heap[m]) m = r;
          if (m === i) break;
          [heap[m], heap[i]] = [heap[i], heap[m]];
          i = m;
        }
      }
      return top;
    };

    const lastLSN = new Map<TxnId, LSN>();
    for (const [txnId, entry] of analysis.txnTable) {
      if (entry.status === 'RUNNING' || entry.status === 'ABORTING') {
        push(entry.lastLSN);
        lastLSN.set(txnId, entry.lastLSN);
      }
    }

    let undone = 0;
    let clrs = 0;

    for (;;) {
      const lsn = popMax();
      if (lsn === undefined) break;
      const rec = byLSN.get(lsn);
      if (!rec) throw new Error(`WAL corrupt: missing LSN ${lsn}`);

      if (rec.type === 'UPDATE') {
        const upd = rec as UpdateRecord;
        // 写 CLR：after-image = 原记录的 before-image，undoNext 跳过已补偿部分
        const clrLSN = this.log.append({
          type: 'CLR',
          prevLSN: lastLSN.get(rec.txnId) ?? 0,
          txnId: rec.txnId,
          pageId: upd.pageId,
          offset: upd.offset,
          afterImage: upd.beforeImage,
          undoNextLSN: upd.prevLSN,
        } satisfies Omit<ClrRecord, 'lsn'>);
        lastLSN.set(rec.txnId, clrLSN);
        // CLR 也需按 WAL 协议落盘后再改页
        this.log.flush(clrLSN);
        this.pool.applyUpdate(upd.pageId, upd.offset, upd.beforeImage, clrLSN);
        if (!analysis.dirtyPageTable.has(upd.pageId)) {
          analysis.dirtyPageTable.set(upd.pageId, { pageId: upd.pageId, recLSN: clrLSN });
        }
        undone++;
        clrs++;
        hooks?.afterClr?.(clrs);
        push(upd.prevLSN);
      } else if (rec.type === 'CLR') {
        // 已补偿过：沿 undoNextLSN 继续，保证崩溃中重启恢复幂等
        push((rec as ClrRecord).undoNextLSN);
      } else if (rec.type === 'BEGIN') {
        // 事务完全回滚：写 END，从败者集合移除
        const endLSN = this.log.append({
          type: 'END',
          prevLSN: lastLSN.get(rec.txnId) ?? 0,
          txnId: rec.txnId,
        });
        this.log.flush(endLSN);
        lastLSN.delete(rec.txnId);
      } else if (rec.type === 'ABORT') {
        push(rec.prevLSN);
      } else {
        // COMMIT / END / CHECKPOINT 不出现在败者链上；防御性跳过
        push(rec.prevLSN);
      }
    }

    return { undone, clrs };
  }

  /** 完整恢复流程；endCheckpoint 为 true 时恢复结束后做检查点并刷脏页 */
  recover(endCheckpoint = true, hooks?: { afterClr?: (clrCount: number) => void }): RecoveryStats {
    const analysis = this.analysis();
    const { redone, skipped } = this.redo(analysis);
    const { undone, clrs } = this.undo(analysis, hooks);
    if (endCheckpoint) {
      this.pool.flushAll();
      this.log.flushAll();
    }
    return {
      analysis,
      redoneCount: redone,
      skippedRedoCount: skipped,
      undoneCount: undone,
      clrCount: clrs,
    };
  }
}
