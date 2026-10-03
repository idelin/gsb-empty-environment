/**
 * Ballot number: totally ordered (round, nodeId) pair.
 * The node id tie-breaker guarantees that two distinct nodes can never
 * campaign with the same ballot, which is what makes election safety
 * reducible to a simple majority-intersection argument.
 */
export interface Ballot {
  readonly round: number;
  readonly node: string;
}

export const ZERO_BALLOT: Ballot = Object.freeze({ round: 0, node: '' });

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round < b.round ? -1 : 1;
  if (a.node === b.node) return 0;
  return a.node < b.node ? -1 : 1;
}

export function ballotEquals(a: Ballot, b: Ballot): boolean {
  return a.round === b.round && a.node === b.node;
}

export function formatBallot(b: Ballot): string {
  return `${b.round}@${b.node}`;
}
