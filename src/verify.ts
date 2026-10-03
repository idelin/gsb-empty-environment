import type { LogRecord, PageId, TxnId } from './types.ts';
import type { Disk } from './storage.ts';

export interface VerificationResult {
  ok: boolean;
  errors: string[];
  checkedKeys: number;
  winners: TxnId[];
  losers: TxnId[];
}

// Strict post-recovery verification. Ground truth is derived purely from the
// WAL: replaying committed transactions' UPDATEs in LSN order must reproduce
// the disk state exactly; every loser update must be compensated by a CLR and
// closed with END; per-transaction prevLSN chains must be intact.
export function verifyRecoveredState(disk: Disk, wal: LogRecord[]): VerificationResult {
  const errors: string[] = [];
  const committed = new Set<TxnId>();
  const ended = new Set<TxnId>();
  const updatesPerTxn = new Map<TxnId, number>();
  const clrsPerTxn = new Map<TxnId, number>();
  const lastLSNPerTxn = new Map<TxnId, number | null>();

  for (const record of wal) {
    switch (record.type) {
      case 'UPDATE':
        updatesPerTxn.set(record.txnId, (updatesPerTxn.get(record.txnId) ?? 0) + 1);
        break;
      case 'CLR':
        clrsPerTxn.set(record.txnId, (clrsPerTxn.get(record.txnId) ?? 0) + 1);
        break;
      case 'COMMIT':
        committed.add(record.txnId);
        break;
      case 'END':
        ended.add(record.txnId);
        break;
      default:
        break;
    }
    if (
      record.type === 'UPDATE' ||
      record.type === 'CLR' ||
      record.type === 'COMMIT' ||
      record.type === 'ABORT' ||
      record.type === 'END'
    ) {
      const expectedPrev = lastLSNPerTxn.get(record.txnId) ?? null;
      if (record.prevLSN !== expectedPrev) {
        errors.push(
          `txn ${record.txnId} ${record.type}@${record.lsn}: prevLSN ${record.prevLSN} != expected ${expectedPrev}`,
        );
      }
      lastLSNPerTxn.set(record.txnId, record.lsn);
    }
  }

  // Expected disk state: last committed writer wins, initial value 0.
  const touched = new Map<PageId, Set<string>>();
  const lastCommitted = new Map<PageId, Map<string, number>>();
  for (const record of wal) {
    if (record.type !== 'UPDATE') continue;
    let keys = touched.get(record.pageId);
    if (!keys) {
      keys = new Set();
      touched.set(record.pageId, keys);
    }
    keys.add(record.key);
    if (committed.has(record.txnId)) {
      let pageMap = lastCommitted.get(record.pageId);
      if (!pageMap) {
        pageMap = new Map();
        lastCommitted.set(record.pageId, pageMap);
      }
      pageMap.set(record.key, record.after);
    }
  }

  let checkedKeys = 0;
  for (const [pageId, keys] of touched) {
    const page = disk.readPage(pageId);
    for (const key of keys) {
      const expected = lastCommitted.get(pageId)?.get(key) ?? 0;
      const actual = page.data[key] ?? 0;
      checkedKeys++;
      if (actual !== expected) {
        errors.push(`page ${pageId} key ${key}: disk=${actual} expected=${expected}`);
      }
    }
  }
  for (const pageId of disk.pageIds()) {
    if (!touched.has(pageId)) errors.push(`page ${pageId} on disk but never touched in WAL`);
  }

  const winners: TxnId[] = [];
  const losers: TxnId[] = [];
  for (const txnId of updatesPerTxn.keys()) {
    if (committed.has(txnId)) {
      winners.push(txnId);
    } else {
      losers.push(txnId);
      const updates = updatesPerTxn.get(txnId) ?? 0;
      const clrs = clrsPerTxn.get(txnId) ?? 0;
      if (clrs !== updates) errors.push(`loser txn ${txnId}: ${clrs} CLRs for ${updates} UPDATEs`);
      if (!ended.has(txnId)) errors.push(`loser txn ${txnId}: missing END record`);
    }
  }
  winners.sort((a, b) => a - b);
  losers.sort((a, b) => a - b);
  return { ok: errors.length === 0, errors, checkedKeys, winners, losers };
}
