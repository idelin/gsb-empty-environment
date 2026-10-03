import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { DBSystem } from '../src/system.ts';
import type { LogRecord } from '../src/types.ts';

export function tempDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'aries-test-'));
}

export function cleanup(dir: string): void {
  fs.rmSync(dir, { recursive: true, force: true });
}

export function walRecords(db: DBSystem): LogRecord[] {
  return [...db.wal.scanAll()];
}

export function recordsOf(db: DBSystem, txId: number): LogRecord[] {
  return walRecords(db).filter(
    (r) => 'txId' in r && (r as { txId: number }).txId === txId,
  );
}

/** 崩溃：丢弃实例（内存态丢失），返回重新打开的实例 */
export function crashAndReopen(
  db: DBSystem,
  opts: { bufferPoolCapacity?: number } = {},
): DBSystem {
  const dir = db.dir;
  db.crash();
  return DBSystem.open(dir, opts);
}
