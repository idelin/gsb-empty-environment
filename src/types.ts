/** Ballot (proposal number): totally ordered by (term, nodeId). */
export interface Ballot {
  term: number;
  nodeId: number;
}

export function compareBallots(a: Ballot, b: Ballot): number {
  if (a.term !== b.term) return a.term - b.term;
  return a.nodeId - b.nodeId;
}

export function ballotEquals(a: Ballot, b: Ballot): boolean {
  return a.term === b.term && a.nodeId === b.nodeId;
}

/** Client command applied to the replicated state machine. */
export type Command =
  | { kind: 'set'; key: string; value: string }
  | { kind: 'noop' };

export interface LogEntry {
  slot: number;
  ballot: Ballot;
  command: Command;
}

export type Message =
  | { type: 'Prepare'; from: number; to: number; ballot: Ballot; fromSlot: number }
  | { type: 'Promise'; from: number; to: number; ballot: Ballot; accepted: LogEntry[] }
  | { type: 'Accept'; from: number; to: number; entry: LogEntry }
  | { type: 'Accepted'; from: number; to: number; ballot: Ballot; slot: number }
  | { type: 'Rejected'; from: number; to: number; ballot: Ballot; promised: Ballot }
  | { type: 'Commit'; from: number; to: number; term: number; commitIndex: number; entries: LogEntry[] };
