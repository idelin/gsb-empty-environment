/**
 * 预写日志（WAL）：内存日志缓冲区 + JSON Lines 文件持久化。
 * 崩溃恢复时从文件重新加载。
 */
import * as fs from "node:fs";
import type { LSN, LogRecord } from "./logRecord.ts";
import {
  serializeRecord,
  deserializeRecord,
} from "./logRecord.ts";

export class WriteAheadLog {
  private records: LogRecord[] = [];
  private nextLSN: LSN = 1;
  private filePath: string | null;

  constructor(filePath?: string) {
    this.filePath = filePath ?? null;
    if (this.filePath && fs.existsSync(this.filePath)) {
      fs.unlinkSync(this.filePath);
    }
  }

  /** 追加一条日志并立即 flush 到磁盘（WAL 协议要求先于数据页落盘） */
  append(record: Omit<LogRecord, "lsn">): LogRecord {
    const full: LogRecord = { ...record, lsn: this.nextLSN++ };
    this.records.push(full);
    if (this.filePath) {
      fs.appendFileSync(this.filePath, serializeRecord(full) + "\n", "utf8");
    }
    return full;
  }

  /** 崩溃后从 WAL 文件重建日志序列 */
  static recoverFromFile(filePath: string): WriteAheadLog {
    const wal = new WriteAheadLog();
    wal.filePath = filePath;
    const content = fs.readFileSync(filePath, "utf8");
    let maxLSN = 0;
    for (const line of content.split("\n")) {
      if (line.trim().length === 0) continue;
      const rec = deserializeRecord(line);
      wal.records.push(rec);
      if (rec.lsn > maxLSN) maxLSN = rec.lsn;
    }
    wal.nextLSN = maxLSN + 1;
    return wal;
  }

  get size(): number {
    return this.records.length;
  }

  get lastLSN(): LSN {
    return this.nextLSN - 1;
  }

  get(index: number): LogRecord {
    return this.records[index];
  }

  /** 全量顺序扫描 */
  scan(): LogRecord[] {
    return [...this.records];
  }

  /** 从指定 LSN 起（含）顺序扫描 */
  scanFrom(lsn: LSN): LogRecord[] {
    return this.records.filter((r) => r.lsn >= lsn);
  }

  /** 找到最后一个 CHECKPOINT 记录；无则返回 null */
  lastCheckpoint(): LogRecord | null {
    for (let i = this.records.length - 1; i >= 0; i--) {
      if (this.records[i].type === "CHECKPOINT") return this.records[i];
    }
    return null;
  }
}
