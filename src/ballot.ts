import type { Ballot } from './types.ts';

/** Total order on ballots: compare rounds, then node ids. */
export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  if (a.node < b.node) return -1;
  if (a.node > b.node) return 1;
  return 0;
}

export function ballotEq(a: Ballot, b: Ballot): boolean {
  return a.round === b.round && a.node === b.node;
}

export function ballotToString(b: Ballot): string {
  return `${b.round}@${b.node}`;
}
