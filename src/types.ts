export type NodeId = string;

/** Lexicographically ordered ballot: (term, nodeId). */
export interface Ballot {
  term: number;
  nodeId: NodeId;
}

export function compareBallots(a: Ballot, b: Ballot): number {
  if (a.term !== b.term) return a.term - b.term;
  return a.nodeId < b.nodeId ? -1 : a.nodeId > b.nodeId ? 1 : 0;
}

export function ballotGt(a: Ballot, b: Ballot | null): boolean {
  return b === null || compareBallots(a, b) > 0;
}

export function ballotGte(a: Ballot, b: Ballot | null): boolean {
  return b === null || compareBallots(a, b) >= 0;
}

/** A single log entry. parentSlot links to the previous slot (-1 = genesis). */
export interface LogEntry {
  slot: number;
  ballot: Ballot;
  value: string;
  parentSlot: number;
}

export const NOOP_VALUE = '__noop__';
export const GENESIS_PARENT = -1;

export interface SlotRecord {
  entry: LogEntry;
  /** Majority-accepted (leader) or commit-noticed (follower); not yet
   *  part of the contiguous committed prefix. */
  chosen: boolean;
  committed: boolean;
}

export type Message =
  | { kind: 'prepare'; from: NodeId; ballot: Ballot }
  | { kind: 'promise'; from: NodeId; ballot: Ballot; accepted: LogEntry[] }
  | { kind: 'nack'; from: NodeId; ballot: Ballot; promised: Ballot | null }
  | { kind: 'accept'; from: NodeId; ballot: Ballot; entry: LogEntry }
  | { kind: 'accepted'; from: NodeId; ballot: Ballot; slot: number }
  | { kind: 'commit'; from: NodeId; entry: LogEntry }
  | { kind: 'heartbeat'; from: NodeId; ballot: Ballot; commitIndex: number }
  | { kind: 'sync-request'; from: NodeId; sinceCommitIndex: number };

export type Role = 'follower' | 'leader';

export type ClusterEvent =
  | { type: 'leader'; term: number; nodeId: NodeId }
  | { type: 'commit'; nodeId: NodeId; slot: number; value: string }
  | { type: 'stepdown'; nodeId: NodeId; term: number };
