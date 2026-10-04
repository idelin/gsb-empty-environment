import { MissingEligibilityVersionError, type EligibilityTable } from './eligibility.ts';
import type { VoteLedger } from './ledger.ts';
import type { Issue, RejectedVote, TallyResult, VoteRecord } from './types.ts';

export function pickWinner(candidateWeights: ReadonlyMap<string, bigint>): string | null {
  let winner: string | null = null;
  let best: bigint | null = null;
  for (const [candidateId, weight] of candidateWeights) {
    if (best === null || weight > best || (weight === best && winner !== null && candidateId < winner)) {
      winner = candidateId;
      best = weight;
    }
  }
  return winner;
}

export function tallyVotes(
  votes: Iterable<VoteRecord>,
  issue: Issue,
  eligibility: EligibilityTable,
): TallyResult {
  const snapshot = eligibility.getVersion(issue.eligibilityVersion);
  if (snapshot === undefined) {
    throw new MissingEligibilityVersionError(issue.eligibilityVersion);
  }
  const candidateWeights = new Map<string, bigint>();
  const rejected: RejectedVote[] = [];
  let participatedWeight = 0n;
  for (const vote of votes) {
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
  return {
    issueId: issue.issueId,
    eligibilityVersion: issue.eligibilityVersion,
    candidateWeights: sortedWeights,
    participatedWeight,
    rejected,
    decided,
    winner: decided ? pickWinner(sortedWeights) : null,
  };
}

export function tallyIssue(
  ledger: VoteLedger,
  issue: Issue,
  eligibility: EligibilityTable,
): TallyResult {
  return tallyVotes(ledger.effectiveVotes(issue.issueId), issue, eligibility);
}
