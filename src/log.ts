import { FileLayer } from "./filelayer.ts";
import type { DPTEntry, LogRecord, LSN, NewLogRecord, TxnTableEntry } from "./types.ts";

export type CheckpointEnd = Extract<LogRecord, { type: "chkpt_end" }>;

export class LogManager {
  readonly fl: FileLayer;
  readonly records: LogRecord[] = [];
  private byLSN = new Map<LSN, LogRecord>();
  nextLSN: LSN = 1;
  readonly anomalies: string[];

  constructor(fl: FileLayer) {
    this.fl = fl;
    const { records, anomalies } = fl.readAll();
    this.anomalies = anomalies;
    for (const r of records) this.index(r);
  }

  private index(r: LogRecord): void {
    this.records.push(r);
    this.byLSN.set(r.lsn, r);
    if (r.lsn >= this.nextLSN) this.nextLSN = r.lsn + 1;
  }

  get(lsn: LSN): LogRecord | undefined {
    return this.byLSN.get(lsn);
  }

  append(rec: NewLogRecord): LogRecord {
    const full = { ...rec, lsn: this.nextLSN } as LogRecord;
    this.fl.append(full);
    this.index(full);
    return full;
  }

  checkpoint(txns: TxnTableEntry[], dirtyPages: DPTEntry[]): LSN {
    const begin = this.append({ type: "chkpt_begin" });
    const end = this.append({
      type: "chkpt_end",
      beginLSN: begin.lsn,
      txns: txns.map((t) => ({ ...t })),
      dirtyPages: dirtyPages.map((d) => ({ ...d })),
    });
    return end.lsn;
  }

  /** Last checkpoint whose begin AND end records both survived intact. */
  lastCompleteCheckpoint(): CheckpointEnd | null {
    for (let i = this.records.length - 1; i >= 0; i--) {
      const r = this.records[i];
      if (r.type === "chkpt_end") {
        const begin = this.byLSN.get(r.beginLSN);
        if (begin && begin.type === "chkpt_begin") return r;
      }
    }
    return null;
  }
}
