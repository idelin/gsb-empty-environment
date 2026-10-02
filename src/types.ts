export type NodeId = string;

export interface Ballot {
  round: number;
  nodeId: NodeId;
}

export const ZERO_BALLOT: Ballot = { round: 0, nodeId: '' };

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  return a.nodeId < b.nodeId ? -1 : a.nodeId > b.nodeId ? 1 : 0;
}

export function ballotKey(b: Ballot): string {
  return `${b.round}@${b.nodeId}`;
}

export type Value = { kind: 'noop' } | { kind: 'cmd'; data: string };

export const NOOP: Value = { kind: 'noop' };

export function cmd(data: string): Value {
  return { kind: 'cmd', data };
}

export function valueEquals(a: Value, b: Value): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === 'noop') return true;
  return (a as { kind: 'cmd'; data: string }).data === (b as { kind: 'cmd'; data: string }).data;
}

export function valueToString(v: Value): string {
  return v.kind === 'noop' ? 'noop' : `cmd(${v.data})`;
}

export const GENESIS_HASH = 'GENESIS';

export interface LogEntry {
  slot: number;
  ballot: Ballot;
  value: Value;
  prevHash: string;
  hash: string;
}

export type Message =
  | { type: 'Prepare'; from: NodeId; to?: NodeId; ballot: Ballot }
  | { type: 'Promise'; from: NodeId; to?: NodeId; ballot: Ballot; accepted: LogEntry[] }
  | { type: 'PrepareNack'; from: NodeId; to?: NodeId; ballot: Ballot; promised: Ballot }
  | { type: 'Accept'; from: NodeId; to?: NodeId; entry: LogEntry }
  | { type: 'Accepted'; from: NodeId; to?: NodeId; slot: number; ballot: Ballot }
  | { type: 'AcceptNack'; from: NodeId; to?: NodeId; slot: number; ballot: Ballot; promised: Ballot }
  | { type: 'Commit'; from: NodeId; to?: NodeId; entry: LogEntry }
  | { type: 'SyncRequest'; from: NodeId; to?: NodeId; fromSlot: number }
  | { type: 'SyncResponse'; from: NodeId; to?: NodeId; entries: LogEntry[] };

export type MessageType = Message['type'];
