/** Multi-Paxos wire types. */

/** A ballot is a lexicographic (round, nodeId) pair — totally ordered. */
export interface Ballot {
  n: number;
  node: number;
}

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.n !== b.n) return a.n - b.n;
  return a.node - b.node;
}

export function ballotKey(b: Ballot): string {
  return `${b.n}:${b.node}`;
}

/** A log entry. `value === NOOP` is a filler used to repair holes. */
export interface LogEntry {
  ballot: Ballot;
  value: unknown;
}

export const NOOP: unique symbol = Symbol('noop');
export type NoOp = typeof NOOP;

export type Message =
  | { type: 'prepare'; from: number; ballot: Ballot }
  | {
      type: 'promise';
      from: number;
      ballot: Ballot;
      accepted: Array<[number, LogEntry]>;
      commitUpTo: number;
    }
  | { type: 'prepare-rejected'; from: number; ballot: Ballot; promised: Ballot }
  | { type: 'accept'; from: number; slot: number; entry: LogEntry }
  | { type: 'accepted'; from: number; slot: number; ballot: Ballot }
  | {
      type: 'commit';
      from: number;
      ballot: Ballot;
      commitUpTo: number;
      // Chosen values for every slot <= commitUpTo. Carrying them makes the
      // commit self-sufficient: a follower holding a dirty (never-chosen)
      // entry replaces it with the chosen value instead of applying garbage.
      entries: Array<[number, LogEntry]>;
    };

export type MessageType = Message['type'];
