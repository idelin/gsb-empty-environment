export type VotePayload =
  | { readonly type: 'choice'; readonly choice: string }
  | { readonly type: 'abstain' }
  | { readonly type: 'withdraw' };

export interface Vote {
  readonly issueId: string;
  readonly voterId: string;
  readonly version: number;
  readonly replicaId: string;
  readonly payload: VotePayload;
}

export interface IssueConfig {
  readonly issueId: string;
  readonly eligibilityVersion: number;
  readonly candidates: readonly string[];
  readonly participationThreshold: bigint;
}

export type RejectReason = 'not_eligible' | 'unknown_candidate';

export interface RejectedVote {
  readonly vote: Vote;
  readonly reason: RejectReason;
}

export type TallyReason = 'ok' | 'threshold_not_met' | 'no_candidate_weight';

export interface TallyResult {
  readonly issueId: string;
  readonly eligibilityVersion: number;
  readonly candidateWeights: Readonly<Record<string, bigint>>;
  readonly participatedWeight: bigint;
  readonly participationThreshold: bigint;
  readonly resolved: boolean;
  readonly winner: string | null;
  readonly reason: TallyReason;
  readonly rejected: readonly RejectedVote[];
}

export function voteKey(vote: Vote): string {
  return [
    vote.issueId,
    vote.voterId,
    String(vote.version),
    vote.replicaId,
  ].join('');
}

export function sameVote(a: Vote, b: Vote): boolean {
  return (
    a.issueId === b.issueId &&
    a.voterId === b.voterId &&
    a.version === b.version &&
    a.replicaId === b.replicaId &&
    samePayload(a.payload, b.payload)
  );
}

export function samePayload(a: VotePayload, b: VotePayload): boolean {
  if (a.type !== b.type) return false;
  if (a.type === 'choice' && b.type === 'choice') {
    return a.choice === b.choice;
  }
  return true;
}

/**
 * Total order over vote versions: higher logical version wins;
 * ties across replicas are broken by replica id (lexicographic).
 */
export function compareVoteOrder(a: Vote, b: Vote): number {
  if (a.version !== b.version) return a.version - b.version;
  if (a.replicaId < b.replicaId) return -1;
  if (a.replicaId > b.replicaId) return 1;
  return 0;
}
