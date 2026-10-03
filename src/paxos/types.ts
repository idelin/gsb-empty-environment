import type { LogEntry } from '../codec/logEntry.ts';

export interface Ballot {
  term: number;
  nodeId: number;
}

export const ZERO_BALLOT: Ballot = { term: 0, nodeId: 0 };

export function compareBallot(a: Ballot, b: Ballot): number {
  return a.term - b.term || a.nodeId - b.nodeId;
}

export function sameBallot(a: Ballot, b: Ballot): boolean {
  return a.term === b.term && a.nodeId === b.nodeId;
}

export function ballotKey(b: Ballot): string {
  return `${b.term}:${b.nodeId}`;
}

export const MSG_PREPARE = 'prepare';
export const MSG_ACCEPT = 'accept';
export const MSG_COMMIT = 'commit';
export type MessageType = typeof MSG_PREPARE | typeof MSG_ACCEPT | typeof MSG_COMMIT;

export interface PrepareReq {
  ballot: Ballot;
}

export interface PrepareRes {
  ok: boolean;
  promised: Ballot;
  /** Entries the acceptor has already accepted (for leader repair). */
  accepted: LogEntry[];
}

export interface AcceptReq {
  ballot: Ballot;
  entry: LogEntry;
  /** Ballot of the leader's entry at index-1 (log matching, 0:0 for slot 1). */
  prevBallot: Ballot;
}

export interface AcceptRes {
  ok: boolean;
  promised: Ballot;
  /** Set when the follower's log diverges: leader must backfill from here. */
  needSyncFrom?: number;
}

export interface CommitNotice {
  commitIndex: number;
}

export class NotLeaderError extends Error {
  constructor(message = 'lost leadership: a higher ballot was promised') {
    super(message);
    this.name = 'NotLeaderError';
  }
}

export class NoMajorityError extends Error {
  constructor(message = 'cannot reach a majority of acceptors') {
    super(message);
    this.name = 'NoMajorityError';
  }
}
