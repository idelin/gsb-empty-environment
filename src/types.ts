/** Comparable Paxos ballot (proposal number). nodeId breaks ties. */
export interface Ballot {
  round: number;
  nodeId: number;
}

export const ZERO_BALLOT: Ballot = { round: 0, nodeId: -1 };

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  return a.nodeId - b.nodeId;
}

export const ballotGt = (a: Ballot, b: Ballot): boolean => compareBallot(a, b) > 0;
export const ballotGe = (a: Ballot, b: Ballot): boolean => compareBallot(a, b) >= 0;
export const ballotEq = (a: Ballot, b: Ballot): boolean => compareBallot(a, b) === 0;
export const fmtBallot = (b: Ballot): string => `${b.round}.${b.nodeId}`;

/** Event sourced saga transition, replicated through the consensus log. */
export interface SagaEvent {
  sagaId: string;
  type:
    | 'step-executed'
    | 'step-failed'
    | 'step-compensated'
    | 'saga-completed'
    | 'saga-compensated'
    | 'saga-failed';
  step?: string;
  error?: string;
}

export type Command =
  | { kind: 'noop' }
  | { kind: 'client'; id: string; data: string }
  | { kind: 'saga'; event: SagaEvent };

/** A log entry carries the ballot at which it was (re-)proposed. */
export interface LogEntry {
  ballot: Ballot;
  value: Command;
}

export function commandLabel(c: Command): string {
  switch (c.kind) {
    case 'noop':
      return 'noop';
    case 'client':
      return c.data;
    case 'saga':
      return `saga:${c.event.sagaId}:${c.event.type}${c.event.step ? ':' + c.event.step : ''}`;
  }
}
