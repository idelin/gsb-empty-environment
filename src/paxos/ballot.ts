/** Paxos 选票（提案编号）：(round, node) 字典序，全局全序。 */
export interface Ballot {
  round: number;
  node: string;
}

export const ZERO_BALLOT: Ballot = { round: 0, node: '' };

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  return a.node < b.node ? -1 : a.node > b.node ? 1 : 0;
}

export function ballotEquals(a: Ballot, b: Ballot): boolean {
  return a.round === b.round && a.node === b.node;
}

export function formatBallot(b: Ballot): string {
  return `(${b.round},${b.node})`;
}
