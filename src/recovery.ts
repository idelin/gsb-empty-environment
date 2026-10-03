import { BufferPool } from './storage.ts';
import { WriteAheadLog } from './wal.ts';
import { NULL_LSN } from './types.ts';
import type {
  CLRRecord,
  DirtyPageEntry,
  EndCheckpointRecord,
  LogRecord,
  LSN,
  PageId,
  TxId,
  TxTableEntry,
} from './types.ts';

export interface AnalysisResult {
  txTable: Map<TxId, TxTableEntry>;
  dirtyPageTable: Map<PageId, LSN>;
  /** Redo 扫描起点 = 脏页表中最小 recLSN */
  redoLSN: LSN;
  checkpointLSN: LSN | null;
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redoneLSNs: LSN[];
  undoCLRs: CLRRecord[];
  losers: TxId[];
  winners: TxId[];
}

/** 简单最大堆，按 LSN 降序取出（Undo 阶段的全局逆序） */
class MaxHeap {
  private items: LSN[] = [];
  get size(): number {
    return this.items.length;
  }
  push(v: LSN): void {
    this.items.push(v);
    let i = this.items.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (this.items[p] >= this.items[i]) break;
      [this.items[p], this.items[i]] = [this.items[i], this.items[p]];
      i = p;
    }
  }
  pop(): LSN {
    const top = this.items[0];
    const last = this.items.pop()!;
    if (this.items.length > 0) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        if (l < this.items.length && this.items[l] > this.items[m]) m = l;
        if (r < this.items.length && this.items[r] > this.items[m]) m = r;
        if (m === i) break;
        [this.items[m], this.items[i]] = [this.items[i], this.items[m]];
        i = m;
      }
    }
    return top;
  }
}

/**
 * ARIES 崩溃恢复引擎：Analysis → Redo（历史重复）→ Undo（反向补偿）。
 * 幂等设计：
 *  - Redo 以 pageLSN 判定是否重放，重复执行无副作用；
 *  - Undo 通过 CLR 的 undoNextLSN 链接，崩溃后重启恢复不会重复补偿。
 */
export class RecoveryManager {
  private readonly wal: WriteAheadLog;
  private readonly bufferPool: BufferPool;
  private readonly afterAppend?: (rec: LogRecord) => void;

  constructor(
    wal: WriteAheadLog,
    bufferPool: BufferPool,
    hooks: { afterAppend?: (rec: LogRecord) => void } = {},
  ) {
    this.wal = wal;
    this.bufferPool = bufferPool;
    this.afterAppend = hooks.afterAppend;
  }

  private append<T extends Omit<LogRecord, 'lsn'>>(rec: T): T & { lsn: LSN } {
    const full = this.wal.append(rec);
    this.afterAppend?.(full);
    return full;
  }

  recover(): RecoveryReport {
    const analysis = this.analysis();
    const redoneLSNs = this.redo(analysis);
    const { undoCLRs, losers, winners } = this.undo(analysis.txTable);
    return { analysis, redoneLSNs, undoCLRs, losers, winners };
  }

  /**
   * 阶段一 Analysis：从最近的模糊检查点出发重建事务表与脏页表，
   * 识别崩溃时的 Winner（已提交）与 Loser（未提交/中止中）事务。
   */
  analysis(): AnalysisResult {
    const txTable = new Map<TxId, TxTableEntry>();
    const dirtyPageTable = new Map<PageId, LSN>();

    // 定位最近的 END_CHECKPOINT，以其快照初始化，从配对的 BEGIN_CHECKPOINT 起扫
    let checkpoint: EndCheckpointRecord | null = null;
    for (const rec of this.wal.scanAll()) {
      if (rec.type === 'END_CHECKPOINT') checkpoint = rec;
    }
    let startLSN: LSN = 1;
    if (checkpoint) {
      for (const e of checkpoint.transactionTable) {
        txTable.set(e.txId, { ...e });
      }
      for (const e of checkpoint.dirtyPageTable) {
        dirtyPageTable.set(e.pageId, e.recLSN);
      }
      startLSN = checkpoint.beginLSN;
    }

    for (const rec of this.wal.scanFrom(startLSN)) {
      switch (rec.type) {
        case 'UPDATE':
        case 'CLR': {
          let entry = txTable.get(rec.txId);
          if (!entry) {
            entry = { txId: rec.txId, status: 'RUNNING', lastLSN: rec.lsn };
            txTable.set(rec.txId, entry);
          }
          entry.lastLSN = rec.lsn;
          if (!dirtyPageTable.has(rec.pageId)) {
            dirtyPageTable.set(rec.pageId, rec.lsn);
          }
          break;
        }
        case 'COMMIT': {
          const entry = txTable.get(rec.txId);
          if (entry) {
            entry.status = 'COMMITTING';
            entry.lastLSN = rec.lsn;
          }
          break;
        }
        case 'ABORT': {
          const entry = txTable.get(rec.txId);
          if (entry) {
            entry.status = 'ABORTING';
            entry.lastLSN = rec.lsn;
          }
          break;
        }
        case 'END':
          txTable.delete(rec.txId);
          break;
        default:
          break; // 检查点记录不影响表
      }
    }

    let redoLSN = this.wal.lastLSN + 1;
    for (const recLSN of dirtyPageTable.values()) {
      if (recLSN < redoLSN) redoLSN = recLSN;
    }
    return {
      txTable,
      dirtyPageTable,
      redoLSN,
      checkpointLSN: checkpoint ? checkpoint.lsn : null,
    };
  }

  /**
   * 阶段二 Redo：历史重复。从 redoLSN 前向扫描，重放所有 UPDATE/CLR 的后镜像。
   * 跳过条件：页不在脏页表、记录早于 recLSN、或页上 pageLSN 已不落后于记录（幂等）。
   */
  redo(analysis: AnalysisResult): LSN[] {
    const redone: LSN[] = [];
    for (const rec of this.wal.scanFrom(analysis.redoLSN)) {
      if (rec.type !== 'UPDATE' && rec.type !== 'CLR') continue;
      const recLSN = analysis.dirtyPageTable.get(rec.pageId);
      if (recLSN === undefined || rec.lsn < recLSN) continue;
      const page = this.bufferPool.fetch(rec.pageId);
      if (page.pageLSN >= rec.lsn) continue; // 已持久化或已重放：幂等跳过
      this.bufferPool.set(rec.pageId, rec.after, rec.lsn);
      redone.push(rec.lsn);
    }
    return redone;
  }

  /**
   * 阶段三 Undo：反向补偿。以最大堆全局逆序处理所有 Loser 的日志链，
   * UPDATE 写 CLR 撤销，CLR 沿 undoNextLSN 跳过；链空则写 END 收尾。
   * Winner（已提交未写 END）直接补写 END。
   */
  undo(txTable: Map<TxId, TxTableEntry>): {
    undoCLRs: CLRRecord[];
    losers: TxId[];
    winners: TxId[];
  } {
    const losers: TxId[] = [];
    const winners: TxId[] = [];
    const heap = new MaxHeap();

    for (const [txId, entry] of txTable) {
      if (entry.status === 'COMMITTING') {
        winners.push(txId);
        this.append({ type: 'END', txId, prevLSN: entry.lastLSN });
        txTable.delete(txId);
      } else {
        losers.push(txId);
        if (entry.lastLSN !== NULL_LSN) heap.push(entry.lastLSN);
      }
    }

    const undoCLRs: CLRRecord[] = [];
    while (heap.size > 0) {
      const lsn = heap.pop();
      const rec = this.wal.getRecord(lsn);
      if (rec.type === 'BEGIN_CHECKPOINT' || rec.type === 'END_CHECKPOINT') {
        throw new Error('checkpoint record in undo chain');
      }
      const txId = rec.txId;
      const entry = txTable.get(txId)!;
      let next: LSN;
      if (rec.type === 'UPDATE') {
        const clr = this.append({
          type: 'CLR',
          txId,
          prevLSN: entry.lastLSN,
          pageId: rec.pageId,
          before: rec.after,
          after: rec.before,
          undoNextLSN: rec.prevLSN,
        });
        entry.lastLSN = clr.lsn;
        this.bufferPool.set(rec.pageId, rec.before, clr.lsn);
        undoCLRs.push(clr);
        next = rec.prevLSN;
      } else if (rec.type === 'CLR') {
        next = rec.undoNextLSN;
      } else if (rec.type === 'ABORT' || rec.type === 'COMMIT') {
        next = rec.prevLSN;
      } else {
        throw new Error(`unexpected record in undo chain: ${rec.type}`);
      }
      if (next === NULL_LSN) {
        this.append({ type: 'END', txId, prevLSN: entry.lastLSN });
        txTable.delete(txId);
      } else {
        heap.push(next);
      }
    }
    return { undoCLRs, losers, winners };
  }
}
