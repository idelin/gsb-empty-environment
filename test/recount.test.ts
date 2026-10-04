import assert from 'node:assert/strict';
import { test } from 'node:test';
import { EligibilityTable } from '../src/eligibility.ts';
import { VoteConflictError, VoteLedger } from '../src/ledger.ts';
import { recount, tallyResultsEqual } from '../src/recount.ts';
import { tallyIssue } from '../src/tally.ts';
import type { Issue, VoteRecord } from '../src/types.ts';

const ISSUE: Issue = {
  issueId: 'ISSUE-1',
  eligibilityVersion: 1,
  participationThreshold: 10n,
};

function eligibility(): EligibilityTable {
  const table = new EligibilityTable();
  table.addVersion(1, { alice: 5n, bob: 3n, carol: 4n });
  table.addVersion(2, { alice: 50n, bob: 30n, carol: 40n });
  return table;
}

function rawVotes(): VoteRecord[] {
  return [
    // alice：先投 c1，后改投 c2（高版本生效）
    { issueId: 'ISSUE-1', voterId: 'alice', version: 1, replicaId: 'r1', choice: { kind: 'candidate', candidateId: 'c1' } },
    { issueId: 'ISSUE-1', voterId: 'alice', version: 2, replicaId: 'r1', choice: { kind: 'candidate', candidateId: 'c2' } },
    // bob：撤回后又重放旧票，撤回必须保持生效
    { issueId: 'ISSUE-1', voterId: 'bob', version: 1, replicaId: 'r1', choice: { kind: 'candidate', candidateId: 'c1' } },
    { issueId: 'ISSUE-1', voterId: 'bob', version: 2, replicaId: 'r1', choice: { kind: 'retract' } },
    { issueId: 'ISSUE-1', voterId: 'bob', version: 1, replicaId: 'r1', choice: { kind: 'candidate', candidateId: 'c1' } },
    // carol：同版本跨副本并列，replica-b 胜出（弃权）
    { issueId: 'ISSUE-1', voterId: 'carol', version: 1, replicaId: 'replica-a', choice: { kind: 'candidate', candidateId: 'c1' } },
    { issueId: 'ISSUE-1', voterId: 'carol', version: 1, replicaId: 'replica-b', choice: { kind: 'abstain' } },
    // mallory：无资格，保留为被拒记录
    { issueId: 'ISSUE-1', voterId: 'mallory', version: 1, replicaId: 'r1', choice: { kind: 'candidate', candidateId: 'c2' } },
    // 其他议题的票不应混入
    { issueId: 'ISSUE-2', voterId: 'alice', version: 1, replicaId: 'r1', choice: { kind: 'candidate', candidateId: 'c9' } },
  ];
}

test('复算器与账本在混合场景下结果一致', () => {
  const votes = rawVotes();
  const table = eligibility();
  const ledger = new VoteLedger();
  ledger.import(votes);
  const fromLedger = tallyIssue(ledger, ISSUE, table);
  const fromRecount = recount(votes, ISSUE, table);
  assert.ok(tallyResultsEqual(fromLedger, fromRecount));

  assert.equal(fromRecount.candidateWeights.get('c2'), 5n);
  assert.equal(fromRecount.candidateWeights.get('c1'), undefined);
  assert.equal(fromRecount.participatedWeight, 9n); // alice 5 + carol 4（弃权也计入）
  assert.equal(fromRecount.rejected.length, 1);
  assert.equal(fromRecount.decided, false); // 9 < 门槛 10
  assert.equal(fromRecount.winner, null);
});

test('复算器对乱序与重复输入幂等', () => {
  const votes = rawVotes();
  const table = eligibility();
  const shuffled = [...votes].reverse();
  const duplicated = [...votes, ...votes];
  const baseline = recount(votes, ISSUE, table);
  assert.ok(tallyResultsEqual(baseline, recount(shuffled, ISSUE, table)));
  assert.ok(tallyResultsEqual(baseline, recount(duplicated, ISSUE, table)));
});

test('复算器同样检测同版本同副本的内容冲突', () => {
  const conflicting: VoteRecord[] = [
    { issueId: 'ISSUE-1', voterId: 'alice', version: 1, replicaId: 'r1', choice: { kind: 'candidate', candidateId: 'c1' } },
    { issueId: 'ISSUE-1', voterId: 'alice', version: 1, replicaId: 'r1', choice: { kind: 'abstain' } },
  ];
  assert.throws(() => recount(conflicting, ISSUE, eligibility()), VoteConflictError);
});

test('账本重复导入后复算仍一致（端到端幂等）', () => {
  const votes = rawVotes();
  const table = eligibility();
  const ledger = new VoteLedger();
  ledger.import(votes);
  ledger.import(votes);
  ledger.import([...votes].reverse());
  const fromLedger = tallyIssue(ledger, ISSUE, table);
  assert.ok(tallyResultsEqual(fromLedger, recount(votes, ISSUE, table)));
});
