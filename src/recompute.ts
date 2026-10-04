import type {
  IssueConfig,
  RejectedVote,
  TallyResult,
  Vote,
} from './types.ts';
import type { EligibilityTable } from './eligibility.ts';
import { resolveTally } from './ledger.ts';

/**
 * Independent recomputer: reduces a raw vote collection straight from
 * scratch, picking the maximum (version, replicaId) vote per voter and
 * counting weights without touching any ledger state. Used to cross-check
 * the ledger's incremental merge results.
 */
export function recomputeTally(
  rawVotes: Iterable<Vote>,
  issue: IssueConfig,
  eligibility: EligibilityTable,
): TallyResult {
  const latestByVoter = new Map<string, Vote>();
  for (const vote of rawVotes) {
    if (vote.issueId !== issue.issueId) continue;
    const current = latestByVoter.get(vote.voterId);
    if (
      current === undefined ||
      vote.version > current.version ||
      (vote.version === current.version && vote.replicaId > current.replicaId)
    ) {
      latestByVoter.set(vote.voterId, vote);
    }
  }

  const weights = eligibility.snapshot(issue.eligibilityVersion);
  const candidateWeights: Record<string, bigint> = {};
  for (const candidate of issue.candidates) {
    candidateWeights[candidate] = 0n;
  }
  let participatedWeight = 0n;
  const rejected: RejectedVote[] = [];

  for (const [voterId, vote] of latestByVoter) {
    const { payload } = vote;
    if (payload.type === 'withdraw') continue;
    const weight = weights.get(voterId);
    if (weight === undefined) {
      rejected.push({ vote, reason: 'not_eligible' });
      continue;
    }
    if (payload.type === 'abstain') {
      participatedWeight += weight;
    } else if (payload.type === 'choice') {
      if (!(payload.choice in candidateWeights)) {
        rejected.push({ vote, reason: 'unknown_candidate' });
        continue;
      }
      candidateWeights[payload.choice] += weight;
      participatedWeight += weight;
    }
  }

  return resolveTally(issue, candidateWeights, participatedWeight, rejected);
}

/** Structural equality check used to verify ledger against recomputation. */
export function sameTally(a: TallyResult, b: TallyResult): boolean {
  if (
    a.issueId !== b.issueId ||
    a.eligibilityVersion !== b.eligibilityVersion ||
    a.participatedWeight !== b.participatedWeight ||
    a.participationThreshold !== b.participationThreshold ||
    a.resolved !== b.resolved ||
    a.winner !== b.winner ||
    a.reason !== b.reason
  ) {
    return false;
  }
  const aKeys = Object.keys(a.candidateWeights);
  const bKeys = Object.keys(b.candidateWeights);
  if (aKeys.length !== bKeys.length) return false;
  for (const key of aKeys) {
    if (a.candidateWeights[key] !== b.candidateWeights[key]) return false;
  }
  if (a.rejected.length !== b.rejected.length) return false;
  const rejectedKey = (entries: readonly RejectedVote[]): string[] =>
    entries
      .map(
        (entry) =>
          `${entry.reason}:${entry.vote.voterId}:${entry.vote.version}:` +
          `${entry.vote.replicaId}`,
      )
      .sort();
  const aRejected = rejectedKey(a.rejected);
  const bRejected = rejectedKey(b.rejected);
  return aRejected.every((value, index) => value === bRejected[index]);
}
