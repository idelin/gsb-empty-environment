/**
 * Shared types for the Multi-Paxos log engine.
 * LogEntry is a flat, pool-friendly structure: no nested objects, so
 * reset() is O(1) and instances can be recycled without GC pressure.
 */

export interface Ballot {
  round: number;
  nodeId: number;
}

/** Total order on ballots: round first, then nodeId as tie-breaker. */
export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  return a.nodeId - b.nodeId;
}

export function ballotToString(b: Ballot): string {
  return `${b.round}.${b.nodeId}`;
}

/**
 * A single replicated log slot. Flat fields only — recycled via ObjectPool.
 * `committed` mirrors the store-level commit index for fast per-entry checks.
 */
export interface LogEntry {
  slot: number;
  ballotRound: number;
  ballotNode: number;
  value: string;
  committed: boolean;
}

export function resetLogEntry(e: LogEntry): void {
  e.slot = -1;
  e.ballotRound = -1;
  e.ballotNode = -1;
  e.value = "";
  e.committed = false;
}

/** Sentinel value used by a new leader to fill holes that no majority ever accepted. */
export const NOOP_VALUE = "__noop__";
