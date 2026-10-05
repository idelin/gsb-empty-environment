import { existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { hashKey } from "./axial.ts";
import type { CellKey } from "./axial.ts";
import { applyEffect, emptyState, planOp } from "./engine.ts";
import type { Effect, State } from "./engine.ts";
import { Journal } from "./journal.ts";
import { ERR, cloneResult, hashPayload } from "./model.ts";
import type {
  CreateArgs,
  LeaseRec,
  Meta,
  OpKind,
  OpResult,
  ReassignArgs,
  ReleaseArgs,
  ReplaceArgs,
  SetCapacityArgs,
  SetQuotaArgs,
} from "./model.ts";

/**
 * Fault-injection / observation hooks. `beforePartitionWrite` may throw to
 * simulate a failed partition write (transaction aborts cleanly). Throwing
 * a `CrashFault` from `beforePartitionWrite` or `beforeCommit` simulates a
 * process crash: no abort/commit record is written and nothing is applied.
 * `afterPartitionWrite` lets tests query the store mid-transaction to
 * verify atomic visibility.
 */
export interface Hooks {
  beforePartitionWrite?: (partition: number, txId: number) => void;
  afterPartitionWrite?: (store: LeaseStore, partition: number, txId: number) => void;
  beforeCommit?: (txId: number) => void;
}

export class CrashFault extends Error {
  constructor(message = "simulated crash") {
    super(message);
    this.name = "CrashFault";
  }
}

interface IntentRecord {
  type: "intent";
  txId: number;
  kind: OpKind;
  tick: number;
  requestId: string | null;
  hash: string | null;
  effect: Effect;
  result: OpResult;
}

export const COORDINATOR_LOG = "coordinator.log";

export function partitionLogPath(dir: string, partition: number): string {
  return join(dir, `part-${partition}.log`);
}

/**
 * Transactional lease store. Cells are owned by partitions (ownership is
 * reassignable); every mutation is a single transaction that:
 *   1. validates against committed state and plans an Effect,
 *   2. appends an intent record to the coordinator journal (fsync),
 *   3. appends per-partition effect records (fsync each),
 *   4. appends the commit record (the commit point),
 *   5. applies the effect to in-memory state in one synchronous section.
 * Queries only ever read committed in-memory state, so a multi-partition
 * replace is atomically visible. On recovery, intents without a commit
 * record are never applied: pending transactions are not treated as
 * having released any capacity.
 */
export class LeaseStore {
  readonly dir: string;
  private state: State;
  private coordinator: Journal;
  private parts: Map<number, Journal>;
  private nextTxId: number;
  private hooks: Hooks;
  private orphans: IntentRecord[];

  private constructor(
    dir: string,
    state: State,
    coordinator: Journal,
    nextTxId: number,
    orphans: IntentRecord[],
    hooks: Hooks,
  ) {
    this.dir = dir;
    this.state = state;
    this.coordinator = coordinator;
    this.parts = new Map();
    this.nextTxId = nextTxId;
    this.orphans = orphans;
    this.hooks = hooks;
  }

  static init(dir: string, meta: Meta): void {
    mkdirSync(dir, { recursive: true });
    const path = join(dir, COORDINATOR_LOG);
    if (existsSync(path)) {
      throw new Error(`store already initialized at ${dir}`);
    }
    const journal = new Journal(path);
    journal.append({ type: "meta", meta });
    journal.close();
  }

  static open(dir: string, hooks: Hooks = {}): LeaseStore {
    const path = join(dir, COORDINATOR_LOG);
    if (!existsSync(path)) {
      throw new Error(`store not initialized at ${dir}`);
    }
    const records = Journal.readAll(path);
    const state = emptyState();
    const pending = new Map<number, IntentRecord>();
    let maxTxId = 0;
    for (const record of records) {
      const rec = record as Record<string, unknown>;
      switch (rec.type) {
        case "meta":
          state.meta = rec.meta as Meta;
          break;
        case "intent": {
          const intent = rec as unknown as IntentRecord;
          pending.set(intent.txId, intent);
          if (intent.txId > maxTxId) maxTxId = intent.txId;
          break;
        }
        case "commit": {
          const txId = rec.txId as number;
          const intent = pending.get(txId);
          if (!intent) throw new Error(`corrupt journal: commit for unknown tx ${txId}`);
          applyEffect(state, intent.effect);
          pending.delete(txId);
          break;
        }
        case "abort": {
          pending.delete(rec.txId as number);
          break;
        }
        default:
          throw new Error(`corrupt journal: unknown record type ${String(rec.type)}`);
      }
    }
    // Leftover intents never reached their commit record. They are NOT
    // applied: their old leases still hold every cell and quota slot.
    const orphans = [...pending.values()].sort((a, b) => a.txId - b.txId);
    return new LeaseStore(dir, state, new Journal(path), maxTxId + 1, orphans, hooks);
  }

  close(): void {
    this.coordinator.close();
    for (const journal of this.parts.values()) journal.close();
    this.parts.clear();
  }

  /** Intents that never reached a decision (crashed transactions). */
  pendingIntents(): IntentRecord[] {
    return this.orphans.slice();
  }

  get currentTick(): number {
    return this.state.tick;
  }

  get meta(): Meta {
    if (!this.state.meta) throw new Error("store not initialized");
    return this.state.meta;
  }

  partitionOf(cell: CellKey): number {
    const owned = this.state.ownership.get(cell);
    if (owned !== undefined) return owned;
    return hashKey(cell) % this.meta.numPartitions;
  }

  /** Query: reads committed state only. */
  getLease(leaseId: string): LeaseRec | undefined {
    const lease = this.state.leases.get(leaseId);
    return lease ? { ...lease, cells: [...lease.cells] } : undefined;
  }

  /** Read-only view for ledger computation. */
  view(): State {
    return this.state;
  }

  /** Serializable snapshot for differential comparison. */
  dump(): Record<string, unknown> {
    const s = this.state;
    const sortEntries = (m: Map<string, number>) =>
      [...m.entries()].sort(([a], [b]) => (a < b ? -1 : 1));
    return {
      tick: s.tick,
      leases: [...s.leases.values()]
        .map((l) => ({ ...l, cells: [...l.cells] }))
        .sort((a, b) => (a.leaseId < b.leaseId ? -1 : 1)),
      capacity: sortEntries(s.capacity),
      quota: sortEntries(s.quota),
      ownership: sortEntries(s.ownership),
      requests: [...s.requests.keys()].sort(),
    };
  }

  tick(t: number): OpResult {
    return this.execute("tick", {}, t, undefined);
  }

  create(args: CreateArgs, tick: number): OpResult {
    return this.execute("create", args, tick, args.requestId);
  }

  replace(args: ReplaceArgs, tick: number): OpResult {
    return this.execute("replace", args, tick, args.requestId);
  }

  release(args: ReleaseArgs, tick: number): OpResult {
    return this.execute("release", args, tick, args.requestId);
  }

  setCapacity(args: SetCapacityArgs, tick: number): OpResult {
    return this.execute("setCapacity", args, tick, undefined);
  }

  setQuota(args: SetQuotaArgs, tick: number): OpResult {
    return this.execute("setQuota", args, tick, undefined);
  }

  reassign(args: ReassignArgs, tick: number): OpResult {
    return this.execute("reassign", args, tick, undefined);
  }

  private partJournal(partition: number): Journal {
    let journal = this.parts.get(partition);
    if (!journal) {
      journal = new Journal(partitionLogPath(this.dir, partition));
      this.parts.set(partition, journal);
    }
    return journal;
  }

  /**
   * Cells whose occupancy the effect changes, grouped by owning partition
   * (ownership as of pre-transaction state; a reassign logs to the new
   * owner). Used for the per-partition effect records.
   */
  private partitionEffects(effect: Effect): Map<number, CellKey[]> {
    const cells = new Set<CellKey>();
    if (effect.expired) {
      for (const id of effect.expired) {
        const lease = this.state.leases.get(id);
        if (lease) for (const cell of lease.cells) cells.add(cell);
      }
    }
    if (effect.deleteLease !== undefined) {
      const lease = this.state.leases.get(effect.deleteLease);
      if (lease) for (const cell of lease.cells) cells.add(cell);
    }
    if (effect.upsertLease) {
      const old = this.state.leases.get(effect.upsertLease.leaseId);
      if (old) for (const cell of old.cells) cells.add(cell);
      for (const cell of effect.upsertLease.cells) cells.add(cell);
    }
    if (effect.setCapacity) cells.add(effect.setCapacity.cell);
    const out = new Map<number, CellKey[]>();
    for (const cell of cells) {
      const partition = this.partitionOf(cell);
      const list = out.get(partition) ?? [];
      list.push(cell);
      out.set(partition, list);
    }
    if (effect.reassign) {
      const list = out.get(effect.reassign.partition) ?? [];
      list.push(effect.reassign.cell);
      out.set(effect.reassign.partition, list);
    }
    for (const list of out.values()) list.sort();
    return new Map([...out.entries()].sort(([a], [b]) => a - b));
  }

  private execute(kind: OpKind, args: unknown, tick: number, requestId?: string): OpResult {
    if (!this.state.meta) throw new Error("store not initialized");

    // Idempotency first: a seen requestId replays its cached result, or
    // conflicts if the content differs.
    if (requestId !== undefined) {
      const seen = this.state.requests.get(requestId);
      if (seen) {
        if (hashPayload(kind, args) === seen.hash) return cloneResult(seen.result);
        return { ok: false, error: ERR.REQUEST_CONFLICT, requestId };
      }
    }
    if (!Number.isInteger(tick) || tick < this.state.tick) {
      return { ok: false, error: ERR.TICK_REGRESSION, tick, currentTick: this.state.tick };
    }

    const hash = requestId !== undefined ? hashPayload(kind, args) : null;
    const plan = planOp(this.state, kind, args, tick, hash, requestId ?? null);
    const txId = this.nextTxId++;
    const intent: IntentRecord = {
      type: "intent",
      txId,
      kind,
      tick,
      requestId: requestId ?? null,
      hash,
      effect: plan.effect,
      result: plan.result,
    };
    this.coordinator.append(intent);

    try {
      for (const [partition, cells] of this.partitionEffects(plan.effect)) {
        this.hooks.beforePartitionWrite?.(partition, txId);
        this.partJournal(partition).append({ type: "effect", txId, partition, cells });
        this.hooks.afterPartitionWrite?.(this, partition, txId);
      }
      this.hooks.beforeCommit?.(txId);
    } catch (err) {
      if (err instanceof CrashFault) throw err; // simulated crash: no decision record
      const reason = err instanceof Error ? err.message : String(err);
      this.coordinator.append({ type: "abort", txId, reason });
      return { ok: false, error: ERR.TX_ABORTED, cause: reason };
    }

    this.coordinator.append({ type: "commit", txId });
    applyEffect(this.state, plan.effect);
    return plan.result;
  }
}
