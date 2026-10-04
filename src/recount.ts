import { MissingEligibilityVersionError, type EligibilityTable } from './eligibility.ts';
import { choiceEquals, compareVoteOrder, validateVote, VoteConflictError } from './ledger.ts';
import type { Issue, RejectedVote, TallyResult, VoteRecord } from './types.ts';

function reduceToEffective(rawVotes: readonly VoteRecord[]): VoteRecord[] {
  const sorted = [...rawVotes].sort((a, b) => {
    if (a.issueId !== b.issueId) return a.issueId < b.issueId ? -1 : 1;
    if (a.voterId !== b.voterId) return a.voterId < b.voterId ? -1 : 1;
    return compareVoteOrder(a, b);
  });
  const effective: VoteRecord[] = [];
  for (const vote of sorted) {
    validateVote(vote);
    const last = effective[effective.length - 1];
    if (last !== undefined && last.issueId === vote.issueId && last.voterId === vote.voterId) {
      if (compareVoteOrder(last, vote) === 0) {
        if (!choiceEquals(last.choice, vote.choice)) {
          throw new VoteConflictError(last, vote);
        }
        continue;
      }
      effective[effective.length - 1] = vote;
    } else {
      effective.push(vote);
    }
  }
  return effective;
}

export function recount(
  rawVotes: readonly VoteRecord[],
  issue: Issue,
  eligibility: EligibilityTable,
): TallyResult {
  const snapshot = eligibility.getVersion(issue.eligibilityVersion);
  if (snapshot === undefined) {
    throw new MissingEligibilityVersionError(issue.eligibilityVersion);
  }
  const effective = reduceToEffective(rawVotes);
  const candidateWeights = new Map<string, bigint>();
  const rejected: RejectedVote[] = [];
  let participatedWeight = 0n;
  for (const vote of effective) {
    if (vote.issueId !== issue.issueId) continue;
    const weight = snapshot.get(vote.voterId);
    if (weight === undefined) {
      rejected.push({ vote, reason: 'ineligible' });
      continue;
    }
    if (vote.choice.kind === 'retract') continue;
    participatedWeight += weight;
    if (vote.choice.kind === 'candidate') {
      const candidateId = vote.choice.candidateId;
      candidateWeights.set(candidateId, (candidateWeights.get(candidateId) ?? 0n) + weight);
    }
  }
  const sortedWeights = new Map(
    [...candidateWeights.entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)),
  );
  const decided = participatedWeight >= issue.participationThreshold;
  let winner: string | null = null;
  if (decided) {
    let best: bigint | null = null;
    for (const [candidateId, weight] of sortedWeights) {
      if (best === null || weight > best || (weight === best && winner !== null && candidateId < winner)) {
        winner = candidateId;
        best = weight;
      }
    }
  }
  return {
    issueId: issue.issueId,
    eligibilityVersion: issue.eligibilityVersion,
    candidateWeights: sortedWeights,
    participatedWeight,
    rejected,
    decided,
    winner,
  };
}

export function tallyResultsEqual(a: TallyResult, b: TallyResult): boolean {
  if (
    a.issueId !== b.issueId ||
    a.eligibilityVersion !== b.eligibilityVersion ||
    a.participatedWeight !== b.participatedWeight ||
    a.decided !== b.decided ||
    a.winner !== b.winner ||
    a.candidateWeights.size !== b.candidateWeights.size ||
    a.rejected.length !== b.rejected.length
  ) {
    return false;
  }
  for (const [candidateId, weight] of a.candidateWeights) {
    if (b.candidateWeights.get(candidateId) !== weight) return false;
  }
  for (let i = 0; i < a.rejected.length; i += 1) {
    const ra = a.rejected[i];
    const rb = b.rejected[i];
    if (ra.reason !== rb.reason) return false;
    if (
      ra.vote.issueId !== rb.vote.issueId ||
      ra.vote.voterId !== rb.vote.voterId ||
      ra.vote.version !== rb.vote.version ||
      ra.vote.replicaId !== rb.vote.replicaId ||
      !choiceEquals(ra.vote.choice, rb.vote.choice)
    ) {
      return false;
    }
  }
  return true;
}
