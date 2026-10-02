import {
  closeSync,
  existsSync,
  fsyncSync,
  openSync,
  readFileSync,
  writeSync,
} from "node:fs";
import type { CheckpointRecord, LogRecord, LSN } from "./types.ts";
import { NULL_LSN } from "./types.ts";

export class LogManager {
  private readonly fd: number;
  private nextLSN: LSN = 1;
  private flushedLSN: LSN = NULL_LSN;
  private readonly records = new Map<LSN, LogRecord>();
  private readonly file: string;

  constructor(file: string) {
    this.file = file;
    this.fd = openSync(file, "a+");
    for (const rec of LogManager.parseFile(file)) {
      this.records.set(rec.lsn, rec);
      this.nextLSN = rec.lsn + 1;
    }
    this.flushedLSN = this.nextLSN - 1;
  }

  private static parseFile(file: string): LogRecord[] {
    if (!existsSync(file)) return [];
    const out: LogRecord[] = [];
    for (const line of readFileSync(file, "utf8").split("\n")) {
      if (line.trim().length === 0) continue;
      out.push(JSON.parse(line) as LogRecord);
    }
    return out;
  }

  append(record: Omit<LogRecord, "lsn">): LSN {
    const lsn = this.nextLSN++;
    const full = { ...record, lsn } as LogRecord;
    writeSync(this.fd, JSON.stringify(full) + "\n");
    this.records.set(lsn, full);
    return lsn;
  }

  flush(upToLSN?: LSN): void {
    const target = upToLSN ?? this.nextLSN - 1;
    if (target <= this.flushedLSN) return;
    fsyncSync(this.fd);
    this.flushedLSN = this.nextLSN - 1;
  }

  getRecord(lsn: LSN): LogRecord {
    const rec = this.records.get(lsn);
    if (!rec) throw new Error(`no log record at LSN ${lsn}`);
    return rec;
  }

  scan(fromLSN: LSN = 1): LogRecord[] {
    const out: LogRecord[] = [];
    for (let lsn = Math.max(1, fromLSN); lsn < this.nextLSN; lsn++) {
      const rec = this.records.get(lsn);
      if (rec) out.push(rec);
    }
    return out;
  }

  get lastLSN(): LSN {
    return this.nextLSN - 1;
  }

  lastCheckpoint(): CheckpointRecord | null {
    for (let lsn = this.nextLSN - 1; lsn >= 1; lsn--) {
      const rec = this.records.get(lsn);
      if (rec?.type === "checkpoint") return rec;
    }
    return null;
  }

  close(): void {
    closeSync(this.fd);
  }
}
