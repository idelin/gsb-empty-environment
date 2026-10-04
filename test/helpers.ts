import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { serializeRecord, type LSN, type LogRecord } from "../src/log-record.ts";
import { writeFileSync } from "node:fs";
import { RecoveryEngine } from "../src/recovery.ts";
import { BufferPool, DiskStorage } from "../src/storage.ts";
import { WriteAheadLog } from "../src/wal.ts";

export function makeTempDir(prefix = "aries-test-"): string {
  return mkdtempSync(join(tmpdir(), prefix));
}

/** Write a WAL file from records without LSNs; LSNs assigned 1..n in order. */
export function writeWalFile(
  path: string,
  records: Array<Omit<LogRecord, "lsn"> & { lsn?: LSN }>,
): LogRecord[] {
  let next = 1;
  const full = records.map((r) => {
    const lsn = r.lsn ?? next;
    next = Math.max(next, lsn + 1);
    return { ...r, lsn } as LogRecord;
  });
  writeFileSync(path, full.map(serializeRecord).join("\n") + "\n", "utf8");
  return full;
}

export interface RecoveryHarness {
  dir: string;
  walPath: string;
  dbPath: string;
  disk: DiskStorage;
  wal: WriteAheadLog;
  pool: BufferPool;
  engine: RecoveryEngine;
}

/** Attach a fresh recovery engine to an existing wal/db pair. */
export function attach(dir: string): RecoveryHarness {
  const walPath = join(dir, "wal.log");
  const dbPath = join(dir, "db.json");
  const disk = new DiskStorage(dbPath);
  const wal = new WriteAheadLog(walPath);
  const pool = new BufferPool(disk);
  const engine = new RecoveryEngine(wal, pool);
  return { dir, walPath, dbPath, disk, wal, pool, engine };
}

export function pageValue(disk: DiskStorage, pageId: string): unknown {
  return disk.readPage(pageId)?.data;
}

export function pageLSN(disk: DiskStorage, pageId: string): number | undefined {
  return disk.readPage(pageId)?.pageLSN;
}
