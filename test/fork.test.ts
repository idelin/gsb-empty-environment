import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  addKeyAliasEnvelope,
  createEnvelope,
  deleteEnvelope,
  recoveryEnvelope,
  rehostEnvelope,
  transferEnvelope,
  updateEnvelope,
} from '../src/ownership/factory.ts';
import { resolveOwnership } from '../src/ownership/resolver.ts';
import type { Envelope } from '../src/ownership/types.ts';
import { buildFixture, decisionMap, permutations } from './helpers.ts';

function buildFork(fx: ReturnType<typeof buildFixture>) {
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u0', fields: { v: 0 } },
    { arrivedAt: 1 },
  );
  // 两支转让都从同一父节点 create 派生（同 gen、同 prev）。
  const branchA = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob, displayUrl: 'url-a' },
    { relay: 'ra', arrivedAt: 2 },
  );
  const branchB = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.carol, displayUrl: 'url-b' },
    { relay: 'rb', arrivedAt: 3 },
  );
  return { create, branchA, branchB };
}

test('同父双转让构成分叉：两支都等待恢复，且不发布任何部分字段', () => {
  const fx = buildFixture();
  const { create, branchA, branchB } = buildFork(fx);
  const updateA = updateEnvelope(
    { id: fx.objectId, prev: branchA.eventHash, gen: 1, holder: fx.bob, fields: { leaked: 'a' } },
    { arrivedAt: 4 },
  );
  const rehostB = rehostEnvelope(
    { id: fx.objectId, prev: branchB.eventHash, gen: 1, holder: fx.carol, displayUrl: 'http://x' },
    { arrivedAt: 5 },
  );

  const resolution = resolveOwnership([create, branchA, branchB, updateA, rehostB], fx.registry);
  const decisions = decisionMap(resolution);
  assert.equal(decisions.get(branchA.eventHash)!.reason, 'wait_fork_unresolved');
  assert.equal(decisions.get(branchB.eventHash)!.reason, 'wait_fork_unresolved');
  assert.equal(decisions.get(updateA.eventHash)!.kind, 'wait');
  assert.equal(decisions.get(rehostB.eventHash)!.kind, 'wait');

  const view = [...resolution.views.values()][0];
  assert.equal(view.forked, true);
  assert.equal(view.holderKeyId, null);
  assert.equal(view.displayUrl, null);
  assert.deepEqual(view.fields, {});
  assert.equal(resolution.isolated.length, 1);
});

test('域名排序不能解决分叉；只有满足门槛且覆盖两支的恢复才能合并', () => {
  const fx = buildFixture();
  const { create, branchA, branchB } = buildFork(fx);

  // 仅一票，低于 threshold=2：继续等待。
  const weakRecovery = recoveryEnvelope(
    {
      id: fx.objectId,
      prev: create.eventHash,
      gen: 1,
      forkParent: create.eventHash,
      branchTips: [branchA.eventHash, branchB.eventHash],
      canonicalTip: branchA.eventHash,
      voters: [fx.alice],
    },
    { arrivedAt: 6 },
  );
  const weak = resolveOwnership([create, branchA, branchB, weakRecovery], fx.registry);
  assert.equal(decisionMap(weak).get(weakRecovery.eventHash)!.reason, 'wait_recovery_threshold');

  // 两票、覆盖两支，选定 A 为权威支。
  const recovery = recoveryEnvelope(
    {
      id: fx.objectId,
      prev: create.eventHash,
      gen: 1,
      forkParent: create.eventHash,
      branchTips: [branchA.eventHash, branchB.eventHash],
      canonicalTip: branchA.eventHash,
      voters: [fx.alice, fx.carol],
    },
    { arrivedAt: 7 },
  );
  const resolved = resolveOwnership([create, branchA, branchB, recovery], fx.registry);
  const decisions = decisionMap(resolved);
  assert.equal(decisions.get(recovery.eventHash)!.kind, 'publish');
  const view = [...resolved.views.values()][0];
  assert.equal(view.forked, false);
  assert.equal(view.holderKeyId, fx.bob.keyId);
  assert.equal(view.displayUrl, 'url-a');
  assert.deepEqual(view.authorityChain, [create.eventHash, branchA.eventHash]);
  assert.equal(resolved.isolated.length, 0);
});

test('恢复必须覆盖两支：只引用一支的恢复被要求补齐证据', () => {
  const fx = buildFixture();
  const { create, branchA, branchB } = buildFork(fx);
  const bad = recoveryEnvelope(
    {
      id: fx.objectId,
      prev: create.eventHash,
      gen: 1,
      forkParent: create.eventHash,
      branchTips: [branchA.eventHash, branchA.eventHash],
      canonicalTip: branchA.eventHash,
      voters: [fx.alice, fx.bob],
    },
    { arrivedAt: 8 },
  );
  const resolution = resolveOwnership([create, branchA, branchB, bad], fx.registry);
  assert.equal(decisionMap(resolution).get(bad.eventHash)!.reason, 'wait_recovery_threshold');
});

test('分叉后其中一支删除：删除仍受分叉冻结，恢复选择另一支后对象存活', () => {
  const fx = buildFixture();
  const { create, branchA, branchB } = buildFork(fx);
  const deleteB = deleteEnvelope(
    { id: fx.objectId, prev: branchB.eventHash, gen: 1, holder: fx.carol, reason: 'branch-b cleanup' },
    { arrivedAt: 9 },
  );
  const recovery = recoveryEnvelope(
    {
      id: fx.objectId,
      prev: create.eventHash,
      gen: 1,
      forkParent: create.eventHash,
      branchTips: [branchA.eventHash, branchB.eventHash],
      canonicalTip: branchA.eventHash,
      voters: [fx.alice, fx.bob],
    },
    { arrivedAt: 10 },
  );
  const before = resolveOwnership([create, branchA, branchB, deleteB], fx.registry);
  assert.equal(decisionMap(before).get(deleteB.eventHash)!.kind, 'wait');

  const after = resolveOwnership([create, branchA, branchB, deleteB, recovery], fx.registry);
  const view = [...after.views.values()][0];
  assert.equal(view.deleted, false);
  assert.equal(view.holderKeyId, fx.bob.keyId);
});

test('恢复签名者公钥别名重票：addKeyAlias 后新公钥仍可投出有效票', () => {
  const fx = buildFixture();
  const { create, branchA, branchB } = buildFork(fx);
  // carol 轮换出新公钥 carol2，并在对象上登记别名（旧密钥签名）。
  const carol2 = buildFixture().carol;
  fx.registry.add(carol2);
  const aliasEvent = addKeyAliasEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 0, existing: fx.alice, newKey: carol2 },
    { arrivedAt: 11 },
  );
  const recovery = recoveryEnvelope(
    {
      id: fx.objectId,
      prev: create.eventHash,
      gen: 1,
      forkParent: create.eventHash,
      branchTips: [branchA.eventHash, branchB.eventHash],
      canonicalTip: branchB.eventHash,
      // alice 直接投，carol 用轮换后的 carol2 公钥投（成员身份经 addKeyAlias 等价）。
      voters: [fx.alice, carol2],
    },
    { arrivedAt: 12 },
  );
  const all = [create, branchA, branchB, aliasEvent, recovery];
  const resolution = resolveOwnership(all, fx.registry);
  assert.equal(decisionMap(resolution).get(recovery.eventHash)!.kind, 'publish');

  for (const ordering of permutations(all)) {
    const rerun = resolveOwnership(ordering, fx.registry);
    assert.equal(decisionMap(rerun).get(recovery.eventHash)!.kind, 'publish');
    const view = [...rerun.views.values()][0];
    assert.equal(view.holderKeyId, fx.carol.keyId);
  }
});

test('分叉事件任意排列：恢复前后判定保持确定一致', () => {
  const fx = buildFixture();
  const { create, branchA, branchB } = buildFork(fx);
  const recovery = recoveryEnvelope(
    {
      id: fx.objectId,
      prev: create.eventHash,
      gen: 1,
      forkParent: create.eventHash,
      branchTips: [branchA.eventHash, branchB.eventHash],
      canonicalTip: branchA.eventHash,
      voters: [fx.alice, fx.bob],
    },
    { arrivedAt: 13 },
  );
  const events: Envelope[] = [create, branchA, branchB, recovery];
  const reference = resolveOwnership(events, fx.registry);
  for (const ordering of permutations(events)) {
    const rerun = resolveOwnership(ordering, fx.registry);
    for (const envelope of events) {
      assert.deepEqual(decisionMap(rerun).get(envelope.eventHash), decisionMap(reference).get(envelope.eventHash));
    }
  }
});
