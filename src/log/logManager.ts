import { appendFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import type { LogRecord, LSN } from './records.ts';

/**
 * WAL 日志管理器。
 * 协议：事务在修改页面前必须先写日志（WAL），
 * COMMIT 记录及其之前的所有日志必须刷盘后才算提交成功（force-at-commit）。
 */
export class LogManager {
  private readonly filePath: string;
  private nextLSN: LSN = 1;
  /** 已分配的最大 LSN（含未刷盘） */
  private tail: LogRecord[] = [];
  /** 已持久化到的最大 LSN */
  private flushedLSN: LSN = 0;

  constructor(filePath: string) {
    this.filePath = filePath;
    if (existsSync(filePath)) {
      // 从已有 WAL 恢复 LSN 计数器（用于追加场景）
      const lines = readFileSync(filePath, 'utf8').split('\n').filter((l) => l.length > 0);
      for (const line of lines) {
        const rec = JSON.parse(line) as LogRecord;
        this.nextLSN = Math.max(this.nextLSN, rec.lsn + 1);
        this.flushedLSN = Math.max(this.flushedLSN, rec.lsn);
      }
    } else {
      writeFileSync(filePath, '');
    }
  }

  getFlushedLSN(): LSN {
    return this.flushedLSN;
  }

  /** 追加一条日志（先进内存尾部缓冲），返回分配的 LSN */
  append(record: Omit<LogRecord, 'lsn'>): LSN {
    const lsn = this.nextLSN++;
    const full = { ...record, lsn } as LogRecord;
    this.tail.push(full);
    return lsn;
  }

  /** 将 <= targetLSN 的所有日志强制写入 WAL 文件 */
  flush(targetLSN: LSN): void {
    const pending = this.tail.filter((r) => r.lsn <= targetLSN);
    if (pending.length === 0) return;
    const payload = pending.map((r) => JSON.stringify(r)).join('\n') + '\n';
    appendFileSync(this.filePath, payload);
    this.tail = this.tail.filter((r) => r.lsn > targetLSN);
    this.flushedLSN = Math.max(this.flushedLSN, targetLSN);
  }

  flushAll(): void {
    if (this.tail.length === 0) return;
    this.flush(this.tail[this.tail.length - 1].lsn);
  }

  /** 顺序读取 WAL 文件中的全部记录（崩溃恢复入口） */
  static readAll(filePath: string): LogRecord[] {
    if (!existsSync(filePath)) return [];
    return readFileSync(filePath, 'utf8')
      .split('\n')
      .filter((l) => l.length > 0)
      .map((l) => JSON.parse(l) as LogRecord);
  }
}
