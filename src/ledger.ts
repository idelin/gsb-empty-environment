import type {
  IssueConfig,
  RejectedVote,
  TallyResult,
  Vote,
} from './types.ts';
import {
  compareVoteOrder,
  sameVote,
  voteKey,
} from './types.ts';
import type { EligibilityTable } from './eligibility.ts';

export class VoteConflictError extends Error {
  readonly key: string;

  constructor(key: string) {
    super(
      `conflicting contents for the same full vote version (${key}); ` +
        'refusing to pick one',
    );
    this.name = 'VoteConflictError';
    this.key = key;
  }
}

function validateVote(vote: Vote): void {
  if (!Number.isSafeInteger(vote.version) || vote.version < 0) {
    throw new RangeError(`invalid version in vote: ${JSON.stringify(vote)}`);
  }
  if (vote.issueId === '' || vote.voterId === '' || vote.replicaId === '') {
    throw new RangeError(`empty identifier in vote: ${JSON.stringify(vote)}`);
  }
  if (vote.payload.type === 'choice' && vote.payload.choice === '') {
    throw new RangeError(`empty candidate in vote: ${JSON.stringify(vote)}`);
  }
}

/**
 * Offline-mergeable vote ledger. Votes are stored by their full version
 * identity (issue, voter, version, replica); the effective vote of a voter
 * is the maximum under the (version, replicaId) order. Withdrawals are
 * ordinary versions, so an older ballot can never resurrect a withdrawn one.
 */
export class VoteLedger {
  private readonly votes = new Map<string, Vote>();

  addVote(vote: Vote): void {
    validateVote(vote);
    const key = voteKey(vote);
    const existing = this.votes.get(key);
    if (existing !== undefined) {
      if (!sameVote(existing, vote)) {
        throw new VoteConflictError(key);
      }
      return; // duplicate import is a no-op
    }
    this.votes.set(key, vote);
  }

  addVotes(votes: Iterable<Vote>): void {
    for (const vote of votes) {
      this.addVote(vote);
    }
  }

  merge(other: VoteLedger): void {
    this.addVotes(other.allVotes());
  }

  get size(): number {
    return this.votes.size;
  }

  allVotes(): Vote[] {
    return [...this.votes.values()];
  }

  /** Effective (latest) vote per voter for one issue. */
  effectiveVotes(issueId: string): Map<string, Vote> {
    const effective = new Map<string, Vote>();
    for (const vote of this.votes.values()) {
      if (vote.issueId !== issueId) continue;
      const current = effective.get(vote.voterId);
      if (current === undefined || compareVoteOrder(vote, current) > 0) {
        effective.set(vote.voterId, vote);
      }
    }
    return effective;
  }

  tally(issue: IssueConfig, eligibility: EligibilityTable): TallyResult {
    const weights = eligibility.snapshot(issue.eligibilityVersion);
    const candidateWeights: Record<string, bigint> = {};
    for (const candidate of issue.candidates) {
      candidateWeights[candidate] = 0n;
    }
    let participatedWeight = 0n;
    const rejected: RejectedVote[] = [];

    for (const vote of this.effectiveVotes(issue.issueId).values()) {
      if (vote.payload.type === 'withdraw') continue;
      const weight = weights.get(vote.voterId);
      if (weight === undefined) {
        rejected.push({ vote, reason: 'not_eligible' });
        continue;
      }
      if (vote.payload.type === 'abstain') {
        participatedWeight += weight;
        continue;
      }
      const candidate = vote.payload.choice;
      if (!(candidate in candidateWeights)) {
        rejected.push({ vote, reason: 'unknown_candidate' });
        continue;
      }
      candidateWeights[candidate] += weight;
      participatedWeight += weight;
    }

    return resolveTally(issue, candidateWeights, participatedWeight, rejected);
  }
}

export function resolveTally(
  issue: IssueConfig,
  candidateWeights: Record<string, bigint>,
  participatedWeight: bigint,
  rejected: RejectedVote[],
): TallyResult {
  let winner: string | null = null;
  let topWeight = 0n;
  for (const candidate of issue.candidates) {
    const weight = candidateWeights[candidate] ?? 0n;
    if (
      weight > topWeight ||
      (weight === topWeight &&
        weight > 0n &&
        (winner === null || candidate < winner))
    ) {
      topWeight = weight;
      winner = candidate;
    }
  }

  let resolved = true;
  let reason: TallyResult['reason'] = 'ok';
  if (participatedWeight < issue.participationThreshold) {
    resolved = false;
    reason = 'threshold_not_met';
    winner = null;
  } else if (winner === null) {
    resolved = false;
    reason = 'no_candidate_weight';
  }

  return {
    issueId: issue.issueId,
    eligibilityVersion: issue.eligibilityVersion,
    candidateWeights,
    participatedWeight,
    participationThreshold: issue.participationThreshold,
    resolved,
    winner,
    reason,
    rejected,
  };
}
