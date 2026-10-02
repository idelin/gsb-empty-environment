/**
 * Formal constraint inspector & invariant assertion engine.
 *
 * WAL-level invariants (inspectWal):
 *   LSN_UNIQUE / LSN_CONTIGUITY   - LSNs are unique, start at 1, gap-free
 *   TXN_SINGLE_BEGIN              - a transaction begins at most once
 *   BEGIN_HAS_NO_PREV             - BEGIN starts a fresh transaction chain
 *   ORPHAN_RECORD                 - no record without a preceding BEGIN
 *   RECORD_AFTER_END              - no record after its transaction's END
 *   TXN_DOUBLE_TERMINAL           - at most one of COMMIT/ABORT per txn
 *   END_WITHOUT_TERMINAL          - END requires a prior COMMIT or ABORT
 *   PREV_LSN_DANGLING / _ORDER / _CROSS_TXN - prevLSN reference integrity
 *   CLR_UNDO_NEXT_DANGLING / _ORDER / _CROSS_TXN - undoNextLSN integrity
 *   ORPHAN_LOOP                   - no cycles in transaction chains
 *   CHECKPOINT_REFS_VALID         - checkpoint tables reference existing,
 *                                   strictly earlier LSNs
 *
 * Post-recovery invariants (inspectRecovered):
 *   TXN_TABLE_EMPTY               - no leaked transactions after undo
 *   LOSER_UPDATE_COMPENSATED      - every loser update has a matching CLR
 *   COMMITTED_TXN_NO_CLR          - committed txns are never compensated
 *   LOSER_TXN_ENDED               - every loser transaction was ENDed
 *   PAGE_LSN_CONSISTENT           - pageLSN == max applied LSN per page
 *   NO_DATA_SKEW                  - disk == committed-only expected state
 */

import type { LogRecord, LSN, PageId, TxnId, Value } from './log-record.ts';
import type { TxnTable } from './recovery.ts';
import type { Storage } from './storage.ts';

export interface Violation {
  invariant: string;
  detail: string;
  lsn?: LSN;
}

export interface InspectionReport {
  ok: boolean;
  checkedRecords: number;
  violations: Violation[];
}

export type SkewKind = 'missing' | 'diverged' | 'unexpected';

export interface SkewEntry {
  pageId: PageId;
  key: string;
  expected: Value | undefined;
  actual: Value | undefined;
  kind: SkewKind;
}

interface TxnLifecycle {
  began: boolean;
  terminal: 'COMMIT' | 'ABORT' | null;
  ended: boolean;
  sawClr: boolean;
}

function keyId(pageId: PageId, key: string): string {
  return pageId + '|' + key;
}

export class Inspector {
  /** Offline integrity inspection of a WAL (pure, side-effect free). */
  inspectWal(records: LogRecord[]): InspectionReport {
    const violations: Violation[] = [];
    const byLsn = new Map<LSN, LogRecord>();

    for (const r of records) {
      if (byLsn.has(r.lsn)) {
        violations.push({ invariant: 'LSN_UNIQUE', detail: `duplicate LSN ${r.lsn}`, lsn: r.lsn });
      }
      byLsn.set(r.lsn, r);
    }
    records.forEach((r, i) => {
      if (r.lsn !== i + 1) {
        violations.push({ invariant: 'LSN_CONTIGUITY', detail: `expected LSN ${i + 1}, found ${r.lsn}`, lsn: r.lsn });
      }
    });

    const lifecycles = new Map<TxnId, TxnLifecycle>();
    for (const r of records) {
      if (r.kind === 'CHECKPOINT') {
        for (const e of r.txnTable) {
          if (!byLsn.has(e.lastLSN) || e.lastLSN >= r.lsn) {
            violations.push({
              invariant: 'CHECKPOINT_REFS_VALID',
              detail: `checkpoint txnTable entry ${e.txnId} references invalid lastLSN ${e.lastLSN}`,
              lsn: r.lsn,
            });
          }
        }
        for (const e of r.dirtyPageTable) {
          if (!byLsn.has(e.recLSN) || e.recLSN >= r.lsn) {
            violations.push({
              invariant: 'CHECKPOINT_REFS_VALID',
              detail: `checkpoint dirtyPageTable entry ${e.pageId} references invalid recLSN ${e.recLSN}`,
              lsn: r.lsn,
            });
          }
        }
        continue;
      }

      const state = lifecycles.get(r.txnId) ?? { began: false, terminal: null, ended: false, sawClr: false };
      lifecycles.set(r.txnId, state);

      if (r.kind === 'BEGIN') {
        if (state.began) {
          violations.push({ invariant: 'TXN_SINGLE_BEGIN', detail: `txn ${r.txnId} begins twice`, lsn: r.lsn });
        }
        state.began = true;
        if (r.prevLSN !== null) {
          violations.push({ invariant: 'BEGIN_HAS_NO_PREV', detail: `BEGIN of ${r.txnId} has prevLSN ${r.prevLSN}`, lsn: r.lsn });
        }
        continue;
      }

      if (!state.began) {
        violations.push({ invariant: 'ORPHAN_RECORD', detail: `${r.kind} of txn ${r.txnId} has no preceding BEGIN`, lsn: r.lsn });
      }
      if (state.ended) {
        violations.push({ invariant: 'RECORD_AFTER_END', detail: `${r.kind} of txn ${r.txnId} appears after END`, lsn: r.lsn });
      }
      if (r.kind === 'COMMIT' || r.kind === 'ABORT') {
        if (state.terminal !== null) {
          violations.push({ invariant: 'TXN_DOUBLE_TERMINAL', detail: `txn ${r.txnId} terminates twice`, lsn: r.lsn });
        }
        state.terminal = r.kind;
      }
      if (r.kind === 'CLR') {
        state.sawClr = true;
      }
      if (r.kind === 'END') {
        // END is legitimate after COMMIT/ABORT, or after undo wrote CLRs
        // (crash-recovery or abort-time compensation of a running txn).
        if (state.terminal === null && !state.sawClr) {
          violations.push({ invariant: 'END_WITHOUT_TERMINAL', detail: `END of txn ${r.txnId} without COMMIT/ABORT or compensation`, lsn: r.lsn });
        }
        state.ended = true;
      }

      if (r.prevLSN !== null) {
        const prev = byLsn.get(r.prevLSN);
        if (!prev) {
          violations.push({ invariant: 'PREV_LSN_DANGLING', detail: `prevLSN ${r.prevLSN} of txn ${r.txnId} does not exist`, lsn: r.lsn });
        } else {
          if (prev.lsn >= r.lsn) {
            violations.push({ invariant: 'PREV_LSN_ORDER', detail: `prevLSN ${prev.lsn} is not earlier than ${r.lsn}`, lsn: r.lsn });
          }
          if (prev.txnId !== r.txnId) {
            violations.push({ invariant: 'PREV_LSN_CROSS_TXN', detail: `prevLSN ${prev.lsn} belongs to txn ${String(prev.txnId)}, not ${r.txnId}`, lsn: r.lsn });
          }
        }
      }

      if (r.kind === 'CLR' && r.undoNextLSN !== null) {
        const target = byLsn.get(r.undoNextLSN);
        if (!target) {
          violations.push({ invariant: 'CLR_UNDO_NEXT_DANGLING', detail: `undoNextLSN ${r.undoNextLSN} of txn ${r.txnId} does not exist`, lsn: r.lsn });
        } else {
          if (target.lsn >= r.lsn) {
            violations.push({ invariant: 'CLR_UNDO_NEXT_ORDER', detail: `undoNextLSN ${target.lsn} is not earlier than ${r.lsn}`, lsn: r.lsn });
          }
          if (target.txnId !== r.txnId) {
            violations.push({ invariant: 'CLR_UNDO_NEXT_CROSS_TXN', detail: `undoNextLSN ${target.lsn} belongs to txn ${String(target.txnId)}, not ${r.txnId}`, lsn: r.lsn });
          }
        }
      }
    }

    // Orphan loops: cycles inside transaction chains.
    const globallyVisited = new Set<LSN>();
    for (const r of records) {
      if (r.txnId === null || globallyVisited.has(r.lsn)) continue;
      const path = new Set<LSN>([r.lsn]);
      let cur: LogRecord = r;
      while (cur.prevLSN !== null) {
        const next = byLsn.get(cur.prevLSN);
        if (!next) break;
        if (path.has(next.lsn)) {
          violations.push({
            invariant: 'ORPHAN_LOOP',
            detail: `cycle in transaction chain of ${r.txnId} at LSN ${next.lsn}`,
            lsn: next.lsn,
          });
          break;
        }
        path.add(next.lsn);
        cur = next;
      }
      for (const lsn of path) globallyVisited.add(lsn);
    }

    return { ok: violations.length === 0, checkedRecords: records.length, violations };
  }

  /**
   * Post-recovery assertion: the log plus the on-disk state must prove that
   * committed data survived and every loser was fully compensated, with no
   * leaked transactions or skewed pages.
   */
  inspectRecovered(records: LogRecord[], txnTable: TxnTable, storage: Storage): InspectionReport {
    const violations: Violation[] = [];

    if (txnTable.size > 0) {
      violations.push({
        invariant: 'TXN_TABLE_EMPTY',
        detail: `${txnTable.size} transaction(s) leaked: ${[...txnTable.keys()].join(', ')}`,
      });
    }

    const committed = new Set<TxnId>();
    const ended = new Set<TxnId>();
    for (const r of records) {
      if (r.kind === 'COMMIT') committed.add(r.txnId);
      if (r.kind === 'END') ended.add(r.txnId);
    }
    const clrs = records.filter((r) => r.kind === 'CLR');

    for (const r of records) {
      if (r.kind !== 'UPDATE' || committed.has(r.txnId)) continue;
      const compensated = clrs.some(
        (c) =>
          c.kind === 'CLR' &&
          c.txnId === r.txnId &&
          c.pageId === r.pageId &&
          c.key === r.key &&
          c.lsn > r.lsn &&
          c.after === r.before &&
          c.undoNextLSN === r.prevLSN,
      );
      if (!compensated) {
        violations.push({
          invariant: 'LOSER_UPDATE_COMPENSATED',
          detail: `update of txn ${r.txnId} on ${r.pageId}.${r.key} lacks a compensating CLR`,
          lsn: r.lsn,
        });
      }
    }

    for (const c of clrs) {
      if (c.kind === 'CLR' && committed.has(c.txnId)) {
        violations.push({ invariant: 'COMMITTED_TXN_NO_CLR', detail: `committed txn ${c.txnId} has a CLR`, lsn: c.lsn });
      }
    }

    const losers = new Set<TxnId>();
    for (const r of records) {
      if (r.kind === 'UPDATE' && !committed.has(r.txnId)) losers.add(r.txnId);
    }
    for (const txnId of losers) {
      if (!ended.has(txnId)) {
        violations.push({ invariant: 'LOSER_TXN_ENDED', detail: `loser txn ${txnId} has no END record` });
      }
    }

    const maxLsnByPage = new Map<PageId, LSN>();
    for (const r of records) {
      if (r.kind === 'UPDATE' || r.kind === 'CLR') {
        maxLsnByPage.set(r.pageId, Math.max(maxLsnByPage.get(r.pageId) ?? 0, r.lsn));
      }
    }
    for (const [pageId, lsn] of maxLsnByPage) {
      const page = storage.getPage(pageId);
      if (page.pageLSN !== lsn) {
        violations.push({
          invariant: 'PAGE_LSN_CONSISTENT',
          detail: `page ${pageId} pageLSN ${page.pageLSN} != max applied LSN ${lsn}`,
        });
      }
    }

    for (const s of this.detectDataSkew(records, storage)) {
      violations.push({
        invariant: 'NO_DATA_SKEW',
        detail: `${s.kind} @ ${s.pageId}.${s.key}: expected=${JSON.stringify(s.expected)} actual=${JSON.stringify(s.actual)}`,
      });
    }

    return { ok: violations.length === 0, checkedRecords: records.length, violations };
  }

  /**
   * Data-skew detection: fold the log into the committed-only expected state
   * (committed UPDATEs + all CLRs) and diff it against disk. Uncommitted
   * residue on disk is reported as 'unexpected'.
   */
  detectDataSkew(records: LogRecord[], storage: Storage): SkewEntry[] {
    const committed = new Set<TxnId>();
    for (const r of records) {
      if (r.kind === 'COMMIT') committed.add(r.txnId);
    }

    const expected = new Map<string, { pageId: PageId; key: string; value: Value }>();
    const loserTouched = new Map<string, { pageId: PageId; key: string }>();
    for (const r of records) {
      if (r.kind === 'UPDATE') {
        if (committed.has(r.txnId)) {
          expected.set(keyId(r.pageId, r.key), { pageId: r.pageId, key: r.key, value: r.after });
        } else {
          loserTouched.set(keyId(r.pageId, r.key), { pageId: r.pageId, key: r.key });
        }
      } else if (r.kind === 'CLR') {
        expected.set(keyId(r.pageId, r.key), { pageId: r.pageId, key: r.key, value: r.after });
      }
    }

    const skew: SkewEntry[] = [];
    for (const { pageId, key, value } of expected.values()) {
      const actual = storage.getValue(pageId, key);
      if (value !== null && actual === undefined) {
        skew.push({ pageId, key, expected: value, actual: undefined, kind: 'missing' });
      } else if (value === null && actual !== undefined) {
        skew.push({ pageId, key, expected: null, actual, kind: 'unexpected' });
      } else if (value !== null && actual !== value) {
        skew.push({ pageId, key, expected: value, actual, kind: 'diverged' });
      }
    }

    // Uncommitted residue: keys only a loser ever wrote must not survive.
    for (const [id, { pageId, key }] of loserTouched) {
      if (expected.has(id)) continue;
      const actual = storage.getValue(pageId, key);
      if (actual !== undefined) {
        skew.push({ pageId, key, expected: undefined, actual, kind: 'unexpected' });
      }
    }
    return skew;
  }
}
