import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  createEnvelope,
  recoveryEnvelope,
  revokeEnvelope,
  transferEnvelope,
  updateEnvelope,
} from '../src/ownership/factory.ts';
import { foldCheckpoint, signWithCheckpointKey, verifyCheckpoint } from '../src/ownership/checkpoint.ts';
import { generateMemberKey } from '../src/ownership/crypto.ts';
import { resolveOwnership } from '../src/ownership/resolver.ts';
import type { Checkpoint, Envelope } from '../src/ownership/types.ts';
import { buildFixture, decisionMap } from './helpers.ts';

test('检查点压缩前缀后：旧别名与同代异摘要仍可识别，撤回不复活', () => {
  const fx = buildFixture();
  const checkpointIssuer = generateMemberKey();
  const create = createEnvelope(
    {
      id: fx.objectId,
      holder: fx.alice,
      recoveryPolicy: fx.policy,
      displayUrl: 'u0',
      aliases: ['legacy-alias'],
      fields: { v: 1 },
    },
    { arrivedAt: 1 },
  );
  const transfer = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { arrivedAt: 2 },
  );
  // bob 的编辑权被撤回（edit 范围）。
  const revoke = revokeEnvelope(
    {
      id: fx.objectId,
      prev: transfer.eventHash,
      gen: 1,
      holder: fx.bob,
      targetKeyId: fx.bob.keyId,
      scope: 'edit',
    },
    { arrivedAt: 3 },
  );

  const prefix = resolveOwnership([create, transfer, revoke], fx.registry);
  const prefixView = [...prefix.views.values()][0];
  const unsigned = foldCheckpoint([create, transfer, revoke], fx.registry, prefixView, {
    checkpointKeyB64: checkpointIssuer.publicKeyB64,
  });
  const checkpoint: Checkpoint = signWithCheckpointKey(unsigned, checkpointIssuer.privateKeyPem);
  assert.equal(verifyCheckpoint(checkpoint, checkpointIssuer.publicKeyB64), true);
  assert.equal(checkpoint.aliases.includes('legacy-alias'), true);
  assert.equal(checkpoint.revokedEdit.includes(fx.bob.keyId), true);

  // 压缩后，仅基于检查点 + 新事件继续；bob 的更新仍被拒绝（编辑权未复活）。
  const staleUpdate = updateEnvelope(
    { id: fx.objectId, prev: checkpoint.tipHash, gen: 1, holder: fx.bob, fields: { v: 99 } },
    { arrivedAt: 4 },
  );
  const after = resolveOwnership([staleUpdate], fx.registry, { checkpoints: [checkpoint] });
  assert.equal(decisionMap(after).get(staleUpdate.eventHash)!.reason, 'reject_revoked_editor');

  // 同代异摘要：检查点保留 genDigests，新的同代竞争仍可被识别为分叉。
  const competing = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.carol },
    { arrivedAt: 5 },
  );
  assert.equal(checkpoint.genDigests.some((digest) => digest.digests.includes(transfer.eventHash)), true);
  const withCompeting = resolveOwnership([competing], fx.registry, { checkpoints: [checkpoint] });
  // 检查点已沿 transfer 推进，competing 引用已压缩父节点形成竞争，被识别为分叉等待。
  const decision = decisionMap(withCompeting).get(competing.eventHash)!;
  assert.equal(['wait_fork_unresolved', 'wait_missing_ancestor'].includes(decision.reason), true);
});

test('恢复不复活已撤回编辑权（撤回在两支并集上保持）', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u0' },
    { arrivedAt: 1 },
  );
  const branchA = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { arrivedAt: 2 },
  );
  const branchB = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.carol },
    { arrivedAt: 3 },
  );
  // 在 B 支撤回 bob 的编辑权。
  const revokeBob = revokeEnvelope(
    {
      id: fx.objectId,
      prev: branchB.eventHash,
      gen: 1,
      holder: fx.carol,
      targetKeyId: fx.bob.keyId,
      scope: 'edit',
    },
    { arrivedAt: 4 },
  );
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
    { arrivedAt: 5 },
  );
  // 恢复后 bob 成为持有人，但其编辑权已在 B 支撤回，更新仍应被拒绝。
  const bobUpdate = updateEnvelope(
    { id: fx.objectId, prev: branchA.eventHash, gen: 1, holder: fx.bob, fields: { x: 1 } },
    { arrivedAt: 6 },
  );
  const events: Envelope[] = [create, branchA, branchB, revokeBob, recovery, bobUpdate];
  const resolution = resolveOwnership(events, fx.registry);
  assert.equal(decisionMap(resolution).get(recovery.eventHash)!.kind, 'publish');
  assert.equal(decisionMap(resolution).get(bobUpdate.eventHash)!.reason, 'reject_revoked_editor');
});
