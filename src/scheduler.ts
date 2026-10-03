import type { DatabaseSystem } from './system.ts';
import type { PageId, TxnId } from './types.ts';

export type TxnOp =
  | { op: 'set'; pageId: PageId; value: number }
  | { op: 'add'; pageId: PageId; delta: number }
  | { op: 'commit' }
  | { op: 'abort' };

export interface TxnScript {
  ops: TxnOp[];
}

export interface ScheduleResult {
  committed: TxnId[];
  aborted: TxnId[];
  txnIds: TxnId[];
}

/**
 * Deterministic cooperative scheduler for concurrency testing. Transactions
 * run one operation per round-robin turn; lock conflicts block the requester
 * until the holder finishes. A no-progress round means deadlock: the wait-for
 * cycle is found and its youngest transaction is aborted (victim selection),
 * guaranteeing progress without lost updates.
 */
export function runSchedule(system: DatabaseSystem, scripts: TxnScript[]): ScheduleResult {
  const txnIds = scripts.map(() => system.begin());
  const positions = scripts.map(() => 0);
  const done = scripts.map(() => false);
  const committed: TxnId[] = [];
  const aborted: TxnId[] = [];

  const abortTxn = (index: number): void => {
    system.abort(txnIds[index] as TxnId);
    done[index] = true;
    aborted.push(txnIds[index] as TxnId);
  };

  for (let guard = 0; ; guard++) {
    if (guard > 100_000) throw new Error('scheduler livelock');
    let progressed = false;
    let remaining = false;
    const waitsFor = new Map<number, TxnId>(); // script index -> holder txnId

    for (let i = 0; i < scripts.length; i++) {
      if (done[i]) continue;
      const script = scripts[i];
      const pos = positions[i] as number;
      const op = script?.ops[pos];
      const txnId = txnIds[i] as TxnId;
      if (!script || !op) {
        system.commit(txnId); // scripts without explicit commit auto-commit
        done[i] = true;
        committed.push(txnId);
        progressed = true;
        continue;
      }
      remaining = true;

      if (op.op === 'commit') {
        system.commit(txnId);
        done[i] = true;
        committed.push(txnId);
        positions[i] = pos + 1;
        progressed = true;
        continue;
      }
      if (op.op === 'abort') {
        abortTxn(i);
        positions[i] = pos + 1;
        progressed = true;
        continue;
      }

      const holder = system.lockManager.holderOf(op.pageId);
      if (holder !== undefined && holder !== txnId) {
        waitsFor.set(i, holder);
        continue;
      }

      if (op.op === 'set') {
        system.update(txnId, op.pageId, op.value);
      } else {
        const current = system.read(txnId, op.pageId);
        system.update(txnId, op.pageId, current + op.delta);
      }
      positions[i] = pos + 1;
      progressed = true;
    }

    if (!remaining) break;
    if (progressed) continue;

    // No progress: a deadlock cycle exists among the waiting transactions.
    const victimIndex = findDeadlockVictim(waitsFor, txnIds);
    if (victimIndex === undefined) {
      throw new Error('scheduler stuck without a detectable deadlock cycle');
    }
    abortTxn(victimIndex);
  }

  return { committed, aborted, txnIds };
}

/** Returns the script index of the youngest txn in a wait-for cycle. */
function findDeadlockVictim(waitsFor: Map<number, TxnId>, txnIds: TxnId[]): number | undefined {
  const indexByTxn = new Map<TxnId, number>();
  txnIds.forEach((txnId, i) => indexByTxn.set(txnId, i));

  for (const start of waitsFor.keys()) {
    const seen = new Set<number>();
    let current: number | undefined = start;
    while (current !== undefined && !seen.has(current)) {
      seen.add(current);
      const holder: TxnId | undefined = waitsFor.get(current);
      current = holder !== undefined ? indexByTxn.get(holder) : undefined;
      if (current !== undefined && waitsFor.has(current) === false) current = undefined;
    }
    if (current !== undefined && seen.has(current)) {
      // Cycle found: collect its members, abort the youngest (largest txnId).
      const cycle: number[] = [];
      let node: number | undefined = current;
      do {
        cycle.push(node as number);
        const holder: TxnId | undefined = waitsFor.get(node as number);
        node = holder !== undefined ? indexByTxn.get(holder) : undefined;
      } while (node !== undefined && node !== current && cycle.length <= waitsFor.size + 1);
      let victim = cycle[0] as number;
      for (const idx of cycle) {
        if ((txnIds[idx] as TxnId) > (txnIds[victim] as TxnId)) victim = idx;
      }
      return victim;
    }
  }
  return undefined;
}
