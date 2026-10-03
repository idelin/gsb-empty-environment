/**
 * Core protocol types for Multi-Paxos.
 *
 * A Ballot is a totally ordered leadership ticket: (round, nodeId).
 * Total order: compare `round` first, then `nodeId` as tie-breaker, so any two
 * ballots are comparable and every node can mint a unique higher ballot.
 */
export interface Ballot {
  round: number;
  nodeId: number;
}

export const ZERO_BALLOT: Ballot = { round: 0, nodeId: -1 };

/** Value written into a log slot to fill a hole (chosen, but carries no command). */
export const NOOP_VALUE = '∅(noop)';

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  return a.nodeId - b.nodeId;
}

export function ballotEq(a: Ballot, b: Ballot): boolean {
  return a.round === b.round && a.nodeId === b.nodeId;
}

export function ballotKey(b: Ballot): string {
  return `${b.round}@${b.nodeId}`;
}

/** An accepted log entry: the value plus the ballot under which it was accepted. */
export interface LogEntry {
  ballot: Ballot;
  value: string;
}

/** Phase 1a: a candidate asks acceptors to promise not to accept lower ballots. */
export interface PrepareMsg {
  type: 'Prepare';
  from: number;
  to: number;
  ballot: Ballot;
}

/**
 * Phase 1b: an acceptor promises, and reports every entry it has accepted so
 * the new leader can repair the log. `ok: false` is a nack carrying the
 * acceptor's current `promised` ballot so the candidate can step down.
 */
export interface PromiseMsg {
  type: 'Promise';
  from: number;
  to: number;
  ballot: Ballot;
  ok: boolean;
  entries: Array<[number, LogEntry]>;
  commitIndex: number;
  promised?: Ballot;
}

/** Phase 2a: the leader asks acceptors to accept `value` at `slot`. */
export interface AcceptMsg {
  type: 'Accept';
  from: number;
  to: number;
  ballot: Ballot;
  slot: number;
  value: string;
}

/** Phase 2b: an acceptor acknowledges (or rejects) an Accept. */
export interface AcceptedMsg {
  type: 'Accepted';
  from: number;
  to: number;
  ballot: Ballot;
  slot: number;
  ok: boolean;
  promised?: Ballot;
}

/** Leader notification: entries up to `commitIndex` are committed. */
export interface CommitMsg {
  type: 'Commit';
  from: number;
  to: number;
  ballot: Ballot;
  commitIndex: number;
}

export type Message = PrepareMsg | PromiseMsg | AcceptMsg | AcceptedMsg | CommitMsg;
