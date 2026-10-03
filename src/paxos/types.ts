/** Ballot (proposal number): totally ordered by (term, nodeId). */
export interface Ballot {
  term: number;
  nodeId: string;
}

/** Sentinel value used by a new leader to fill log holes during repair. */
export const NOOP_VALUE = '__noop__';

export interface LogEntry {
  slot: number;
  ballot: Ballot;
  value: string;
}

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.term !== b.term) return a.term - b.term;
  return a.nodeId < b.nodeId ? -1 : a.nodeId > b.nodeId ? 1 : 0;
}

export function ballotEq(a: Ballot | null, b: Ballot | null): boolean {
  if (a === null || b === null) return a === b;
  return a.term === b.term && a.nodeId === b.nodeId;
}

export function ballotToString(b: Ballot): string {
  return `(${b.term},${b.nodeId})`;
}

export type Message =
  | { type: 'Prepare'; from: string; to: string; ballot: Ballot }
  | {
      type: 'Promise';
      from: string;
      to: string;
      ballot: Ballot;
      /**
       * Full local log snapshot (committed prefix + uncommitted tail) so the
       * new leader can backfill decided slots and repair undecided ones.
       */
      entries: LogEntry[];
      commitIndex: number;
    }
  | { type: 'Accept'; from: string; to: string; ballot: Ballot; entry: LogEntry }
  | { type: 'Accepted'; from: string; to: string; ballot: Ballot; slot: number }
  | {
      type: 'Commit';
      from: string;
      to: string;
      commitIndex: number;
      /** All committed entries (slots <= commitIndex) so followers can fill holes. */
      entries: LogEntry[];
    };
