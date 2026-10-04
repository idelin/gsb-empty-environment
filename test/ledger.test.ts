import assert from 'node:assert/strict';
import { test } from 'node:test';
import { VoteConflictError, VoteLedger } from '../src/ledger.ts';
import type { Choice, VoteRecord } from '../src/types.ts';

function vote(overrides: Partial<VoteRecord> & { choice?: Choice } = {}): VoteRecord {
  return {
    issueId: 'ISSUE-1',
    voterId: 'alice',
    version: 1,
    replicaId: 'replica-a',
    choice: { kind: 'candidate', candidateId: 'c1' },
    ...overrides,
  };
}

test('迟到的撤回是版本，旧票无法将其复活', () => {
  const ledger = new VoteLedger();
  ledger.import([vote({ version: 1 })]);
  ledger.import([vote({ version: 2, choice: { kind: 'retract' } })]);
  ledger.import([vote({ version: 1 })]);
  ledger.import([vote({ version: 1, replicaId: 'replica-z' })]);
  const effective = ledger.effectiveVotes('ISSUE-1');
  assert.equal(effective.length, 1);
  assert.deepEqual(effective[0].choice, { kind: 'retract' });
  assert.equal(effective[0].version, 2);
});

test('撤回先于原票到达时合并结果一致', () => {
  const ledger = new VoteLedger();
  ledger.import([vote({ version: 2, choice: { kind: 'retract' } })]);
  ledger.import([vote({ version: 1 })]);
  assert.deepEqual(ledger.effectiveVotes('ISSUE-1')[0].choice, { kind: 'retract' });
});

test('重复导入幂等', () => {
  const ledger = new VoteLedger();
  const batch = [
    vote({}),
    vote({ voterId: 'bob', version: 3, replicaId: 'replica-b', choice: { kind: 'abstain' } }),
  ];
  ledger.import(batch);
  const first = ledger.effectiveVotes();
  ledger.import(batch);
  ledger.import([...batch].reverse());
  assert.equal(ledger.size, 2);
  assert.deepEqual(ledger.effectiveVotes(), first);
});

test('同完整票版本内容冲突报错，不任选一份', () => {
  const ledger = new VoteLedger();
  ledger.import([vote({})]);
  assert.throws(
    () => ledger.import([vote({ choice: { kind: 'candidate', candidateId: 'c2' } })]),
    VoteConflictError,
  );
  assert.throws(
    () => ledger.import([vote({ choice: { kind: 'abstain' } })]),
    VoteConflictError,
  );
  assert.deepEqual(ledger.effectiveVotes()[0].choice, { kind: 'candidate', candidateId: 'c1' });
});

test('同版本跨副本并列时按副本ID取舍，与到达顺序无关', () => {
  const low = vote({ replicaId: 'replica-a', choice: { kind: 'candidate', candidateId: 'c1' } });
  const high = vote({ replicaId: 'replica-b', choice: { kind: 'candidate', candidateId: 'c2' } });
  const ledgerAB = new VoteLedger();
  ledgerAB.import([low, high]);
  const ledgerBA = new VoteLedger();
  ledgerBA.import([high, low]);
  assert.deepEqual(ledgerAB.effectiveVotes()[0].choice, { kind: 'candidate', candidateId: 'c2' });
  assert.deepEqual(ledgerBA.effectiveVotes()[0].choice, { kind: 'candidate', candidateId: 'c2' });
});

test('低版本票在高版本之后导入被忽略', () => {
  const ledger = new VoteLedger();
  ledger.import([vote({ version: 5, choice: { kind: 'candidate', candidateId: 'c9' } })]);
  ledger.import([vote({ version: 4, replicaId: 'replica-z' })]);
  assert.deepEqual(ledger.effectiveVotes()[0].choice, { kind: 'candidate', candidateId: 'c9' });
});

test('非法票记录被拒绝', () => {
  const ledger = new VoteLedger();
  assert.throws(() => ledger.import([vote({ version: -1 })]), TypeError);
  assert.throws(() => ledger.import([vote({ version: 1.5 })]), TypeError);
  assert.throws(() => ledger.import([vote({ voterId: '' })]), TypeError);
  assert.throws(
    () => ledger.import([vote({ choice: { kind: 'candidate' } as Choice })]),
    TypeError,
  );
});

test('不同议题的票相互隔离', () => {
  const ledger = new VoteLedger();
  ledger.import([vote({ issueId: 'ISSUE-1', version: 1 })]);
  ledger.import([vote({ issueId: 'ISSUE-2', version: 1, choice: { kind: 'abstain' } })]);
  assert.equal(ledger.effectiveVotes('ISSUE-1').length, 1);
  assert.equal(ledger.effectiveVotes('ISSUE-2').length, 1);
  assert.equal(ledger.size, 2);
});
