export interface Ballot {
  round: number;
  leaderId: string;
}

export const ZERO_BALLOT: Ballot = { round: 0, leaderId: "" };

export function compareBallot(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  return a.leaderId < b.leaderId ? -1 : a.leaderId > b.leaderId ? 1 : 0;
}

export function ballotToString(b: Ballot): string {
  return `(${b.round},${b.leaderId})`;
}
