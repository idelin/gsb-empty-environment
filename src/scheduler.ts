import { DependencyGraph } from "./graph.ts";
import { InMemoryTargetDb } from "./target-db.ts";
import type { ChangeEvent } from "./types.ts";

export interface ReplayOptions {
  /** Max events applied concurrently. Default 4. */
  concurrency?: number;
  /** Simulated per-event apply latency (ms), to exercise interleavings. */
  applyDelayMs?: number | ((event: ChangeEvent) => number);
  /** Sink for human-readable trace lines. */
  onTrace?: (line: string) => void;
}

export interface ReplayResult {
  /** Event ids in commit order. */
  appliedOrder: string[];
  /** Events that were suspended at least once, with the reason. */
  suspended: { id: string; reason: string }[];
  /** Full trace of scheduling decisions. */
  trace: string[];
}

/** Raised when pending events can never become ready (cycle / missing parent). */
export class DeadlockError extends Error {
  readonly blocked: ChangeEvent[];
  constructor(blocked: ChangeEvent[]) {
    super(
      `replay deadlocked: ${blocked.length} event(s) can never become ready: ` +
        blocked.map((e) => e.id).join(", "),
    );
    this.name = "DeadlockError";
    this.blocked = blocked;
  }
}

/**
 * Cross-table CDC replay scheduler.
 *
 * Events may arrive in any order. An event is only dispatched to a worker
 * when the target DB state guarantees it cannot violate a foreign key:
 *
 * - INSERT/UPDATE: every non-null FK parent row already exists
 *   (parent INSERT first — topological order).
 * - DELETE: no child row references the row anymore
 *   (child DELETE first — reverse topological order).
 *
 * Events that are not ready are suspended and re-evaluated after every
 * commit, so an OrderItem insert that arrived before its User insert is
 * held until the User (and Order) commits, then released automatically.
 */
export class CdcReplayScheduler {
  private db: InMemoryTargetDb;
  private graph: DependencyGraph;
  private concurrency: number;
  private applyDelayMs: number | ((event: ChangeEvent) => number);
  private onTrace?: (line: string) => void;

  constructor(db: InMemoryTargetDb, graph: DependencyGraph, opts: ReplayOptions = {}) {
    this.db = db;
    this.graph = graph;
    this.concurrency = Math.max(1, opts.concurrency ?? 4);
    this.applyDelayMs = opts.applyDelayMs ?? 0;
    this.onTrace = opts.onTrace;
  }

  async replay(events: ChangeEvent[]): Promise<ReplayResult> {
    const trace: string[] = [];
    const log = (line: string): void => {
      trace.push(line);
      this.onTrace?.(line);
    };
    const pending = [...events].sort((a, b) => a.seq - b.seq);
    const suspended = new Map<string, string>();
    const suspendedList: { id: string; reason: string }[] = [];
    const appliedOrder: string[] = [];
    const running = new Set<Promise<void>>();

    const blockReason = (e: ChangeEvent): string | null => {
      if (e.op === "delete") {
        const pk = this.graph.tableDef(e.table).primaryKey;
        const blocking = this.db.blockingChildren(e.table, e.row[pk]);
        return blocking.length > 0
          ? `waiting for child deletes (${blocking.join(", ")})`
          : null;
      }
      const missing = this.db.missingParents(e.table, e.row);
      return missing.length > 0 ? `waiting for parent rows (${missing.join(", ")})` : null;
    };

    const apply = async (e: ChangeEvent): Promise<void> => {
      const delay =
        typeof this.applyDelayMs === "function" ? this.applyDelayMs(e) : this.applyDelayMs;
      if (delay > 0) await new Promise((r) => setTimeout(r, delay));
      const pk = this.graph.tableDef(e.table).primaryKey;
      if (e.op === "insert") this.db.insert(e.table, e.row, e.id);
      else if (e.op === "update") this.db.update(e.table, e.row, e.id);
      else this.db.delete(e.table, e.row[pk], e.id);
      appliedOrder.push(e.id);
      log(`APPLY   ${e.id} (${e.op} ${e.table} ${this.pkLabel(e)})`);
    };

    while (pending.length > 0 || running.size > 0) {
      // Dispatch every ready event up to the concurrency limit.
      let dispatched = false;
      for (let i = 0; i < pending.length && running.size < this.concurrency; ) {
        const e = pending[i];
        const reason = blockReason(e);
        if (reason !== null) {
          if (!suspended.has(e.id)) {
            suspended.set(e.id, reason);
            suspendedList.push({ id: e.id, reason });
            log(`SUSPEND ${e.id} (${e.op} ${e.table} ${this.pkLabel(e)}): ${reason}`);
          }
          i++;
          continue;
        }
        pending.splice(i, 1);
        if (suspended.delete(e.id)) {
          log(`RESUME  ${e.id} (${e.op} ${e.table} ${this.pkLabel(e)})`);
        }
        const p = apply(e).finally(() => running.delete(p));
        running.add(p);
        dispatched = true;
      }
      if (running.size > 0) {
        await Promise.race(running);
      } else if (!dispatched) {
        // Nothing running and nothing ready: unresolvable dependencies.
        throw new DeadlockError(pending);
      }
    }

    return { appliedOrder, suspended: suspendedList, trace };
  }

  private pkLabel(e: ChangeEvent): string {
    const pk = this.graph.tableDef(e.table).primaryKey;
    return `(${pk}=${String(e.row[pk])})`;
  }
}
