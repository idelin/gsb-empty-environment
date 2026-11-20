import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  ConflictResolutionError,
  resolveConflict,
  type AnchorVerifier,
  type Branch,
  type StreamCommit,
} from '../src/index.js';

const GENESIS = 'genesis-cid';

interface FakeAnchor {
  blockTimestamp: number;
  valid: boolean;
}

/** 调用方注入的验证器：valid 为 true 才接受，并返回区块时间戳。 */
const acceptingVerifier: AnchorVerifier = (proof) => {
  const anchor = proof as FakeAnchor;
  if (!anchor.valid) {
    return undefined;
  }
  return { anchorTimestamp: anchor.blockTimestamp };
};

/** 永远拒绝的验证器，用于模拟伪造锚定。 */
const rejectingVerifier: AnchorVerifier = () => undefined;

/** 会抛异常的验证器：异常等价于未通过验证。 */
const throwingVerifier: AnchorVerifier = () => {
  throw new Error('anchor proof verification failed');
};

type CommitSpec = {
  cid: string;
  anchor?: FakeAnchor;
  prev?: string;
};

/** 按顺序构造提交链；首个提交为创世提交。 */
function makeBranch(cids: Array<string | CommitSpec>): Branch {
  const commits: StreamCommit[] = [];
  for (let index = 0; index < cids.length; index += 1) {
    const entry = cids[index];
    const spec: CommitSpec =
      typeof entry === 'string' ? { cid: entry } : entry;
    const prev =
      spec.prev ?? (index === 0 ? undefined : commits[index - 1].cid);
    commits.push({
      cid: spec.cid,
      prev,
      anchor: spec.anchor as unknown,
    });
  }
  return { genesisCid: GENESIS, commits };
}

const anchor = (blockTimestamp: number, valid = true): FakeAnchor => ({
  blockTimestamp,
  valid,
});

test('同一提示直接保留当前分支', async () => {
  const branch = makeBranch([GENESIS, 'c1', 'c2']);
  const sameTip = makeBranch([GENESIS, 'c1', 'c2']);
  const result = await resolveConflict(branch, sameTip, acceptingVerifier);

  assert.equal(result.tipCid, 'c2');
  assert.equal(result.winner, branch);
  assert.equal(result.reason, 'same-tip');
});

test('已锚定分支优先于更长的未锚定分支', async () => {
  // 当前分支在 c2 锚定后继续延伸；候选从锚定提交 c2 之后分叉，不锚定但更长。
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c2', anchor: anchor(1000) },
    'c3',
  ]);
  const candidate = makeBranch([
    GENESIS,
    'c1',
    'c2',
    'u3',
    'u4',
    'u5',
  ]);

  const result = await resolveConflict(current, candidate, acceptingVerifier);

  assert.equal(result.winner, current);
  assert.equal(result.tipCid, 'c3');
  assert.equal(result.anchorTimestamp, 1000);
  assert.equal(result.reason, 'anchored-over-unanchored');
});

test('已锚定分支收到另一条已锚定候选时，较早区块时间戳者胜', async () => {
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c3', anchor: anchor(2000) },
    'c4',
  ]);
  const candidate = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c3', anchor: anchor(2000) },
    { cid: 'n3', anchor: anchor(1500) },
    'n4',
  ]);

  const result = await resolveConflict(current, candidate, acceptingVerifier);

  assert.equal(result.winner, candidate);
  assert.equal(result.tipCid, 'n4');
  assert.equal(result.anchorTimestamp, 1500);
  assert.equal(result.reason, 'earlier-anchor-timestamp');
});

test('两支锚定时间戳相同时，较长者胜', async () => {
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c3', anchor: anchor(1000) },
    'c4',
  ]);
  const candidate = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c3', anchor: anchor(1000) },
    { cid: 'n3', anchor: anchor(1000) },
    'n4',
  ]);

  const result = await resolveConflict(current, candidate, acceptingVerifier);

  assert.equal(result.winner, candidate);
  assert.equal(result.tipLength, 5);
  assert.equal(result.reason, 'longer-history');
});

test('两支均未锚定时，较长者胜', async () => {
  const current = makeBranch([GENESIS, 'c1', 'c2']);
  const candidate = makeBranch([GENESIS, 'c1', 'n2', 'n3']);

  const result = await resolveConflict(current, candidate, acceptingVerifier);

  assert.equal(result.winner, candidate);
  assert.equal(result.reason, 'longer-history');
});

test('未锚定且长度相同时，提示 CID 字典序较小者胜', async () => {
  const current = makeBranch([GENESIS, 'c1', 'tip-zzz']);
  const candidate = makeBranch([GENESIS, 'c1', 'tip-aaa']);

  const result = await resolveConflict(current, candidate, acceptingVerifier);

  assert.equal(result.winner, candidate);
  assert.equal(result.tipCid, 'tip-aaa');
  assert.equal(result.reason, 'smaller-tip-cid');

  // 交换入参方向，结果仍是字典序较小者，规则对称。
  const reversed = await resolveConflict(candidate, current, acceptingVerifier);
  assert.equal(reversed.winner, candidate);
});

test('分叉点早于当前锚定提交：候选锚定更早才允许取代', async () => {
  // 当前链在 c2 锚定，随后继续延伸；候选在创世处（早于 c2）分叉。
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c2', anchor: anchor(2000) },
    'c3',
  ]);
  const candidate = makeBranch([
    GENESIS,
    { cid: 'n1', prev: GENESIS },
    { cid: 'n2', prev: 'n1', anchor: anchor(1800) },
  ]);

  const result = await resolveConflict(current, candidate, acceptingVerifier);

  assert.equal(result.winner, candidate);
  assert.equal(result.reason, 'anchor-before-current-anchor');
});

test('分叉点早于当前锚定提交：候选未锚定时即使更长也不能取代', async () => {
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c2', anchor: anchor(2000) },
    'c3',
  ]);
  const candidate = makeBranch([
    GENESIS,
    { cid: 'n1', prev: GENESIS },
    { cid: 'n2', prev: 'n1' },
    { cid: 'n3', prev: 'n2' },
    { cid: 'n4', prev: 'n3' },
  ]);

  const result = await resolveConflict(current, candidate, acceptingVerifier);

  assert.equal(result.winner, current);
  assert.equal(result.reason, 'unanchored-cannot-replace-anchored');
});

test('分叉点早于当前锚定提交：候选锚定时间相同或更晚时不能取代', async () => {
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c2', anchor: anchor(2000) },
    'c3',
  ]);
  const sameTimestamp = makeBranch([
    GENESIS,
    { cid: 'n1', prev: GENESIS },
    { cid: 'n2', prev: 'n1', anchor: anchor(2000) },
    { cid: 'n3', prev: 'n2' },
  ]);

  const sameResult = await resolveConflict(current, sameTimestamp, acceptingVerifier);
  assert.equal(sameResult.winner, current);
  assert.equal(sameResult.reason, 'anchor-not-before-current-anchor');

  const later = makeBranch([
    GENESIS,
    { cid: 'm1', prev: GENESIS },
    { cid: 'm2', prev: 'm1', anchor: anchor(2500) },
  ]);
  const laterResult = await resolveConflict(current, later, acceptingVerifier);
  assert.equal(laterResult.winner, current);
  assert.equal(laterResult.reason, 'anchor-not-before-current-anchor');
});

test('伪造锚定未通过验证时按未锚定处理，不能取代已锚定历史', async () => {
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c2', anchor: anchor(1000) },
    'c3',
  ]);
  // 候选在锚定提交 c2 之后分叉，携带 valid=false 的凭据；验证器返回 undefined，
  // 该锚定按未锚定处理，因此已锚定的当前分支仍然获胜。
  const forged = makeBranch([
    GENESIS,
    'c1',
    'c2',
    { cid: 'n3', anchor: anchor(500, false) },
    'n4',
    'n5',
  ]);

  const result = await resolveConflict(current, forged, acceptingVerifier);

  assert.equal(result.winner, current);
  assert.equal(result.anchorTimestamp, 1000);
  assert.equal(result.reason, 'anchored-over-unanchored');
});

test('全部锚定均被拒绝时，分支整体按未锚定参与比较', async () => {
  const current = makeBranch([
    GENESIS,
    { cid: 'c1', anchor: anchor(1000) },
  ]);
  const candidate = makeBranch([
    GENESIS,
    { cid: 'n1', anchor: anchor(500) },
    'n2',
  ]);

  const result = await resolveConflict(current, candidate, rejectingVerifier);

  assert.equal(result.anchorTimestamp, undefined);
  assert.equal(result.winner, candidate);
  assert.equal(result.reason, 'longer-history');
});

test('验证器抛异常等价于未通过验证', async () => {
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c2', anchor: anchor(1000) },
  ]);
  const candidate = makeBranch([
    GENESIS,
    'c1',
    { cid: 'z2', anchor: anchor(900) },
  ]);

  const result = await resolveConflict(current, candidate, throwingVerifier);

  assert.equal(result.winner, current);
  assert.equal(result.reason, 'smaller-tip-cid');
  assert.equal(result.anchorTimestamp, undefined);
});

test('同一支上存在多个锚定时，最后一个有效锚定决定分支锚定时间', async () => {
  const current = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c2', anchor: anchor(900) },
    { cid: 'c3', anchor: anchor(2000) },
    'c4',
  ]);
  const candidate = makeBranch([
    GENESIS,
    'c1',
    { cid: 'c2', anchor: anchor(900) },
    { cid: 'c3', anchor: anchor(2000) },
    { cid: 'd4', anchor: anchor(1800) },
  ]);

  const result = await resolveConflict(current, candidate, acceptingVerifier);

  assert.equal(result.winner, candidate);
  assert.equal(result.anchorTimestamp, 1800);
  assert.equal(result.reason, 'earlier-anchor-timestamp');
});

test('创世 CID 不一致时抛错', async () => {
  const current = makeBranch([GENESIS, 'c1']);
  const other: Branch = { genesisCid: 'other-genesis', commits: [{ cid: 'other-genesis' }] };

  await assert.rejects(
    () => resolveConflict(current, other, acceptingVerifier),
    ConflictResolutionError,
  );
});

test('提交链结构损坏时抛错', async () => {
  const current = makeBranch([GENESIS, 'c1']);
  const broken: Branch = {
    genesisCid: GENESIS,
    commits: [
      { cid: GENESIS },
      { cid: 'x', prev: 'some-unrelated-cid' },
    ],
  };

  await assert.rejects(
    () => resolveConflict(current, broken, acceptingVerifier),
    ConflictResolutionError,
  );
});
