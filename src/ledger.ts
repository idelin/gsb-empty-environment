// 追加式 JSONL 账本：先写盘后应用（WAL）。重启 = 从文件重放。
import fs from 'node:fs';
import path from 'node:path';

export class Ledger<T> {
  readonly file: string;
  private records: T[] = [];

  constructor(dir: string, name: string) {
    this.file = path.join(dir, `${name}.jsonl`);
    fs.mkdirSync(dir, { recursive: true });
    if (fs.existsSync(this.file)) {
      for (const line of fs.readFileSync(this.file, 'utf8').split('\n')) {
        if (line.trim().length > 0) this.records.push(JSON.parse(line) as T);
      }
    }
  }

  append(rec: T): void {
    fs.appendFileSync(this.file, JSON.stringify(rec) + '\n');
    this.records.push(rec);
  }

  all(): readonly T[] {
    return this.records;
  }
}
