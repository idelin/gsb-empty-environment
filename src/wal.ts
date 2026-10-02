import {
  closeSync,
  existsSync,
  fsyncSync,
  mkdirSync,
  openSync,
  readFileSync,
  writeSync,
} from 'node:fs';
import { dirname } from 'node:path';

export type WalRecord =
  | { op: 'begin'; xid: string; ts: number }
  | { op: 'prepared'; xid: string; ts: number; writes: Record<string, unknown> }
  | { op: 'committed'; xid: string; ts: number }
  | { op: 'rolledback'; xid: string; ts: number };

/**
 * Append-only write-ahead log. Every record is written and fsync'ed before
 * the caller is allowed to proceed, so a `prepared` record is guaranteed to
 * survive a process crash or power loss once prepare() has returned.
 */
export class Wal {
  private readonly fd: number;
  readonly file: string;

  constructor(file: string) {
    this.file = file;
    mkdirSync(dirname(file), { recursive: true });
    this.fd = openSync(file, 'a');
  }

  append(record: WalRecord): void {
    writeSync(this.fd, JSON.stringify(record) + '\n');
    fsyncSync(this.fd);
  }

  close(): void {
    closeSync(this.fd);
  }

  static readAll(file: string): WalRecord[] {
    if (!existsSync(file)) return [];
    return readFileSync(file, 'utf8')
      .split('\n')
      .filter((line) => line.trim().length > 0)
      .map((line) => JSON.parse(line) as WalRecord);
  }
}
