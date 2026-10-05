import { existsSync } from "node:fs";
import { join } from "node:path";
import { hashKey } from "./axial.ts";
import type { CellKey } from "./axial.ts";
import {
  applyEffect,
  capacityOf,
  cellUsage,
  emptyState,
  expiredAt,
  tenantUsage,
} from "./engine.ts";
import type { Effect, State } from "./engine.ts";
import { Journal } from "./journal.ts";
import { computeLedger } from "./ledger.ts";
import type { Ledger } from "./ledger.ts";
import type { Meta, OpResult } from "./model.ts";
import { COORDINATOR_LOG, partitionLogPath } from "./store.ts";

interface IntentRecord {
  type: "intent";
  txId: number;
  kind: string;
  tick: number;
  requestId: string | null;
  hash: string | null;
  effect: Effect;
  result: OpResult;
}

export interface AuditReport {
  committedTxs: number;
  /** Transactions with an abort record (e.g. partial partition write). */
  abortedTxs: number;
  /** Intents with no decision record at all (crashed transactions). */
  orphanIntents: number;
  /** Grant-time over-allocation proof failures. Must be empty. */
  grantViolations: string[];
  /** Partition-log inconsistencies (torn or misplaced writes). */
  partitionErrors: string[];
  /** Expiry sweeps recorded in committed effects that do not match replay. */
  sweepErrors: string[];
  finalTick: number;
  ledger: Ledger;
  ok: boolean;
}

function partitionOf(state: State, cell: CellKey): number {
  const owned = state.ownership.get(cell);
  if (owned !== undefined) return owned;
  return hashKey(cell) % (state.meta as Meta).numPartitions;
}

/** Cells whose occupancy an effect changes, grouped by owning partition. */
function expectedPartitionEffects(state: State, effect: Effect): Map<number, CellKey[]> {
  const cells = new Set<CellKey>();
  if (effect.expired) {
    for (const id of effect.expired) {
      const lease = state.leases.get(id);
      if (lease) for (const cell of lease.cells) cells.add(cell);
    }
  }
  if (effect.deleteLease !== undefined) {
    const lease = state.leases.get(effect.deleteLease);
    if (lease) for (const cell of lease.cells) cells.add(cell);
  }
  if (effect.upsertLease) {
    const old = state.leases.get(effect.upsertLease.leaseId);
    if (old) for (const cell of old.cells) cells.add(cell);
    for (const cell of effect.upsertLease.cells) cells.add(cell);
  }
  if (effect.setCapacity) cells.add(effect.setCapacity.cell);
  const out = new Map<number, CellKey[]>();
  for (const cell of cells) {
    const partition = partitionOf(state, cell);
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
  return out;
}

/**
 * Replays the coordinator journal from scratch and proves, for every
 * committed transaction, that:
 *   - the recorded expiry sweep matches the replayed state,
 *   - every granted cell had a free slot at grant time,
 *   - every tenant stayed within quota at grant time,
 *   - per-partition effect records exist and match the committed effect
 *     (aborted/orphaned intents may leave partial records behind; those
 *     transactions were never applied, so their capacity was never
 *     released).
 */
export function auditStore(dir: string): AuditReport {
  const grantViolations: string[] = [];
  const partitionErrors: string[] = [];
  const sweepErrors: string[] = [];
  const state = emptyState();
  const pending = new Map<number, IntentRecord>();
  const committed: IntentRecord[] = [];
  let abortedTxs = 0;

  for (const record of Journal.readAll(join(dir, COORDINATOR_LOG))) {
    const rec = record as Record<string, unknown>;
    if (rec.type === "meta") {
      state.meta = rec.meta as Meta;
    } else if (rec.type === "intent") {
      const intent = rec as unknown as IntentRecord;
      pending.set(intent.txId, intent);
    } else if (rec.type === "commit") {
      const intent = pending.get(rec.txId as number);
      if (!intent) {
        partitionErrors.push(`commit for unknown tx ${rec.txId}`);
        continue;
      }
      pending.delete(intent.txId);
      committed.push(intent);
      verifyEffect(state, intent, grantViolations, sweepErrors);
      applyEffect(state, intent.effect);
    } else if (rec.type === "abort") {
      if (pending.delete(rec.txId as number)) abortedTxs++;
    }
  }

  // Cross-check partition logs against committed effects.
  const actual = new Map<number, Map<number, CellKey[]>>(); // txId -> partition -> cells
  const meta = state.meta;
  if (meta) {
    for (let p = 0; p < meta.numPartitions; p++) {
      const path = partitionLogPath(dir, p);
      if (!existsSync(path)) continue;
      for (const record of Journal.readAll(path)) {
        const rec = record as { type: string; txId: number; partition: number; cells: CellKey[] };
        if (rec.type !== "effect") continue;
        let byTx = actual.get(rec.txId);
        if (!byTx) {
          byTx = new Map();
          actual.set(rec.txId, byTx);
        }
        const list = byTx.get(rec.partition) ?? [];
        list.push(...rec.cells);
        byTx.set(rec.partition, list);
      }
    }
  }

  // Re-verify placement of committed effects against pre-effect state.
  const replay = emptyState();
  replay.meta = state.meta;
  const committedIds = new Set<number>();
  for (const intent of committed) {
    committedIds.add(intent.txId);
    const expected = expectedPartitionEffects(replay, intent.effect);
    const got = actual.get(intent.txId) ?? new Map<number, CellKey[]>();
    const norm = (m: Map<number, CellKey[]>) =>
      JSON.stringify(
        [...m.entries()].map(([p, c]) => [p, [...c].sort()] as const).sort(([a], [b]) => a - b),
      );
    if (norm(expected) !== norm(got)) {
      partitionErrors.push(
        `tx ${intent.txId}: partition effects mismatch (expected ${norm(expected)}, got ${norm(got)})`,
      );
    }
    applyEffect(replay, intent.effect);
  }
  // Any effect records whose txId is not in committedIds are partial
  // writes of aborted/crashed transactions: they were never applied, so
  // the capacity they touched was never released.

  const ledger = computeLedger(state);
  const ok =
    grantViolations.length === 0 &&
    partitionErrors.length === 0 &&
    sweepErrors.length === 0;
  return {
    committedTxs: committed.length,
    abortedTxs,
    orphanIntents: pending.size,
    grantViolations,
    partitionErrors,
    sweepErrors,
    finalTick: state.tick,
    ledger,
    ok,
  };
}

function verifyEffect(
  state: State,
  intent: IntentRecord,
  grantViolations: string[],
  sweepErrors: string[],
): void {
  const effect = intent.effect;
  if (effect.tick !== undefined) {
    const expected = expiredAt(state, effect.tick);
    const recorded = effect.expired ?? [];
    if (JSON.stringify(expected) !== JSON.stringify(recorded)) {
      sweepErrors.push(
        `tx ${intent.txId}: recorded sweep [${recorded}] != replayed [${expected}] at tick ${effect.tick}`,
      );
    }
  }
  // Post-sweep view for grant-time checks.
  const view = emptyState();
  view.meta = state.meta;
  view.tick = effect.tick ?? state.tick;
  view.capacity = new Map(state.capacity);
  view.quota = new Map(state.quota);
  view.leases = new Map(state.leases);
  for (const id of effect.expired ?? []) view.leases.delete(id);

  if (effect.upsertLease) {
    const lease = effect.upsertLease;
    const old = view.leases.get(lease.leaseId);
    const oldCells = new Set(old?.cells ?? []);
    const usage = cellUsage(view);
    for (const cell of lease.cells) {
      if (oldCells.has(cell)) continue; // retained, not a new grant
      const used = usage.get(cell) ?? 0;
      const cap = capacityOf(view, cell);
      if (used >= cap) {
        grantViolations.push(
          `tx ${intent.txId}: granted cell ${cell} to ${lease.leaseId} while usage ${used} >= capacity ${cap}`,
        );
      }
    }
    const quota = view.quota.get(lease.tenantId);
    if (quota !== undefined) {
      const projected =
        (tenantUsage(view).get(lease.tenantId) ?? 0) -
        (old?.cells.length ?? 0) +
        lease.cells.length;
      if (projected > quota) {
        grantViolations.push(
          `tx ${intent.txId}: tenant ${lease.tenantId} projected usage ${projected} > quota ${quota}`,
        );
      }
    }
  }
}
