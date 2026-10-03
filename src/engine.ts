import { CdcLog } from "./cdc-log.ts";
import { generateRollbackPackage } from "./undo-generator.ts";
import { TableStore } from "./store.ts";
import type { CompensationOp, RollbackPackage } from "./types.ts";

export interface ReplayResult {
  packageId: string;
  applied: number;
  /** Per-op execution record, in replay order. */
  statements: string[];
}

/**
 * Disaster-recovery engine: builds rollback packages from the CDC log and
 * replays them against the live store to reverse accidental changes.
 */
export class RollbackEngine {
  #log: CdcLog;
  #stores = new Map<string, TableStore>();
  #pkColumn: string;

  constructor(log: CdcLog, options: { pkColumn?: string } = {}) {
    this.#log = log;
    this.#pkColumn = options.pkColumn ?? "id";
  }

  registerStore(store: TableStore): void {
    this.#stores.set(store.name, store);
  }

  /** Build a rollback package for every change committed by a transaction. */
  buildPackageForTx(txId: string, reason?: string): RollbackPackage {
    const events = this.#log.byTx(txId);
    if (events.length === 0) throw new Error(`no CDC events for tx ${txId}`);
    return generateRollbackPackage(events, {
      pkColumn: this.#pkColumn,
      reason: reason ?? `rollback tx ${txId}`,
    });
  }

  /** Build a rollback package for all changes in the (fromMs, toMs] window. */
  buildPackageForWindow(fromMs: number, toMs: number, reason?: string): RollbackPackage {
    const events = this.#log.byTimeWindow(fromMs, toMs);
    if (events.length === 0) {
      throw new Error(`no CDC events in window (${fromMs}, ${toMs}]`);
    }
    return generateRollbackPackage(events, {
      pkColumn: this.#pkColumn,
      reason: reason ?? `rollback window (${fromMs}, ${toMs}]`,
    });
  }

  /**
   * Replay a rollback package against the registered stores.
   * Compensation writes go through TableStore DML so they are themselves
   * captured in the CDC log (auditable, and re-revertible).
   */
  replay(pkg: RollbackPackage): ReplayResult {
    const statements: string[] = [];
    for (const op of pkg.ops) {
      this.#applyOp(op);
      statements.push(op.sql);
    }
    return { packageId: pkg.id, applied: statements.length, statements };
  }

  /** Convenience: build + replay in one call. */
  rollbackTx(txId: string, reason?: string): ReplayResult {
    return this.replay(this.buildPackageForTx(txId, reason));
  }

  #applyOp(op: CompensationOp): void {
    const store = this.#stores.get(op.table);
    if (store === undefined) throw new Error(`no store registered for table ${op.table}`);
    switch (op.op) {
      case "DELETE":
        store.delete(op.pk);
        break;
      case "INSERT":
        if (op.image === null) throw new Error("INSERT compensation missing image");
        store.insert(op.image);
        break;
      case "UPDATE": {
        if (op.image === null) throw new Error("UPDATE compensation missing image");
        const { [store.pkColumn]: _pk, ...patch } = op.image;
        store.update(op.pk, patch);
        break;
      }
    }
  }
}
