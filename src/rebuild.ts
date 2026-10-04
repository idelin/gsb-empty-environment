// Independent reconstruction of the final committed pages from a log alone:
// replay the after-images of committed transactions in LSN order onto empty
// pages. Used to cross-check recovery after log truncation.

import type { LogRecord, PageId } from "./types.ts";

export function rebuildFromLog(records: LogRecord[]): Map<PageId, string> {
  const committed = new Set<number>();
  for (const r of records) {
    if (r.type === "COMMIT") committed.add(r.txnId);
  }
  const pages = new Map<PageId, string>();
  for (const r of records) {
    if ((r.type === "UPDATE" || r.type === "CLR") && committed.has(r.txnId)) {
      pages.set(r.pageId, r.after);
    }
  }
  return pages;
}

export function snapshotsEqual(
  a: Map<PageId, string>,
  b: Record<string, string>,
): { equal: boolean; diffs: string[] } {
  const diffs: string[] = [];
  const keys = new Set([...a.keys(), ...Object.keys(b)]);
  for (const k of keys) {
    const av = a.get(k) ?? "";
    const bv = b[k] ?? "";
    if (av !== bv) diffs.push(`page "${k}": rebuild=${JSON.stringify(av)} recovered=${JSON.stringify(bv)}`);
  }
  return { equal: diffs.length === 0, diffs };
}
