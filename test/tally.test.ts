import assert from 'node:assert/strict';
import { test } from 'node:test';
import { EligibilityTable, MissingEligibilityVersionError } from '../src/eligibility.ts';
import { VoteLedger } from '../src/ledger.ts';
import { tallyIssue } from '../src/tally.ts';
import type { Issue, VoteRecord } from '../src/types.ts';

function eligibilityOf(entries: Array<[number, Record<string, bigint>]>): EligibilityTable {
  const table = new EligibilityTable();
  for (const [version, weights] of entries) {
    table.addVersion(version, weights);
  }
  return table;
}

function issueOf(overrides: Partial<Issue> = {}): Issue {
  return {
    issueId: 'ISSUE-1',
    eligibilityVersion: 1,
    participationThreshold: 0n,
    ...overrides,
  };
}

function vote(overrides: Partial<VoteRecord> = {}): VoteRecord {
  return {
    issueId: 'ISSUE-1',
    voterId: 'alice',
    version: 1,
    replicaId: 'replica-a',
    choice: { kind: 'candidate', candidateId: 'c1' },
    ...overrides,
  };
}

function ledgerOf(votes: VoteRecord[]): VoteLedger {
  const ledger = new VoteLedger();
  ledger.import(votes);
  return ledger;
}

test('议题冻结资格版本，后续权重调整不影响历史结果', () => {
  const eligibility = eligibilityOf([
    [1, { alice: 5n, bob: 3n }],
    [2, { alice: 500n, bob: 1n }],
  ]);
  const ledger = ledgerOf([vote({}), vote({ voterId: 'bob', choice: { kind: 'candidate', candidateId: 'c2' } })]);
  const result = tallyIssue(ledger, issueOf({ eligibilityVersion: 1 }), eligibility);
  assert.equal(result.candidateWeights.get('c1'), 5n);
  assert.equal(result.candidateWeights.get('c2'), 3n);
  assert.equal(result.participatedWeight, 8n);
  assert.equal(result.winner, 'c1');
});

test('资格版本缺失时报错', () => {
  const eligibility = eligibilityOf([[1, { alice: 5n }]]);
  const ledger = ledgerOf([vote({})]);
  assert.throws(
    () => tallyIssue(ledger, issueOf({ eligibilityVersion: 7 }), eligibility),
    MissingEligibilityVersionError,
  );
});

test('弃权计入参与权重但不计候选', () => {
  const eligibility = eligibilityOf([[1, { alice: 5n, bob: 3n }]]);
  const ledger = ledgerOf([
    vote({}),
    vote({ voterId: 'bob', choice: { kind: 'abstain' } }),
  ]);
  const result = tallyIssue(ledger, issueOf(), eligibility);
  assert.equal(result.participatedWeight, 8n);
  assert.equal(result.candidateWeights.size, 1);
  assert.equal(result.candidateWeights.get('c1'), 5n);
  assert.equal(result.winner, 'c1');
});

test('全部弃权：参与权重达标但无胜者', () => {
  const eligibility = eligibilityOf([[1, { alice: 5n, bob: 3n }]]);
  const ledger = ledgerOf([
    vote({ choice: { kind: 'abstain' } }),
    vote({ voterId: 'bob', choice: { kind: 'abstain' } }),
  ]);
  const result = tallyIssue(ledger, issueOf({ participationThreshold: 8n }), eligibility);
  assert.equal(result.participatedWeight, 8n);
  assert.equal(result.decided, true);
  assert.equal(result.winner, null);
  assert.equal(result.candidateWeights.size, 0);
});

test('撤回的票不计入参与权重', () => {
  const eligibility = eligibilityOf([[1, { alice: 5n, bob: 3n }]]);
  const ledger = ledgerOf([
    vote({}),
    vote({ voterId: 'bob', version: 2, choice: { kind: 'retract' } }),
    vote({ voterId: 'bob', version: 1, choice: { kind: 'candidate', candidateId: 'c2' } }),
  ]);
  const result = tallyIssue(ledger, issueOf(), eligibility);
  assert.equal(result.participatedWeight, 5n);
  assert.equal(result.candidateWeights.get('c2'), undefined);
});

test('无资格票保留为被拒记录且不计权重', () => {
  const eligibility = eligibilityOf([[1, { alice: 5n }]]);
  const stranger = vote({ voterId: 'mallory', choice: { kind: 'candidate', candidateId: 'c2' } });
  const ledger = ledgerOf([vote({}), stranger]);
  const result = tallyIssue(ledger, issueOf(), eligibility);
  assert.equal(result.participatedWeight, 5n);
  assert.equal(result.candidateWeights.get('c2'), undefined);
  assert.equal(result.rejected.length, 1);
  assert.equal(result.rejected[0].reason, 'ineligible');
  assert.equal(result.rejected[0].vote.voterId, 'mallory');
});

test('未达到参与门槛则无决议', () => {
  const eligibility = eligibilityOf([[1, { alice: 5n, bob: 3n }]]);
  const ledger = ledgerOf([vote({})]);
  const result = tallyIssue(ledger, issueOf({ participationThreshold: 6n }), eligibility);
  assert.equal(result.participatedWeight, 5n);
  assert.equal(result.decided, false);
  assert.equal(result.winner, null);
});

test('候选权重并列时按候选ID取字典序最小者', () => {
  const eligibility = eligibilityOf([[1, { alice: 5n, bob: 5n }]]);
  const ledger = ledgerOf([
    vote({ choice: { kind: 'candidate', candidateId: 'c2' } }),
    vote({ voterId: 'bob', choice: { kind: 'candidate', candidateId: 'c1' } }),
  ]);
  const result = tallyIssue(ledger, issueOf(), eligibility);
  assert.equal(result.candidateWeights.get('c1'), 5n);
  assert.equal(result.candidateWeights.get('c2'), 5n);
  assert.equal(result.winner, 'c1');
});

test('大整数权重精确求和，不丢失精度', () => {
  const big1 = 9007199254740993n; // 2^53 + 1，超出 double 精确范围
  const big2 = 9007199254740995n; // 2^53 + 3
  const eligibility = eligibilityOf([[1, { alice: big1, bob: big2 }]]);
  const ledger = ledgerOf([
    vote({}),
    vote({ voterId: 'bob' }),
  ]);
  const result = tallyIssue(ledger, issueOf(), eligibility);
  assert.equal(result.candidateWeights.get('c1'), 18014398509481988n);
  assert.equal(result.participatedWeight, 18014398509481988n);
  assert.equal(result.winner, 'c1');
});

test('资格表同版本内容冲突报错，重复相同版本幂等', () => {
  const table = new EligibilityTable();
  table.addVersion(1, { alice: 5n });
  table.addVersion(1, { alice: 5n });
  assert.throws(() => table.addVersion(1, { alice: 6n }), /conflicting/);
});
