// Page storage: a durable page file (pages.json, atomically replaced), an
// in-memory buffer pool carrying pageLSN/dirty/recLSN, strict txn-level
// exclusive locks, and a committed view used by external readers.
//
// Policy: NO-STEAL-ish — pages dirtied by a still-active transaction are never
// flushed; commit only *marks* them committed-dirty (log is already durable),
// and checkpoint flushes committed-dirty pages. External readers therefore
// only ever see committed versions.

import * as fs from "node:fs";
import * as path from "node:path";
import type { LSN, PageId, TxnId } from "./types.ts";

export class LockConflictError extends Error {
  constructor(pageId: PageId, holder: TxnId, requester: TxnId) {
    super(
      `lock conflict on page "${pageId}": held exclusively by txn ${holder}, requested by txn ${requester}`,
    );
    this.name = "LockConflictError";
  }
}

export interface BufferedPage {
  value: string;
  pageLSN: LSN;
  dirty: boolean;
  recLSN: LSN; // oldest LSN that dirtied the page since last flush
  dirtyByTxn: TxnId | null; // null once the dirtying txn committed
}

interface PageFileImage {
  [pageId: string]: { value: string; pageLSN: LSN };
}

export class PageStore {
  readonly filePath: string;
  private file: Map<PageId, { value: string; pageLSN: LSN }> = new Map();
  private pool: Map<PageId, BufferedPage> = new Map();
  private committedView: Map<PageId, string> = new Map();
  private locks: Map<PageId, TxnId> = new Map();

  constructor(dir: string) {
    this.filePath = path.join(dir, "pages.json");
    this.loadFile();
  }

  private loadFile(): void {
    let image: PageFileImage = {};
    try {
      image = JSON.parse(fs.readFileSync(this.filePath, "utf8")) as PageFileImage;
    } catch {
      image = {};
    }
    for (const [pageId, p] of Object.entries(image)) {
      this.file.set(pageId, { value: p.value, pageLSN: p.pageLSN });
      this.committedView.set(pageId, p.value);
    }
  }

  /** Durable, atomic flush of every committed-dirty page. */
  flush(): PageId[] {
    const flushed: PageId[] = [];
    for (const [pageId, page] of this.pool) {
      if (page.dirty && page.dirtyByTxn === null) {
        this.file.set(pageId, { value: page.value, pageLSN: page.pageLSN });
        page.dirty = false;
        flushed.push(pageId);
      }
    }
    if (flushed.length > 0 || !fs.existsSync(this.filePath)) {
      const image: PageFileImage = {};
      for (const [pageId, p] of this.file) {
        image[pageId] = { value: p.value, pageLSN: p.pageLSN };
      }
      const tmp = this.filePath + ".tmp";
      fs.writeFileSync(tmp, JSON.stringify(image, null, 2));
      fs.renameSync(tmp, this.filePath);
    }
    return flushed;
  }

  // ---- lock manager: strict txn-level exclusive ----
  acquireExclusive(pageId: PageId, txnId: TxnId): void {
    const holder = this.locks.get(pageId);
    if (holder !== undefined && holder !== txnId) {
      throw new LockConflictError(pageId, holder, txnId);
    }
    this.locks.set(pageId, txnId);
  }

  releaseAll(txnId: TxnId): void {
    for (const [pageId, holder] of this.locks) {
      if (holder === txnId) this.locks.delete(pageId);
    }
  }

  lockHolder(pageId: PageId): TxnId | undefined {
    return this.locks.get(pageId);
  }

  // ---- buffer pool ----
  private ensureBuffered(pageId: PageId): BufferedPage {
    let page = this.pool.get(pageId);
    if (!page) {
      const onDisk = this.file.get(pageId);
      page = {
        value: onDisk?.value ?? "",
        pageLSN: onDisk?.pageLSN ?? 0,
        dirty: false,
        recLSN: 0,
        dirtyByTxn: null,
      };
      this.pool.set(pageId, page);
    }
    return page;
  }

  /** Read the working (possibly uncommitted) image; caller must hold the lock. */
  readForTxn(pageId: PageId, txnId: TxnId): string {
    this.acquireExclusive(pageId, txnId);
    return this.ensureBuffered(pageId).value;
  }

  /** Apply a logged update/undo to the working image. */
  apply(pageId: PageId, txnId: TxnId, value: string, lsn: LSN): void {
    const page = this.ensureBuffered(pageId);
    if (!page.dirty) page.recLSN = lsn;
    page.value = value;
    page.pageLSN = lsn;
    page.dirty = true;
    page.dirtyByTxn = txnId;
  }

  /** Redo path (recovery): apply if the durable page is older than `lsn`. */
  redoApply(pageId: PageId, value: string, lsn: LSN): boolean {
    const page = this.ensureBuffered(pageId);
    if (page.pageLSN >= lsn) return false;
    if (!page.dirty) page.recLSN = lsn;
    page.value = value;
    page.pageLSN = lsn;
    page.dirty = true;
    page.dirtyByTxn = null;
    return true;
  }

  /** Mark all pages dirtied by `txnId` as committed-dirty and publish them. */
  publishCommitted(txnId: TxnId): void {
    for (const [pageId, page] of this.pool) {
      if (page.dirty && page.dirtyByTxn === txnId) {
        page.dirtyByTxn = null;
        this.committedView.set(pageId, page.value);
      }
    }
  }

  /** Recovery: rebuild the committed view from the fully-recovered pool. */
  publishAllRecovered(): void {
    for (const [pageId, page] of this.pool) {
      page.dirtyByTxn = null;
      this.committedView.set(pageId, page.value);
    }
  }

  /** External readers only ever see the committed version. */
  readCommitted(pageId: PageId): string {
    return this.committedView.get(pageId) ?? "";
  }

  committedSnapshot(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const [k, v] of [...this.committedView.entries()].sort()) out[k] = v;
    return out;
  }

  /** Committed-dirty pages = the dirty page table relevant for redo. */
  dirtyPageTable(): { pageId: PageId; recLSN: LSN }[] {
    const out: { pageId: PageId; recLSN: LSN }[] = [];
    for (const [pageId, page] of this.pool) {
      if (page.dirty && page.dirtyByTxn === null) {
        out.push({ pageId, recLSN: page.recLSN });
      }
    }
    return out;
  }

  /** All dirty pages (incl. uncommitted) — used only for diagnostics. */
  allDirtyPages(): { pageId: PageId; recLSN: LSN; uncommitted: boolean }[] {
    const out: { pageId: PageId; recLSN: LSN; uncommitted: boolean }[] = [];
    for (const [pageId, page] of this.pool) {
      if (page.dirty) {
        out.push({ pageId, recLSN: page.recLSN, uncommitted: page.dirtyByTxn !== null });
      }
    }
    return out;
  }

  pageLSN(pageId: PageId): LSN {
    return this.ensureBuffered(pageId).pageLSN;
  }
}
