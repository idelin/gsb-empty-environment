export type Choice =
  | { readonly kind: 'candidate'; readonly candidateId: string }
  | { readonly kind: 'abstain' }
  | { readonly kind: 'retract' };

export interface VoteRecord {
  readonly issueId: string;
  readonly voterId: string;
  readonly version: number;
  readonly replicaId: string;
  readonly choice: Choice;
}

export interface Issue {
  readonly issueId: string;
  readonly eligibilityVersion: number;
  readonly participationThreshold: bigint;
}

export interface RejectedVote {
  readonly vote: VoteRecord;
  readonly reason: 'ineligible';
}

export interface TallyResult {
  readonly issueId: string;
  readonly eligibilityVersion: number;
  readonly candidateWeights: ReadonlyMap<string, bigint>;
  readonly participatedWeight: bigint;
  readonly rejected: readonly RejectedVote[];
  readonly decided: boolean;
  readonly winner: string | null;
}
