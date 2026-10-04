// System: wires log + pages + transactions, provides checkpoint, log-segment
// reclamation planning (with refusal when recovery info is insufficient),
// and crash/restart simulation on top of the synthetic file layer.

import { LogManager } from "./logManager.ts";
import { PageStore } from "./pageStore.ts";
import { TransactionManager } from "./txnManager.ts";
import { recover } from "./recovery.ts";
import type { RecoveryReport } from "./recovery.ts";
import type { LSN, PageId } from "./types.ts";

export interface TruncationPlan {
  boundary: LSN; // retain every record with lsn >= boundary
  reasons: string[];
  canTruncateEverything: boolean;
}

export class TruncationRefusedError extends Error {
  readonly plan: TruncationPlan;
  constructor(requested: LSN, plan: TruncationPlan) {
    super(
      `truncation to LSN ${requested} refused: insufficient recovery information; ` +
        `safe retention boundary is LSN ${plan.boundary}`,
    );
    this.name = "TruncationRefusedError";
    this.plan = plan;
  }
}

export interface SystemOptions {
  maxRecordsPerSegment?: number;
}

export class System {
  readonly dir: string;
  readonly log: LogManager;
  readonly pages: PageStore;
  readonly txns: TransactionManager;
  readonly recoveryReport: RecoveryReport;
  private ckptCounter: number;

  private constructor(dir: string, log: LogManager, pages: PageStore, report: RecoveryReport) {
    this.dir = dir;
    this.log = log;
    this.pages = pages;
    this.recoveryReport = report;
    let maxTxn = 0;
    let maxCkpt = 0;
    for (const r of log.all()) {
      if ("txnId" in r && r.txnId > maxTxn) maxTxn = r.txnId;
      if ((r.type === "CKPT_BEGIN" || r.type === "CKPT_END") && r.ckptId > maxCkpt) {
        maxCkpt = r.ckptId;
      }
    }
    this.txns = new TransactionManager(log, pages, maxTxn + 1);
    this.ckptCounter = maxCkpt;
  }

  /** Open (or create) a system on `dir`; always runs recovery first. */
  static open(dir: string, opts: SystemOptions = {}): System {
    const log = new LogManager(dir, opts.maxRecordsPerSegment ?? 100);
    const pages = new PageStore(dir);
    const report = recover(log, pages);
    return new System(dir, log, pages, report);
  }

  // ---- transactional API (thin wrappers for convenience) ----
  begin(): number { return this.txns.begin(); }
  update(txn: number, page: PageId, value: string): LSN { return this.txns.update(txn, page, value); }
  savepoint(txn: number, name: string): LSN { return this.txns.savepoint(txn, name); }
  rollbackTo(txn: number, name: string): LSN[] { return this.txns.rollbackTo(txn, name); }
  commit(txn: number): void { this.txns.commit(txn); }
  abort(txn: number): void { this.txns.abort(txn); }

  /** External reader: committed versions only. */
  readPage(page: PageId): string { return this.pages.readCommitted(page); }

  /** Fuzzy checkpoint: flush committed-dirty pages, log BEGIN+snapshot+END. */
  checkpoint(): { ckptId: number; beginLSN: LSN; endLSN: LSN } {
    this.pages.flush();
    const ckptId = ++this.ckptCounter;
    const beginLSN = this.log.allocLSN();
    this.log.append({ type: "CKPT_BEGIN", lsn: beginLSN, ckptId });
    const endLSN = this.log.allocLSN();
    this.log.append({
      type: "CKPT_END",
      lsn: endLSN,
      ckptId,
      dirtyPages: this.pages.dirtyPageTable(),
      activeTxns: this.txns.activeTxns.map((t) => ({
        txnId: t.txnId,
        firstLSN: t.firstLSN,
        lastLSN: t.lastLSN,
        status: t.status as "running" | "aborting",
      })),
    });
    return { ckptId, beginLSN, endLSN };
  }

  /**
   * Retention boundary = min of:
   *  - oldest log any active transaction still needs (its firstLSN),
   *  - oldest redo start of any committed-dirty page (recLSN),
   *  - the last complete checkpoint (analysis starts there).
   */
  planTruncation(): TruncationPlan {
    const candidates: { lsn: LSN; reason: string }[] = [];
    for (const t of this.txns.activeTxns) {
      candidates.push({
        lsn: t.firstLSN,
        reason: `active txn ${t.txnId} (status ${t.status}) may still roll back; its chain starts at LSN ${t.firstLSN}`,
      });
    }
    for (const d of this.pages.dirtyPageTable()) {
      candidates.push({
        lsn: d.recLSN,
        reason: `dirty page "${d.pageId}" is not yet flushed; redo for it starts at LSN ${d.recLSN}`,
      });
    }
    const ckpt = this.log.lastCompleteCheckpoint();
    if (ckpt) {
      candidates.push({
        lsn: ckpt.beginLSN,
        reason: `last complete checkpoint #${ckpt.end.ckptId} starts analysis at LSN ${ckpt.beginLSN}`,
      });
    } else {
      candidates.push({
        lsn: this.log.firstAvailableLSN,
        reason: "no complete checkpoint exists; recovery must scan from the first available LSN",
      });
    }
    let boundary: LSN | null = null;
    const reasons: string[] = [];
    for (const c of candidates.sort((a, b) => a.lsn - b.lsn)) {
      if (boundary === null || c.lsn === boundary) {
        reasons.push(c.reason + "  <-- binding");
        boundary = c.lsn;
      } else {
        reasons.push(c.reason);
      }
    }
    if (boundary === null) {
      return { boundary: this.log.highWaterLSN + 1, reasons: ["no active txns, no dirty pages, no checkpoints: entire log is reclaimable"], canTruncateEverything: true };
    }
    return { boundary, reasons, canTruncateEverything: false };
  }

  /**
   * Reclaim log segments up to `requestedLSN`. Refuses when the request would
   * cut into log still needed for recovery (active-txn chains, dirty-page
   * redo, or the last complete checkpoint).
   */
  truncateLog(requestedLSN?: LSN): { boundary: LSN; removed: string[]; reasons: string[] } {
    const plan = this.planTruncation();
    const target = requestedLSN ?? plan.boundary;
    if (target > plan.boundary) throw new TruncationRefusedError(target, plan);
    const { removed } = this.log.truncate(target, plan.reasons);
    return { boundary: plan.boundary, removed, reasons: plan.reasons };
  }

  // ---- fault-injection passthroughs (synthetic file layer) ----
  armCrashAfterAppends(n: number): void { this.log.store.armCrashAfterAppends(n); }
  armTornNextAppend(): void { this.log.store.armTornNextAppend(); }
}
