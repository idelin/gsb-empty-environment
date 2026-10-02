import { EngineError, ErrorCodes } from "../errors.ts";
import type { MetricsRegistry } from "../metrics/registry.ts";
import type { BufferPool } from "../storage/bufferPool.ts";
import type { DiskManager } from "../storage/disk.ts";
import { LogType, type LogRecord, type TxnStatus } from "../wal/record.ts";
import { scanWal, WalWriter } from "../wal/wal.ts";

export interface TTEntry {
  txnId: number;
  status: TxnStatus;
  lastLSN: number;
}

export interface RecoveryReport {
  dirtyPageTable: Map<number, number>;
  transactionTable: Map<number, TTEntry>;
  losers: number[];
  redoStartLSN: number;
  redoApplied: number;
  redoSkipped: number;
  undoCLRs: number;
  undoneTxns: number[];
}

/**
 * ARIES recovery: Analysis (rebuild DPT/TT from the last checkpoint),
 * Redo (repeat history from the minimum recLSN), Undo (rollback losers
 * with compensation log records, following undoNextLSN chains).
 */
export class RecoveryEngine {
  private readonly metrics: MetricsRegistry;
  private readonly walPath: string;
  private readonly disk: DiskManager;
  private readonly bufferPool: BufferPool;
  private readonly dpt = new Map<number, number>();
  private readonly tt = new Map<number, TTEntry>();
  private analysisDPT = new Map<number, number>();
  private analysisTT = new Map<number, TTEntry>();
  private records: LogRecord[] = [];
  private byLSN = new Map<number, LogRecord>();

  constructor(walPath: string, disk: DiskManager, bufferPool: BufferPool, metrics: MetricsRegistry) {
    this.walPath = walPath;
    this.disk = disk;
    this.bufferPool = bufferPool;
    this.metrics = metrics;
  }

  recover(): RecoveryReport {
    const scan = scanWal(this.walPath, this.metrics);
    this.records = scan.records;
    for (const r of this.records) this.byLSN.set(r.lsn, r);

    this.analysis();
    const redoStartLSN = this.redo();
    const undoneTxns = this.undo();

    // Recovery complete: all pages clean on disk, no volatile state retained.
    this.bufferPool.flushAll();
    this.bufferPool.assertQuiescent();
    this.metrics.gauge("recovery.dirty_pages").set(0);
    this.metrics.gauge("recovery.active_txns").set(0);

    const report: RecoveryReport = {
      dirtyPageTable: this.analysisDPT,
      transactionTable: this.analysisTT,
      losers: undoneTxns,
      redoStartLSN,
      redoApplied: this.metrics.counter("recovery.redo.applied").get(),
      redoSkipped: this.metrics.counter("recovery.redo.skipped").get(),
      undoCLRs: this.metrics.counter("recovery.undo.clr_written").get(),
      undoneTxns,
    };
    // Release internal state: no references to log records are retained.
    this.records = [];
    this.byLSN.clear();
    this.dpt.clear();
    this.tt.clear();
    return report;
  }

  /** Phase 1: locate the last complete checkpoint, rebuild DPT/TT to log end. */
  private analysis(): void {
    const timer = this.metrics.timer("recovery.analysis.duration_ms");
    timer.time(() => {
      let startIndex = 0;
      for (let i = this.records.length - 1; i >= 0; i--) {
        const r = this.records[i]!;
        if (r.type === LogType.END_CHECKPOINT) {
          startIndex = i;
          for (const [pageId, recLSN] of Object.entries(r.checkpointDPT ?? {})) {
            this.dpt.set(Number(pageId), recLSN);
          }
          for (const entry of r.checkpointTT ?? []) {
            this.tt.set(entry.txnId, { txnId: entry.txnId, status: entry.status, lastLSN: entry.lastLSN });
          }
          break;
        }
      }
      for (let i = startIndex; i < this.records.length; i++) {
        const r = this.records[i]!;
        this.metrics.counter("recovery.analysis.records_scanned").inc();
        switch (r.type) {
          case LogType.UPDATE:
          case LogType.CLR: {
            const entry = this.tt.get(r.txnId);
            if (entry === undefined) {
              this.tt.set(r.txnId, { txnId: r.txnId, status: "RUNNING", lastLSN: r.lsn });
            } else {
              entry.lastLSN = r.lsn;
            }
            if (r.pageId !== undefined && !this.dpt.has(r.pageId)) {
              this.dpt.set(r.pageId, r.lsn);
            }
            break;
          }
          case LogType.COMMIT: {
            const entry = this.tt.get(r.txnId);
            if (entry === undefined) {
              this.tt.set(r.txnId, { txnId: r.txnId, status: "COMMITTED", lastLSN: r.lsn });
            } else {
              entry.status = "COMMITTED";
              entry.lastLSN = r.lsn;
            }
            break;
          }
          case LogType.ABORT: {
            const entry = this.tt.get(r.txnId);
            if (entry !== undefined) {
              entry.status = "ABORTING";
              entry.lastLSN = r.lsn;
            }
            break;
          }
          case LogType.END: {
            this.tt.delete(r.txnId);
            break;
          }
          default:
            break; // checkpoint records carry no further analysis work
        }
      }
      this.metrics.gauge("recovery.dirty_pages").set(this.dpt.size);
      this.metrics.gauge("recovery.active_txns").set(this.tt.size);
      // Snapshot the analysis-phase tables for the recovery report.
      this.analysisDPT = new Map(this.dpt);
      this.analysisTT = new Map([...this.tt].map(([k, v]) => [k, { ...v }]));
    });
  }

  /** Phase 2: repeat history from the smallest recLSN in the DPT. */
  private redo(): number {
    const timer = this.metrics.timer("recovery.redo.duration_ms");
    let redoStart = 0;
    timer.time(() => {
      if (this.dpt.size === 0) return;
      redoStart = Math.min(...this.dpt.values());
      for (const r of this.records) {
        if (r.lsn < redoStart) continue;
        if (r.type !== LogType.UPDATE && r.type !== LogType.CLR) continue;
        this.metrics.counter("recovery.redo.records_scanned").inc();
        const pageId = r.pageId;
        if (pageId === undefined) continue;
        const recLSN = this.dpt.get(pageId);
        if (recLSN === undefined) continue; // page was flushed before this record
        if (r.lsn < recLSN) {
          this.metrics.counter("recovery.redo.skipped").inc();
          continue;
        }
        const frame = this.bufferPool.getPage(pageId);
        if (frame.pageLSN >= r.lsn) {
          this.metrics.counter("recovery.redo.skipped").inc();
          this.bufferPool.unpin(pageId);
          continue;
        }
        frame.data = r.afterImage ?? "";
        frame.pageLSN = r.lsn;
        if (!frame.dirty) frame.recLSN = r.lsn;
        frame.dirty = true;
        this.bufferPool.unpin(pageId);
        this.metrics.counter("recovery.redo.applied").inc();
      }
    });
    return redoStart;
  }

  /** Phase 3: roll back losers in reverse LSN order, writing CLRs. */
  private undo(): number[] {
    const timer = this.metrics.timer("recovery.undo.duration_ms");
    const undone: number[] = [];
    timer.time(() => {
      const losers = [...this.tt.values()].filter((t) => t.status !== "COMMITTED");
      if (losers.length === 0) return;

      // Append CLRs/ENDs to the same WAL file (recovery continues the log).
      const appendWal = WalWriter.append(
        this.walPath,
        this.metrics,
        (this.records[this.records.length - 1]?.lsn ?? 0) + 1,
      );

      // Max-LSN priority queue over the losers' to-undo chains.
      const pending: number[] = losers.map((t) => t.lastLSN);
      try {
        while (pending.length > 0) {
          let maxIdx = 0;
          for (let i = 1; i < pending.length; i++) {
            if (pending[i]! > pending[maxIdx]!) maxIdx = i;
          }
          const lsn = pending.splice(maxIdx, 1)[0]!;
          const record = this.byLSN.get(lsn);
          if (record === undefined) {
            throw new EngineError(ErrorCodes.RECOVERY_INVALID_LSN, `undo: LSN ${lsn} not found in log`);
          }
          this.metrics.counter("recovery.undo.records_scanned").inc();

          if (record.type === LogType.CLR) {
            const next = record.undoNextLSN ?? 0;
            if (next === 0) {
              this.writeEnd(appendWal, record.txnId, record.lsn);
              undone.push(record.txnId);
            } else {
              pending.push(next);
            }
            continue;
          }
          if (record.type === LogType.UPDATE) {
            const clr = appendWal.append({
              prevLSN: lsn,
              txnId: record.txnId,
              type: LogType.CLR,
              pageId: record.pageId,
              undoNextLSN: record.prevLSN,
              beforeImage: record.afterImage,
              afterImage: record.beforeImage,
            });
            if (record.pageId !== undefined) {
              const frame = this.bufferPool.getPage(record.pageId);
              frame.data = record.beforeImage ?? "";
              frame.pageLSN = clr.lsn;
              if (!frame.dirty) frame.recLSN = clr.lsn;
              frame.dirty = true;
              this.bufferPool.unpin(record.pageId);
            }
            this.metrics.counter("recovery.undo.clr_written").inc();
            if (record.prevLSN === 0) {
              this.writeEnd(appendWal, record.txnId, clr.lsn);
              undone.push(record.txnId);
            } else {
              pending.push(record.prevLSN);
            }
            continue;
          }
          // COMMIT/ABORT/other: keep walking the chain.
          if (record.prevLSN !== 0) pending.push(record.prevLSN);
        }
      } finally {
        appendWal.close();
      }
      for (const txnId of undone) this.tt.delete(txnId);
      this.metrics.counter("recovery.undo.txns_aborted").inc(undone.length);
    });
    return undone.sort((a, b) => a - b);
  }

  private writeEnd(wal: WalWriter, txnId: number, prevLSN: number): void {
    wal.append({ prevLSN, txnId, type: LogType.END });
  }
}
