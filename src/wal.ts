import { appendFileSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import {
  NULL_LSN,
  deserializeRecord,
  serializeRecord,
  type LSN,
  type LogRecord,
} from "./log-record.ts";
import { Mutex } from "./mutex.ts";

/**
 * Write-ahead log. Records are appended to an in-memory tail and become
 * durable only after flush() — this models the WAL protocol where a COMMIT
 * forces the log up to the commit LSN, and a crash loses the unflushed tail.
 */
export class WriteAheadLog {
  private records: LogRecord[] = [];
  private flushedUpTo: LSN = NULL_LSN;
  private nextLSN: LSN = NULL_LSN + 1;
  private readonly appendMutex = new Mutex();
  private readonly filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
    if (existsSync(filePath)) {
      for (const record of readWalFile(filePath)) {
        this.records.push(record);
        this.nextLSN = Math.max(this.nextLSN, record.lsn + 1);
      }
      this.flushedUpTo = this.nextLSN - 1;
    }
  }

  /** Append a record, assigning its LSN. Serialized for concurrent callers. */
  async append(record: Omit<LogRecord, "lsn"> & { lsn?: LSN }): Promise<LogRecord> {
    return this.appendMutex.runExclusive(() => {
      const full = { ...record, lsn: this.nextLSN++ } as LogRecord;
      this.records.push(full);
      return full;
    });
  }

  /** Force all records up to (and including) `uptoLSN` to stable storage. */
  flush(uptoLSN?: LSN): void {
    const target = uptoLSN ?? this.nextLSN - 1;
    const pending = this.records.filter(
      (r) => r.lsn > this.flushedUpTo && r.lsn <= target,
    );
    if (pending.length === 0) return;
    appendFileSync(
      this.filePath,
      pending.map(serializeRecord).join("\n") + "\n",
      "utf8",
    );
    this.flushedUpTo = Math.max(this.flushedUpTo, target);
  }

  /** Simulate a crash: everything past the flushed prefix is lost. */
  durableRecords(): LogRecord[] {
    return this.records.filter((r) => r.lsn <= this.flushedUpTo);
  }

  allRecords(): LogRecord[] {
    return [...this.records];
  }

  get lastLSN(): LSN {
    return this.nextLSN - 1;
  }

  get flushedLSN(): LSN {
    return this.flushedUpTo;
  }

  get appendLock(): Mutex {
    return this.appendMutex;
  }
}

export function readWalFile(filePath: string): LogRecord[] {
  if (!existsSync(filePath)) return [];
  const content = readFileSync(filePath, "utf8").trim();
  if (content.length === 0) return [];
  return content.split("\n").map(deserializeRecord);
}

/** Reset a WAL file (used by tests / demo setup). */
export function resetWalFile(filePath: string): void {
  writeFileSync(filePath, "", "utf8");
}
