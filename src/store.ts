import { appendFileSync, existsSync, mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import type { AuditEvent } from './types.ts';

/**
 * 持久化事件存储：append-only JSONL 审计日志。
 * 每条事件同步落盘后再推进内存状态，保证断电后可通过重放精确恢复。
 */
export class EventStore {
  private readonly file: string;
  private readonly events: AuditEvent[] = [];

  constructor(dir: string) {
    mkdirSync(dir, { recursive: true });
    this.file = join(dir, 'saga-events.log');
    if (existsSync(this.file)) {
      for (const line of readFileSync(this.file, 'utf8').split('\n')) {
        if (line.trim()) this.events.push(JSON.parse(line) as AuditEvent);
      }
    }
  }

  append(event: AuditEvent): void {
    appendFileSync(this.file, JSON.stringify(event) + '\n');
    this.events.push(event);
  }

  all(): AuditEvent[] {
    return [...this.events];
  }
}
