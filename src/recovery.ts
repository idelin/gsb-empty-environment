import type { LogRecord, TxStatus, WAL } from './wal.ts';
import type { PageStore } from './storage.ts';

export interface TxnEntry {
  txId: number;
  status: TxStatus;
  lastLSN: number;
}

export interface AnalysisResult {
  txTable: Map<number, TxnEntry>;
  dpt: Map<number, number>;
  checkpointLSN: number;
}

export function analyze(log: LogRecord[]): AnalysisResult {
  const txTable = new Map<number, TxnEntry>();
  const dpt = new Map<number, number>();
  let startIndex = 0;
  let checkpointLSN = 0;

  for (let i = log.length - 1; i >= 0; i--) {
    const record = log[i];
    if (record.kind === 'end_checkpoint') {
      for (const [txId, status, lastLSN] of record.tt ?? []) {
        txTable.set(txId, { txId, status, lastLSN });
      }
      for (const [pageId, recLSN] of record.dpt ?? []) {
        dpt.set(pageId, recLSN);
      }
      startIndex = i + 1;
      checkpointLSN = record.beginLSN ?? record.lsn;
      break;
    }
  }

  for (let i = startIndex; i < log.length; i++) {
    const record = log[i];
    switch (record.kind) {
      case 'begin':
        txTable.set(record.txId, { txId: record.txId, status: 'running', lastLSN: record.lsn });
        break;
      case 'update':
      case 'clr': {
        const entry = txTable.get(record.txId);
        if (entry) entry.lastLSN = record.lsn;
        else txTable.set(record.txId, { txId: record.txId, status: 'running', lastLSN: record.lsn });
        if (!dpt.has(record.pageId!)) dpt.set(record.pageId!, record.lsn);
        break;
      }
      case 'commit': {
        const entry = txTable.get(record.txId);
        if (entry) {
          entry.status = 'committed';
          entry.lastLSN = record.lsn;
        }
        break;
      }
      case 'end':
        txTable.delete(record.txId);
        break;
      default:
        break;
    }
  }
  return { txTable, dpt, checkpointLSN };
}

export function redo(log: LogRecord[], dpt: Map<number, number>, store: PageStore): number[] {
  const redone: number[] = [];
  let startLSN = Infinity;
  for (const recLSN of dpt.values()) startLSN = Math.min(startLSN, recLSN);
  if (!Number.isFinite(startLSN)) return redone;

  for (const record of log) {
    if (record.lsn < startLSN) continue;
    if (record.kind !== 'update' && record.kind !== 'clr') continue;
    const recLSN = dpt.get(record.pageId!);
    if (recLSN === undefined || recLSN > record.lsn) continue;
    if (store.pageLSN(record.pageId!) >= record.lsn) continue;
    store.setSlot(record.pageId!, record.slot!, record.after!, record.lsn);
    redone.push(record.lsn);
  }
  return redone;
}

class MaxHeap {
  private items: Array<{ lsn: number; txId: number }> = [];

  get size(): number {
    return this.items.length;
  }

  push(lsn: number, txId: number): void {
    this.items.push({ lsn, txId });
    let i = this.items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.items[parent].lsn >= this.items[i].lsn) break;
      [this.items[parent], this.items[i]] = [this.items[i], this.items[parent]];
      i = parent;
    }
  }

  pop(): { lsn: number; txId: number } {
    const top = this.items[0];
    const last = this.items.pop()!;
    if (this.items.length > 0) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let largest = i;
        if (left < this.items.length && this.items[left].lsn > this.items[largest].lsn) largest = left;
        if (right < this.items.length && this.items[right].lsn > this.items[largest].lsn) largest = right;
        if (largest === i) break;
        [this.items[largest], this.items[i]] = [this.items[i], this.items[largest]];
        i = largest;
      }
    }
    return top;
  }
}

export interface UndoResult {
  clrLSNs: number[];
  ended: number[];
}

export function undo(
  log: LogRecord[],
  txTable: Map<number, TxnEntry>,
  wal: WAL,
  store: PageStore,
): UndoResult {
  const byLSN = new Map<number, LogRecord>();
  for (const record of log) byLSN.set(record.lsn, record);

  const heap = new MaxHeap();
  for (const entry of txTable.values()) {
    if (entry.status === 'running') heap.push(entry.lastLSN, entry.txId);
  }

  const clrLSNs: number[] = [];
  const ended: number[] = [];

  const endTxn = (txId: number, prevLSN: number): void => {
    wal.append({ kind: 'end', txId, prevLSN });
    ended.push(txId);
  };

  while (heap.size > 0) {
    const { lsn, txId } = heap.pop();
    const record = byLSN.get(lsn);
    if (!record) throw new Error(`undo chain references missing LSN ${lsn}`);

    if (record.kind === 'clr') {
      const next = record.undoNextLSN ?? 0;
      if (next === 0) endTxn(txId, record.lsn);
      else heap.push(next, txId);
    } else if (record.kind === 'update') {
      const clr = wal.append({
        kind: 'clr',
        txId,
        prevLSN: record.lsn,
        pageId: record.pageId,
        slot: record.slot,
        before: record.after,
        after: record.before,
        undoNextLSN: record.prevLSN,
      });
      byLSN.set(clr.lsn, clr);
      store.setSlot(record.pageId!, record.slot!, record.before!, clr.lsn);
      clrLSNs.push(clr.lsn);
      if (record.prevLSN === 0) endTxn(txId, clr.lsn);
      else heap.push(record.prevLSN, txId);
    } else if (record.kind === 'begin') {
      endTxn(txId, record.lsn);
    } else {
      throw new Error(`unexpected record kind '${record.kind}' in undo chain`);
    }
  }
  return { clrLSNs, ended };
}
