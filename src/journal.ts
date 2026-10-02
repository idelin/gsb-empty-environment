import { appendFileSync, existsSync, mkdirSync, readFileSync } from 'node:fs';
import { dirname } from 'node:path';
import type { JournalEvent, StoredJournalEvent } from './types.ts';

/**
 * 追加式 JSONL 持久化日志。
 * 每次状态迁移先落盘（同步写）再推进内存状态，保证断电后可完整重放；
 * 读取时容忍最后一行撕裂写（断电场景），直接丢弃不完整记录。
 */
export class Journal {
  private readonly path: string;
  private readonly events: StoredJournalEvent[] = [];

  constructor(path: string) {
    this.path = path;
    if (existsSync(path)) {
      const lines = readFileSync(path, 'utf8').split('\n');
      for (const line of lines) {
        if (!line.trim()) continue;
        try {
          this.events.push(JSON.parse(line) as StoredJournalEvent);
        } catch {
          // 撕裂的尾行：忽略
        }
      }
    }
  }

  append(event: JournalEvent): StoredJournalEvent {
    const stored: StoredJournalEvent = {
      ...event,
      seq: this.events.length,
      ts: Date.now(),
    };
    mkdirSync(dirname(this.path), { recursive: true });
    appendFileSync(this.path, JSON.stringify(stored) + '\n');
    this.events.push(stored);
    return stored;
  }

  all(): StoredJournalEvent[] {
    return [...this.events];
  }

  forTx(txId: string): StoredJournalEvent[] {
    return this.events.filter((e) => e.txId === txId);
  }
}
