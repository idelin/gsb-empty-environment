/**
 * Write-ahead log manager.
 *
 * Records are appended to an in-memory chain and synchronously flushed to a
 * JSON-lines file, modelling the "log is forced to stable storage" rule.
 */

import { appendFileSync, existsSync, readFileSync, rmSync } from "node:fs";
import type { CheckpointRecord, LogRecord, LSN } from "./types.ts";
import { LogType } from "./types.ts";

export class WalManager {
  private records: LogRecord[] = [];
  private nextLSN: LSN = 1;
  private readonly filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
  }

  static create(filePath: string): WalManager {
    if (existsSync(filePath)) rmSync(filePath);
    return new WalManager(filePath);
  }

  /** Reopen an existing WAL file (e.g. after a crash). */
  static open(filePath: string): WalManager {
    const wal = new WalManager(filePath);
    if (existsSync(filePath)) {
      const text = readFileSync(filePath, "utf8");
      for (const line of text.split("\n")) {
        if (!line.trim()) continue;
        const record = JSON.parse(line) as LogRecord;
        wal.records.push(record);
        wal.nextLSN = Math.max(wal.nextLSN, record.lsn + 1);
      }
    }
    return wal;
  }

  append(record: Omit<LogRecord, "lsn">): LogRecord {
    const full: LogRecord = { ...record, lsn: this.nextLSN++ };
    this.records.push(full);
    appendFileSync(this.filePath, JSON.stringify(full) + "\n", "utf8");
    return full;
  }

  appendCheckpoint(
    att: CheckpointRecord["att"],
    dpt: CheckpointRecord["dpt"],
  ): CheckpointRecord {
    const full: CheckpointRecord = {
      lsn: this.nextLSN++,
      type: LogType.CHECKPOINT,
      txnId: -1,
      prevLSN: 0,
      att,
      dpt,
    };
    this.records.push(full);
    appendFileSync(this.filePath, JSON.stringify(full) + "\n", "utf8");
    return full;
  }

  getRecord(lsn: LSN): LogRecord | undefined {
    return this.records.find((r) => r.lsn === lsn);
  }

  /** All records with lsn >= fromLSN, in log order. */
  scanFrom(fromLSN: LSN): LogRecord[] {
    return this.records.filter((r) => r.lsn >= fromLSN);
  }

  all(): LogRecord[] {
    return [...this.records];
  }

  get maxLSN(): LSN {
    return this.nextLSN - 1;
  }
}
