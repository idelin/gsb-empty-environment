import type { LogRecord, PageId, TxnId } from "./types.ts";

/**
 * Independent re-derivation of the final committed page contents from a
 * complete log, written separately from the recovery code:
 *  - updates and CLRs of committed txns stand as logged (LSN order);
 *  - for loser txns, walk each chain backwards (skipping already-compensated
 *    updates via undoNext, honouring pre-crash CLRs) and apply the
 *    compensations highest-LSN-first, exactly where restart undo would land.
 */
export function rebuildFinalPages(records: LogRecord[]): Map<PageId, number> {
  const committed = new Set<TxnId>();
  const touched = new Set<TxnId>();
  for (const r of records) {
    if (r.type === "commit") committed.add(r.txn);
    if (r.type === "update" || r.type === "clr") touched.add(r.txn);
  }

  const pages = new Map<PageId, number>();
  for (const r of records) {
    if ((r.type === "update" || r.type === "clr") && committed.has(r.txn)) {
      pages.set(r.page, r.after);
    }
  }

  const byLSN = new Map(records.map((r) => [r.lsn, r]));
  const undos: { lsn: number; page: PageId; value: number }[] = [];
  for (const txn of touched) {
    if (committed.has(txn)) continue;
    let last = 0;
    for (const r of records) {
      if ("txn" in r && r.txn === txn && r.type !== "end") last = Math.max(last, r.lsn);
    }
    let cur = last;
    while (cur > 0) {
      const rec = byLSN.get(cur);
      if (!rec) break;
      if (rec.type === "update") {
        undos.push({ lsn: rec.lsn, page: rec.page, value: rec.before });
        cur = rec.prevLSN;
      } else if (rec.type === "clr") {
        undos.push({ lsn: rec.lsn, page: rec.page, value: rec.after });
        cur = rec.undoNext;
      } else if (rec.type === "abort" || rec.type === "commit") {
        cur = rec.prevLSN;
      } else {
        break;
      }
    }
  }
  undos.sort((a, b) => b.lsn - a.lsn);
  for (const u of undos) pages.set(u.page, u.value);
  return pages;
}

export function comparePages(
  expected: Map<PageId, number>,
  actual: Record<string, number>,
): { match: boolean; diffs: string[] } {
  const diffs: string[] = [];
  const keys = new Set([...expected.keys(), ...Object.keys(actual)]);
  for (const k of keys) {
    const e = expected.get(k) ?? 0;
    const a = actual[k] ?? 0;
    if (e !== a) diffs.push(`page ${k}: expected ${e}, got ${a}`);
  }
  return { match: diffs.length === 0, diffs };
}
