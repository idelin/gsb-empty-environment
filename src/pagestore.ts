import fs from "node:fs";
import path from "node:path";
import type { DPTEntry, LSN, PageId, TxnId } from "./types.ts";

interface BufPage {
  value: number;
  committedValue: number;
  pageLSN: LSN;
  committedLSN: LSN;
  dirty: boolean;
  recLSN: LSN;
  dirtyBy: TxnId | null;
}

/**
 * Buffer pool + committed-version store.
 *  - Transactions read/write the buffer image (`readForTxn` / `applyUpdate`).
 *  - External readers only ever see the committed version (`readCommitted`).
 *  - `dirtyBy` enforces no-steal: a page owned by an active txn is not flushed.
 */
export class PageStore {
  private buf = new Map<PageId, BufPage>();
  private disk = new Map<PageId, { value: number; pageLSN: LSN }>();
  private file: string | null;

  constructor(dir?: string) {
    this.file = dir ? path.join(dir, "pages.json") : null;
    if (this.file && fs.existsSync(this.file)) {
      const data = JSON.parse(fs.readFileSync(this.file, "utf8"));
      for (const [k, v] of Object.entries(data.pages as Record<string, { value: number; pageLSN: LSN }>)) {
        this.disk.set(k, v);
      }
    }
  }

  private load(page: PageId): BufPage {
    let b = this.buf.get(page);
    if (!b) {
      const d = this.disk.get(page);
      const v = d ? d.value : 0;
      const pl = d ? d.pageLSN : 0;
      b = { value: v, committedValue: v, pageLSN: pl, committedLSN: pl, dirty: false, recLSN: 0, dirtyBy: null };
      this.buf.set(page, b);
    }
    return b;
  }

  readCommitted(page: PageId): number {
    return this.load(page).committedValue;
  }

  readForTxn(page: PageId): number {
    return this.load(page).value;
  }

  applyUpdate(page: PageId, value: number, lsn: LSN, txn: TxnId | null): void {
    const b = this.load(page);
    b.value = value;
    b.pageLSN = lsn;
    if (!b.dirty) {
      b.dirty = true;
      b.recLSN = lsn;
    }
    b.dirtyBy = txn;
  }

  onCommit(txn: TxnId): void {
    for (const b of this.buf.values()) {
      if (b.dirtyBy === txn) {
        b.committedValue = b.value;
        b.committedLSN = b.pageLSN;
        b.dirtyBy = null;
      }
    }
  }

  onAbortEnd(txn: TxnId): void {
    for (const b of this.buf.values()) {
      if (b.dirtyBy === txn) {
        b.value = b.committedValue;
        b.dirtyBy = null;
      }
    }
  }

  /** After recovery every txn has ended; the buffer image IS the committed one. */
  finalizeRecovery(): void {
    for (const b of this.buf.values()) {
      b.committedValue = b.value;
      b.committedLSN = b.pageLSN;
      b.dirtyBy = null;
    }
  }

  flushEligible(): PageId[] {
    const flushed: PageId[] = [];
    for (const [p, b] of this.buf) {
      if (b.dirty && b.dirtyBy === null) {
        this.disk.set(p, { value: b.committedValue, pageLSN: b.committedLSN });
        b.dirty = false;
        b.value = b.committedValue;
        b.pageLSN = b.committedLSN;
        flushed.push(p);
      }
    }
    return flushed;
  }

  dirtyPageTable(): DPTEntry[] {
    const out: DPTEntry[] = [];
    for (const [page, b] of this.buf) if (b.dirty) out.push({ page, recLSN: b.recLSN });
    return out;
  }

  pageLSN(page: PageId): LSN {
    return this.load(page).pageLSN;
  }

  persistDisk(): void {
    if (!this.file) return;
    const tmp = `${this.file}.tmp`;
    fs.writeFileSync(tmp, JSON.stringify({ pages: Object.fromEntries(this.disk) }, null, 1));
    fs.renameSync(tmp, this.file);
  }

  committedSnapshot(): Record<string, number> {
    const out: Record<string, number> = {};
    const keys = new Set<PageId>([...this.disk.keys(), ...this.buf.keys()]);
    for (const k of keys) out[k] = this.readCommitted(k);
    return out;
  }
}
