import { openSync, writeSync, fsyncSync, closeSync, readFileSync, existsSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';

export type WalRecord = Record<string, any>;

/** 预写式日志：每条状态转移先落盘(fsync)再生效，崩溃后按序重放。 */
export class Wal {
  readonly path: string;
  private fd: number;

  constructor(path: string) {
    this.path = path;
    mkdirSync(dirname(path), { recursive: true });
    this.fd = openSync(path, 'a');
  }

  append(record: WalRecord): void {
    writeSync(this.fd, JSON.stringify(record) + '\n');
    fsyncSync(this.fd);
  }

  replay(): WalRecord[] {
    if (!existsSync(this.path)) return [];
    return readFileSync(this.path, 'utf8')
      .split('\n')
      .filter((line) => line.trim().length > 0)
      .map((line) => JSON.parse(line) as WalRecord);
  }

  close(): void {
    closeSync(this.fd);
  }
}
