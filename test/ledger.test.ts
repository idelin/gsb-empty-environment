import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import type { IssueConfig, Vote, VotePayload } from '../src/types.ts';
import {
  EligibilityTable,
  EligibilityConflictError,
  EligibilityVersionNotFoundError,
} from '../src/eligibility.ts';
import { VoteLedger, VoteConflictError } from '../src/ledger.ts';
import { recomputeTally, sameTally } from '../src/recompute.ts';

function vote(
  voterId: string,
  version: number,
  payload: VotePayload,
  replicaId = 'r1',
  issueId = 'issue-1',
): Vote {
  return { issueId, voterId, version, replicaId, payload };
}

function choice(candidate: string): VotePayload {
  return { type: 'choice', choice: candidate };
}

const ABSTAIN: VotePayload = { type: 'abstain' };
const WITHDRAW: VotePayload = { type: 'withdraw' };

function makeIssue(overrides: Partial<IssueConfig> = {}): IssueConfig {
  return {
    issueId: 'issue-1',
    eligibilityVersion: 1,
    candidates: ['alice', 'bob'],
    participationThreshold: 1n,
    ...overrides,
  };
}

function makeEligibility(
  entries: ReadonlyArray<readonly [string, bigint]>,
  version = 1,
): EligibilityTable {
  const table = new EligibilityTable();
  table.publishVersion(version, entries);
  return table;
}

describe('ledger tally', () => {
  it('counts weighted votes and picks the winner', () => {
    const ledger = new VoteLedger();
    ledger.addVotes([
      vote('v1', 1, choice('alice')),
      vote('v2', 1, choice('alice')),
      vote('v3', 1, choice('bob')),
    ]);
    const eligibility = makeEligibility([
      ['v1', 3n],
      ['v2', 2n],
      ['v3', 4n],
    ]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.candidateWeights['alice'], 5n);
    assert.equal(result.candidateWeights['bob'], 4n);
    assert.equal(result.participatedWeight, 9n);
    assert.equal(result.resolved, true);
    assert.equal(result.winner, 'alice');
    assert.deepEqual(result.rejected, []);
  });

  it('abstain counts toward participation but not candidates', () => {
    const ledger = new VoteLedger();
    ledger.addVotes([
      vote('v1', 1, choice('alice')),
      vote('v2', 1, ABSTAIN),
    ]);
    const eligibility = makeEligibility([
      ['v1', 5n],
      ['v2', 7n],
    ]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.candidateWeights['alice'], 5n);
    assert.equal(result.candidateWeights['bob'], 0n);
    assert.equal(result.participatedWeight, 12n);
    assert.equal(result.winner, 'alice');
  });

  it('higher version replaces the older ballot of the same voter', () => {
    const ledger = new VoteLedger();
    ledger.addVotes([
      vote('v1', 1, choice('alice')),
      vote('v1', 2, choice('bob')),
    ]);
    const eligibility = makeEligibility([['v1', 10n]]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.candidateWeights['alice'], 0n);
    assert.equal(result.candidateWeights['bob'], 10n);
    assert.equal(result.winner, 'bob');
  });

  it('a late withdrawal wins and cannot be resurrected by old ballots', () => {
    const ledger = new VoteLedger();
    ledger.addVote(vote('v1', 1, choice('alice')));
    const eligibility = makeEligibility([['v1', 10n]]);

    const before = ledger.tally(makeIssue(), eligibility);
    assert.equal(before.candidateWeights['alice'], 10n);

    // Withdrawal arrives late, after the first tally.
    ledger.addVote(vote('v1', 2, WITHDRAW));
    // Stale replicas re-deliver the old ballot afterwards.
    ledger.addVote(vote('v1', 1, choice('alice')));

    const after = ledger.tally(makeIssue(), eligibility);
    assert.equal(after.candidateWeights['alice'], 0n);
    assert.equal(after.participatedWeight, 0n);
    assert.equal(after.resolved, false);
    assert.equal(after.reason, 'threshold_not_met');
  });

  it('a later re-vote after a withdrawal is honoured', () => {
    const ledger = new VoteLedger();
    ledger.addVotes([
      vote('v1', 1, choice('alice')),
      vote('v1', 2, WITHDRAW),
      vote('v1', 3, choice('bob')),
    ]);
    const eligibility = makeEligibility([['v1', 10n]]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.candidateWeights['bob'], 10n);
    assert.equal(result.winner, 'bob');
  });

  it('breaks cross-replica ties at the same version by replica id', () => {
    const ledger = new VoteLedger();
    ledger.addVotes([
      vote('v1', 5, choice('alice'), 'replica-a'),
      vote('v1', 5, choice('bob'), 'replica-b'),
    ]);
    const eligibility = makeEligibility([['v1', 4n]]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.candidateWeights['bob'], 4n);
    assert.equal(result.candidateWeights['alice'], 0n);
    assert.equal(result.winner, 'bob');
  });

  it('keeps ineligible votes as rejected records', () => {
    const ledger = new VoteLedger();
    ledger.addVotes([
      vote('v1', 1, choice('alice')),
      vote('ghost', 1, choice('bob')),
    ]);
    const eligibility = makeEligibility([['v1', 5n]]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.candidateWeights['bob'], 0n);
    assert.equal(result.participatedWeight, 5n);
    assert.equal(result.rejected.length, 1);
    assert.equal(result.rejected[0]?.reason, 'not_eligible');
    assert.equal(result.rejected[0]?.vote.voterId, 'ghost');
  });

  it('rejects votes for unknown candidates', () => {
    const ledger = new VoteLedger();
    ledger.addVote(vote('v1', 1, choice('mallory')));
    const eligibility = makeEligibility([['v1', 5n]]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.participatedWeight, 0n);
    assert.equal(result.rejected[0]?.reason, 'unknown_candidate');
  });

  it('reports no resolution below the participation threshold', () => {
    const ledger = new VoteLedger();
    ledger.addVote(vote('v1', 1, choice('alice')));
    const eligibility = makeEligibility([['v1', 5n]]);
    const result = ledger.tally(
      makeIssue({ participationThreshold: 6n }),
      eligibility,
    );
    assert.equal(result.resolved, false);
    assert.equal(result.reason, 'threshold_not_met');
    assert.equal(result.winner, null);
    assert.equal(result.participatedWeight, 5n);
  });

  it('resolves nothing when everyone abstains', () => {
    const ledger = new VoteLedger();
    ledger.addVotes([vote('v1', 1, ABSTAIN), vote('v2', 1, ABSTAIN)]);
    const eligibility = makeEligibility([
      ['v1', 5n],
      ['v2', 5n],
    ]);
    const result = ledger.tally(
      makeIssue({ participationThreshold: 3n }),
      eligibility,
    );
    assert.equal(result.participatedWeight, 10n);
    assert.equal(result.resolved, false);
    assert.equal(result.reason, 'no_candidate_weight');
    assert.equal(result.winner, null);
  });

  it('breaks candidate ties by candidate id', () => {
    const ledger = new VoteLedger();
    ledger.addVotes([
      vote('v1', 1, choice('bob')),
      vote('v2', 1, choice('alice')),
    ]);
    const eligibility = makeEligibility([
      ['v1', 5n],
      ['v2', 5n],
    ]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.resolved, true);
    assert.equal(result.winner, 'alice');
  });

  it('handles weights beyond the safe integer range exactly', () => {
    const huge = (1n << 62n) + 12345n;
    const ledger = new VoteLedger();
    ledger.addVotes([
      vote('v1', 1, choice('alice')),
      vote('v2', 1, choice('alice')),
      vote('v3', 1, choice('bob')),
    ]);
    const eligibility = makeEligibility([
      ['v1', huge],
      ['v2', huge],
      ['v3', huge * 2n - 1n],
    ]);
    const result = ledger.tally(makeIssue(), eligibility);
    assert.equal(result.candidateWeights['alice'], huge * 2n);
    assert.equal(result.candidateWeights['bob'], huge * 2n - 1n);
    assert.equal(result.participatedWeight, huge * 4n - 1n);
    assert.equal(result.winner, 'alice');
  });
});

describe('eligibility versions', () => {
  it('freezes the eligibility version pinned by the issue', () => {
    const table = new EligibilityTable();
    table.publishVersion(1, [['v1', 5n]]);
    table.publishVersion(2, [['v1', 500n]]); // later adjustment

    const ledger = new VoteLedger();
    ledger.addVote(vote('v1', 1, choice('alice')));

    const frozen = ledger.tally(makeIssue({ eligibilityVersion: 1 }), table);
    assert.equal(frozen.candidateWeights['alice'], 5n);

    const current = ledger.tally(makeIssue({ eligibilityVersion: 2 }), table);
    assert.equal(current.candidateWeights['alice'], 500n);
  });

  it('fails loudly when the pinned eligibility version is missing', () => {
    const table = new EligibilityTable();
    table.publishVersion(1, [['v1', 5n]]);
    const ledger = new VoteLedger();
    ledger.addVote(vote('v1', 1, choice('alice')));
    assert.throws(
      () => ledger.tally(makeIssue({ eligibilityVersion: 9 }), table),
      EligibilityVersionNotFoundError,
    );
    assert.throws(
      () => recomputeTally(ledger.allVotes(), makeIssue({ eligibilityVersion: 9 }), table),
      EligibilityVersionNotFoundError,
    );
  });

  it('rejects re-publishing a version with different content', () => {
    const table = new EligibilityTable();
    table.publishVersion(1, [['v1', 5n]]);
    table.publishVersion(1, [['v1', 5n]]); // identical: idempotent
    assert.throws(
      () => table.publishVersion(1, [['v1', 6n]]),
      EligibilityConflictError,
    );
  });
});

describe('merge and idempotency', () => {
  const votes = [
    vote('v1', 1, choice('alice'), 'r1'),
    vote('v1', 2, choice('bob'), 'r1'),
    vote('v2', 1, ABSTAIN, 'r2'),
    vote('v3', 3, WITHDRAW, 'r1'),
    vote('v3', 1, choice('alice'), 'r2'),
  ];

  it('re-importing the same votes is a no-op', () => {
    const ledger = new VoteLedger();
    ledger.addVotes(votes);
    const size = ledger.size;
    ledger.addVotes(votes);
    ledger.addVotes([...votes].reverse());
    assert.equal(ledger.size, size);
  });

  it('merging replicas converges regardless of order and repetition', () => {
    const left = new VoteLedger();
    left.addVotes([votes[0]!, votes[2]!, votes[4]!]);
    const right = new VoteLedger();
    right.addVotes([votes[1]!, votes[3]!, votes[2]!]);

    left.merge(right);
    left.merge(right); // repeated merge stays idempotent
    right.merge(left);

    const eligibility = makeEligibility([
      ['v1', 3n],
      ['v2', 2n],
      ['v3', 9n],
    ]);
    const issue = makeIssue();
    const leftTally = left.tally(issue, eligibility);
    const rightTally = right.tally(issue, eligibility);
    assert.ok(sameTally(leftTally, rightTally));

    // v1 -> bob(3), v2 -> abstain(2), v3 -> withdrawn
    assert.equal(leftTally.candidateWeights['bob'], 3n);
    assert.equal(leftTally.participatedWeight, 5n);
    assert.equal(leftTally.winner, 'bob');
  });

  it('reports conflicts for the same full version instead of picking one', () => {
    const ledger = new VoteLedger();
    ledger.addVote(vote('v1', 1, choice('alice'), 'r1'));
    assert.throws(
      () => ledger.addVote(vote('v1', 1, choice('bob'), 'r1')),
      VoteConflictError,
    );
    assert.throws(
      () => ledger.addVote(vote('v1', 1, WITHDRAW, 'r1')),
      VoteConflictError,
    );
    // Identical duplicate is still fine.
    ledger.addVote(vote('v1', 1, choice('alice'), 'r1'));
  });
});

describe('independent recomputer', () => {
  it('agrees with the ledger on shuffled and duplicated input', () => {
    const votes: Vote[] = [
      vote('v1', 1, choice('alice'), 'r1'),
      vote('v1', 2, WITHDRAW, 'r2'),
      vote('v2', 1, choice('bob'), 'r1'),
      vote('v2', 3, choice('alice'), 'r1'),
      vote('v3', 1, ABSTAIN, 'r3'),
      vote('ghost', 1, choice('alice'), 'r1'),
    ];
    const shuffled = [...votes].reverse();
    const duplicated = [...shuffled, ...shuffled];

    const ledger = new VoteLedger();
    ledger.addVotes(duplicated);

    const eligibility = makeEligibility([
      ['v1', 4n],
      ['v2', 6n],
      ['v3', 1n],
    ]);
    const issue = makeIssue();
    const ledgerTally = ledger.tally(issue, eligibility);
    const recomputed = recomputeTally(duplicated, issue, eligibility);
    assert.ok(sameTally(ledgerTally, recomputed));

    // v1 withdrew; v2 -> alice(6); v3 abstains(1); ghost rejected.
    assert.equal(recomputed.candidateWeights['alice'], 6n);
    assert.equal(recomputed.participatedWeight, 7n);
    assert.equal(recomputed.rejected.length, 1);
    assert.equal(recomputed.rejected[0]?.reason, 'not_eligible');
  });

  it('agrees on big integer weights', () => {
    const big = (1n << 70n) + 7n;
    const votes = [
      vote('v1', 1, choice('alice')),
      vote('v2', 1, ABSTAIN),
    ];
    const ledger = new VoteLedger();
    ledger.addVotes(votes);
    const eligibility = makeEligibility([
      ['v1', big],
      ['v2', big * 3n],
    ]);
    const issue = makeIssue();
    const a = ledger.tally(issue, eligibility);
    const b = recomputeTally(votes, issue, eligibility);
    assert.ok(sameTally(a, b));
    assert.equal(a.participatedWeight, big * 4n);
  });
});
