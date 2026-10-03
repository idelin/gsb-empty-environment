import type { CdcEvent, CdcOp, RowImage } from "./types.ts";

/** Append-only in-memory CDC log capturing before/after images per change. */
export class CdcLog {
  #events: CdcEvent[] = [];
  #seq = 0;

  append(entry: {
    tsMs: number;
    txId: string;
    table: string;
    op: CdcOp;
    pk: string | number;
    before: RowImage | null;
    after: RowImage | null;
  }): CdcEvent {
    const event: CdcEvent = { seq: ++this.#seq, ...entry };
    this.#events.push(event);
    return event;
  }

  get size(): number {
    return this.#events.length;
  }

  all(): readonly CdcEvent[] {
    return this.#events;
  }

  /** Events committed by a given transaction, in commit order. */
  byTx(txId: string): CdcEvent[] {
    return this.#events.filter((e) => e.txId === txId);
  }

  /**
   * Events in (fromMs, toMs] commit-time window, in commit order.
   * fromMs is exclusive so re-running a window after a rollback is stable.
   */
  byTimeWindow(fromMs: number, toMs: number): CdcEvent[] {
    return this.#events.filter((e) => e.tsMs > fromMs && e.tsMs <= toMs);
  }
}
