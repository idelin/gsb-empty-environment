import type { MessageBus } from "./bus.ts";
import {
  applyToEntry,
  type FieldEntry,
  type FieldVersion,
  type MergeStrategy,
} from "./merge.ts";
import type { Operation, OpKind } from "./operations.ts";
import {
  causallyReady,
  mergeClocks,
  tick,
  type VectorClock,
} from "./vectorClock.ts";

export interface TxnSummary {
  txnId: string;
  origin: string;
  local: boolean;
  ops: Operation[];
}

export type Snapshot = Record<string, unknown>;

export interface ReplicaOptions {
  id: string;
  bus: MessageBus;
  strategies?: Record<string, MergeStrategy>;
  defaultStrategy?: MergeStrategy;
  now?: () => number;
}

export type CommitObserver = (snapshot: Snapshot, txn: TxnSummary) => void;

interface Draft {
  kind: OpKind;
  field: string;
  value?: unknown;
}

function jsonKey(value: unknown): string {
  return JSON.stringify(value) ?? "null";
}

function strategyFor(
  field: string,
  fieldStrategies: Record<string, MergeStrategy>,
  defaultStrategy: MergeStrategy,
): MergeStrategy {
  return fieldStrategies[field] ?? defaultStrategy;
}

function renderEntries(
  entries: Map<string, FieldEntry>,
  fieldStrategies: Record<string, MergeStrategy>,
  defaultStrategy: MergeStrategy,
): Snapshot {
  const out: Snapshot = {};
  for (const [field, entry] of entries) {
    const visible = entry.versions.filter((v) => !v.deleted);
    if (visible.length === 0) continue;
    if (
      strategyFor(field, fieldStrategies, defaultStrategy) === "multi-value" &&
      visible.length > 1
    ) {
      out[field] = visible
        .map((v) => v.value)
        .sort((a, b) => (jsonKey(a) < jsonKey(b) ? -1 : 1));
    } else {
      out[field] = visible[0].value;
    }
  }
  return out;
}

export function replayLog(
  log: readonly (readonly Operation[])[],
  options: {
    strategies?: Record<string, MergeStrategy>;
    defaultStrategy?: MergeStrategy;
  } = {},
): Snapshot {
  const fieldStrategies = options.strategies ?? {};
  const defaultStrategy = options.defaultStrategy ?? "lww";
  const entries = new Map<string, FieldEntry>();
  for (const ops of log) {
    for (const op of ops) {
      const version: FieldVersion = {
        value: op.value,
        deleted: op.kind === "delete",
        ts: op.ts,
        replicaId: op.replicaId,
        clock: op.clock,
      };
      entries.set(
        op.field,
        applyToEntry(
          entries.get(op.field),
          version,
          strategyFor(op.field, fieldStrategies, defaultStrategy),
        ),
      );
    }
  }
  return renderEntries(entries, fieldStrategies, defaultStrategy);
}

export class Replica {
  readonly id: string;
  private readonly bus: MessageBus;
  private readonly fieldStrategies: Record<string, MergeStrategy>;
  private readonly defaultStrategy: MergeStrategy;
  private readonly nowFn: () => number;

  private clock: VectorClock = {};
  private entries = new Map<string, FieldEntry>();
  private applied = new Set<string>();
  private pending = new Map<string, Operation[]>();
  private readonly observers: CommitObserver[] = [];
  private readonly log: Operation[][] = [];

  constructor(options: ReplicaOptions) {
    this.id = options.id;
    this.bus = options.bus;
    this.fieldStrategies = options.strategies ?? {};
    this.defaultStrategy = options.defaultStrategy ?? "lww";
    this.nowFn = options.now ?? (() => Date.now());
    this.bus.subscribe(this.id, (op) => this.receive(op));
  }

  get txnLog(): readonly Operation[][] {
    return this.log;
  }

  onCommit(fn: CommitObserver): void {
    this.observers.push(fn);
  }

  transact(
    mutate: (txn: {
      set: (field: string, value: unknown) => void;
      delete: (field: string) => void;
    }) => void,
  ): string {
    this.clock = tick(this.clock, this.id);
    const seq = this.clock[this.id];
    const txnId = `${this.id}#${seq}`;
    const drafts: Draft[] = [];
    mutate({
      set: (field, value) => drafts.push({ kind: "set", field, value }),
      delete: (field) => drafts.push({ kind: "delete", field }),
    });
    const ops: Operation[] = drafts.map((draft, index) => ({
      id: `${txnId}/${index}`,
      replicaId: this.id,
      txnId,
      txnSize: drafts.length,
      clock: { ...this.clock },
      ts: this.nowFn(),
      kind: draft.kind,
      field: draft.field,
      value: draft.value,
    }));
    this.commit(ops, true);
    for (const op of ops) this.bus.publish(op);
    return txnId;
  }

  receive(op: Operation): void {
    if (this.applied.has(op.id)) return;
    let buffer = this.pending.get(op.txnId);
    if (!buffer) {
      buffer = [];
      this.pending.set(op.txnId, buffer);
    }
    if (buffer.some((existing) => existing.id === op.id)) return;
    buffer.push(op);
    this.drain();
  }

  private drain(): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (const [txnId, ops] of this.pending) {
        const complete = ops.length === ops[0].txnSize;
        if (!complete) continue;
        if (!causallyReady(ops[0].clock, this.clock, ops[0].replicaId)) continue;
        this.pending.delete(txnId);
        this.commit(ops, false);
        progressed = true;
      }
    }
  }

  private commit(ops: Operation[], local: boolean): void {
    const next = new Map(this.entries);
    for (const op of ops) {
      this.applied.add(op.id);
      const version: FieldVersion = {
        value: op.value,
        deleted: op.kind === "delete",
        ts: op.ts,
        replicaId: op.replicaId,
        clock: op.clock,
      };
      next.set(
        op.field,
        applyToEntry(
          next.get(op.field),
          version,
          strategyFor(op.field, this.fieldStrategies, this.defaultStrategy),
        ),
      );
    }
    this.entries = next;
    this.clock = mergeClocks(this.clock, ops[0].clock);
    this.log.push(ops);

    const snapshot = this.snapshot();
    const summary: TxnSummary = {
      txnId: ops[0].txnId,
      origin: ops[0].replicaId,
      local,
      ops,
    };
    for (const observer of this.observers) observer(snapshot, summary);
  }

  snapshot(): Snapshot {
    return renderEntries(
      this.entries,
      this.fieldStrategies,
      this.defaultStrategy,
    );
  }

  get(field: string): unknown {
    return this.snapshot()[field];
  }

  rebuildFromLog(): Snapshot {
    return replayLog(this.log, {
      strategies: this.fieldStrategies,
      defaultStrategy: this.defaultStrategy,
    });
  }
}
