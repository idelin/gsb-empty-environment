import { WalWriter, readWal } from './wal.ts';
import type { BufferedPage, DiskPages } from './storage.ts';
import type { LogRecord, TxnTableEntry } from './types.ts';

export interface AnalysisResult {
  checkpointLSN: number | null;
  txnTable: Map<number, TxnTableEntry>;
  dirtyPageTable: Map<string, number>;
}

export interface RedoResult {
  startLSN: number | null;
  applied: number[];
  skipped: number[];
}

export interface UndoResult {
  losers: number[];
  clrLSNs: number[];
  endedTxIds: number[];
  flushedPages: number;
}

export interface RecoveryResult {
  analysis: AnalysisResult;
  redo: RedoResult;
  undo: UndoResult;
}

interface HeapItem {
  lsn: number;
  txId: number;
}

class MaxHeap {
  private items: HeapItem[] = [];

  get size(): number {
    return this.items.length;
  }

  push(lsn: number, txId: number): void {
    this.items.push({ lsn, txId });
    let index = this.items.length - 1;
    while (index > 0) {
      const parent = (index - 1) >> 1;
      if (this.items[parent].lsn >= this.items[index].lsn) break;
      [this.items[parent], this.items[index]] = [this.items[index], this.items[parent]];
      index = parent;
    }
  }

  pop(): HeapItem {
    const top = this.items[0];
    const last = this.items.pop() as HeapItem;
    if (this.items.length > 0) {
      this.items[0] = last;
      let index = 0;
      for (;;) {
        const left = index * 2 + 1;
        const right = left + 1;
        let largest = index;
        if (left < this.items.length && this.items[left].lsn > this.items[largest].lsn) largest = left;
        if (right < this.items.length && this.items[right].lsn > this.items[largest].lsn) largest = right;
        if (largest === index) break;
        [this.items[largest], this.items[index]] = [this.items[index], this.items[largest]];
        index = largest;
      }
    }
    return top;
  }
}

export class RecoveryManager {
  private walClosed = false;
  private readonly walPath: string;
  private readonly disk: DiskPages;

  constructor(walPath: string, disk: DiskPages) {
    this.walPath = walPath;
    this.disk = disk;
  }

  recover(): RecoveryResult {
    const log = readWal(this.walPath);
    const analysis = this.analyze(log);
    const buffer = new Map<string, BufferedPage>();
    const fetchPage = (pageId: string): BufferedPage => {
      const cached = buffer.get(pageId);
      if (cached) return cached;
      const onDisk = this.disk.get(pageId);
      const page: BufferedPage = onDisk
        ? { data: onDisk.data, pageLSN: onDisk.pageLSN, dirty: false }
        : { data: 0, pageLSN: 0, dirty: false };
      buffer.set(pageId, page);
      return page;
    };
    const redo = this.redo(log, analysis, fetchPage);
    const wal = WalWriter.openAppend(this.walPath, log);
    let flushedPages = 0;
    try {
      const undo = this.undo(analysis, wal, fetchPage);
      for (const [pageId, page] of buffer) {
        if (!page.dirty) continue;
        this.disk.set(pageId, { data: page.data, pageLSN: page.pageLSN });
        page.dirty = false;
        flushedPages++;
      }
      return { analysis, redo, undo: { ...undo, flushedPages } };
    } finally {
      wal.close();
      this.walClosed = true;
    }
  }

  get closed(): boolean {
    return this.walClosed;
  }

  private analyze(log: LogRecord[]): AnalysisResult {
    const txnTable = new Map<number, TxnTableEntry>();
    const dirtyPageTable = new Map<string, number>();
    let checkpointLSN: number | null = null;
    let startIndex = 0;
    for (let i = log.length - 1; i >= 0; i--) {
      const record = log[i];
      if (record.type === 'CHECKPOINT') {
        checkpointLSN = record.lsn;
        for (const entry of record.txTable) txnTable.set(entry.txId, { ...entry });
        for (const entry of record.dirtyPages) dirtyPageTable.set(entry.pageId, entry.recLSN);
        startIndex = i + 1;
        break;
      }
    }
    const touchTxn = (txId: number, status: TxnTableEntry['status'], lsn: number): void => {
      const entry = txnTable.get(txId) ?? { txId, status: 'RUNNING' as const, lastLSN: lsn };
      entry.status = status;
      entry.lastLSN = lsn;
      txnTable.set(txId, entry);
    };
    for (let i = startIndex; i < log.length; i++) {
      const record = log[i];
      switch (record.type) {
        case 'BEGIN':
          txnTable.set(record.txId, { txId: record.txId, status: 'RUNNING', lastLSN: record.lsn });
          break;
        case 'UPDATE':
        case 'CLR': {
          const entry = txnTable.get(record.txId) ?? {
            txId: record.txId,
            status: 'RUNNING' as const,
            lastLSN: record.lsn,
          };
          entry.lastLSN = record.lsn;
          txnTable.set(record.txId, entry);
          if (!dirtyPageTable.has(record.pageId)) dirtyPageTable.set(record.pageId, record.lsn);
          break;
        }
        case 'COMMIT':
          touchTxn(record.txId, 'COMMITTED', record.lsn);
          break;
        case 'ABORT':
          touchTxn(record.txId, 'ABORTING', record.lsn);
          break;
        case 'END':
          txnTable.delete(record.txId);
          break;
        case 'CHECKPOINT':
          break;
      }
    }
    return { checkpointLSN, txnTable, dirtyPageTable };
  }

  private redo(
    log: LogRecord[],
    analysis: AnalysisResult,
    fetchPage: (pageId: string) => BufferedPage,
  ): RedoResult {
    const applied: number[] = [];
    const skipped: number[] = [];
    const dpt = analysis.dirtyPageTable;
    if (dpt.size === 0) return { startLSN: null, applied, skipped };
    const startLSN = Math.min(...dpt.values());
    for (const record of log) {
      if (record.lsn < startLSN) continue;
      if (record.type !== 'UPDATE' && record.type !== 'CLR') continue;
      const recLSN = dpt.get(record.pageId);
      if (recLSN === undefined || record.lsn < recLSN) {
        skipped.push(record.lsn);
        continue;
      }
      const page = fetchPage(record.pageId);
      if (page.pageLSN >= record.lsn) {
        skipped.push(record.lsn);
        continue;
      }
      page.data = record.after;
      page.pageLSN = record.lsn;
      page.dirty = true;
      applied.push(record.lsn);
    }
    return { startLSN, applied, skipped };
  }

  private undo(
    analysis: AnalysisResult,
    wal: WalWriter,
    fetchPage: (pageId: string) => BufferedPage,
  ): Omit<UndoResult, 'flushedPages'> {
    const losers: number[] = [];
    const endedTxIds: number[] = [];
    const clrLSNs: number[] = [];
    const lastWritten = new Map<number, number>();
    const heap = new MaxHeap();
    for (const entry of analysis.txnTable.values()) {
      if (entry.status === 'COMMITTED') {
        wal.append({ type: 'END', txId: entry.txId, prevLSN: entry.lastLSN });
        endedTxIds.push(entry.txId);
      } else {
        losers.push(entry.txId);
        lastWritten.set(entry.txId, entry.lastLSN);
        heap.push(entry.lastLSN, entry.txId);
      }
    }
    while (heap.size > 0) {
      const { lsn, txId } = heap.pop();
      const record = wal.recordAt(lsn);
      if (record.type === 'UPDATE') {
        const page = fetchPage(record.pageId);
        const clr = wal.append({
          type: 'CLR',
          txId,
          prevLSN: lastWritten.get(txId) ?? lsn,
          pageId: record.pageId,
          after: record.before,
          undoNextLSN: record.prevLSN,
        });
        lastWritten.set(txId, clr.lsn);
        clrLSNs.push(clr.lsn);
        page.data = record.before;
        page.pageLSN = clr.lsn;
        page.dirty = true;
        if (record.prevLSN > 0) heap.push(record.prevLSN, txId);
      } else if (record.type === 'CLR') {
        if (record.undoNextLSN > 0) heap.push(record.undoNextLSN, txId);
      } else if (record.type === 'BEGIN') {
        wal.append({ type: 'END', txId, prevLSN: lastWritten.get(txId) ?? lsn });
        endedTxIds.push(txId);
      } else if (record.prevLSN > 0) {
        heap.push(record.prevLSN, txId);
      }
    }
    return { losers, clrLSNs, endedTxIds };
  }
}
