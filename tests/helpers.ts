import assert from 'node:assert/strict';
import { RecoveryEngine } from '../src/recovery.ts';
import { boot, crash, type BootOptions, type System } from '../src/system.ts';
import type { LogRecord, PageId } from '../src/types.ts';

export { boot, crash, RecoveryEngine };
export type { System };

/** Simulates power loss and reboots onto the surviving durable state. */
export function reboot(sys: System): System {
  const durable: BootOptions = crash(sys);
  return boot(durable);
}

export async function recover(sys: System) {
  const engine = new RecoveryEngine(sys.wal, sys.disk, sys.bp, sys.injector);
  return engine.recover();
}

export function committedTxnIds(records: LogRecord[]): Set<number> {
  const committed = new Set<number>();
  for (const rec of records) {
    if (rec.type === 'COMMIT') committed.add(rec.txnId);
  }
  return committed;
}

/**
 * Independent model of crash semantics: replaying the durable log must apply
 * committed UPDATEs and all CLRs (compensations), nothing else.
 */
export function modelReplay(records: LogRecord[]): Map<PageId, Record<string, unknown>> {
  const committed = committedTxnIds(records);
  const pages = new Map<PageId, Record<string, unknown>>();
  for (const rec of records) {
    const applies = rec.type === 'CLR' || (rec.type === 'UPDATE' && committed.has(rec.txnId));
    if (!applies) continue;
    const page = pages.get(rec.pageId!) ?? {};
    if (rec.after === null || rec.after === undefined) delete page[rec.key!];
    else page[rec.key!] = rec.after;
    pages.set(rec.pageId!, page);
  }
  return pages;
}

export function diskData(sys: System): Map<PageId, Record<string, unknown>> {
  const out = new Map<PageId, Record<string, unknown>>();
  for (const pid of sys.disk.pageIds()) {
    out.set(pid, sys.disk.readPage(pid)!.data);
  }
  return out;
}

/** Every durable page's pageLSN must equal the newest log record touching it. */
export function assertPageLSNConsistent(sys: System): void {
  const records = sys.wal.readDurable();
  const maxLSN = new Map<PageId, number>();
  for (const rec of records) {
    if ((rec.type === 'UPDATE' || rec.type === 'CLR') && rec.pageId !== undefined) {
      maxLSN.set(rec.pageId, Math.max(rec.lsn, maxLSN.get(rec.pageId) ?? 0));
    }
  }
  assert.deepEqual(
    sys.disk.pageIds(),
    [...maxLSN.keys()].sort((a, b) => a - b),
    'disk must contain exactly the pages referenced by the durable WAL',
  );
  for (const [pid, lsn] of maxLSN) {
    const page = sys.disk.readPage(pid);
    assert.ok(page, `page ${pid} must exist on disk`);
    assert.equal(page.pageLSN, lsn, `page ${pid} pageLSN must equal newest log LSN`);
  }
}

/** Strict no-leak assertions: nothing volatile left dangling, all faults consumed. */
export function assertNoLeaks(
  sys: System,
  opts: { walFlushed?: boolean } = {},
  extra?: () => void,
): void {
  if (opts.walFlushed ?? true) {
    assert.equal(sys.wal.bufferedCount(), 0, 'WAL buffer must be fully flushed');
  }
  assert.deepEqual(sys.bp.dirtyPageIds(), [], 'buffer pool must have no dirty pages');
  assert.deepEqual(sys.tm.openTxnIds(), [], 'transaction manager must have no open txns');
  sys.injector.assertNoPendingFaults();
  extra?.();
}
