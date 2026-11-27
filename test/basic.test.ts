import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  createEnvelope,
  reDeliver,
  transferEnvelope,
  updateEnvelope,
} from '../src/ownership/factory.ts';
import { resolveOwnership } from '../src/ownership/resolver.ts';
import type { Envelope } from '../src/ownership/types.ts';
import { buildFixture, decisionMap, permutations } from './helpers.ts';

test('乱序到达：更新先于其转让，最终仍按权威链发布', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    {
      id: fx.objectId,
      holder: fx.alice,
      recoveryPolicy: fx.policy,
      displayUrl: 'https://cdn.example/a',
      fields: { v: 1 },
    },
    { relay: 'r1', arrivedAt: 1 },
  );
  const transfer = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { relay: 'r1', arrivedAt: 3 },
  );
  // 更新在转让之前到达（联邦收件箱乱序），其 prev 指向尚不可见的转让。
  const update = updateEnvelope(
    { id: fx.objectId, prev: transfer.eventHash, gen: 1, holder: fx.bob, fields: { v: 2 } },
    { relay: 'r2', arrivedAt: 2 },
  );

  const early = resolveOwnership([create, update], fx.registry);
  const earlyDecision = decisionMap(early).get(update.eventHash)!;
  assert.equal(earlyDecision.kind, 'wait');
  assert.equal(earlyDecision.reason, 'wait_missing_ancestor');

  const full = resolveOwnership([create, update, transfer], fx.registry);
  const decisions = decisionMap(full);
  assert.equal(decisions.get(create.eventHash)!.kind, 'publish');
  assert.equal(decisions.get(transfer.eventHash)!.kind, 'publish');
  assert.equal(decisions.get(update.eventHash)!.kind, 'publish');

  const view = [...full.views.values()][0];
  assert.equal(view.holderKeyId, fx.bob.keyId);
  assert.deepEqual(view.fields, { v: 2 });
  assert.equal(view.authorityChain.includes(transfer.eventHash), true);
});

test('转让缺少接收者签名时拒绝，仅有 HTTP 传输签名也拒绝', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u' },
    { arrivedAt: 1 },
  );
  const transfer = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { arrivedAt: 2 },
  );
  // 去掉接收者签名，仅保留旧持有人签名。
  const oldHolderOnly: Envelope = { ...transfer, auth: [transfer.auth[0]!] };
  const resolution = resolveOwnership([create, oldHolderOnly], fx.registry);
  assert.equal(decisionMap(resolution).get(transfer.eventHash)!.reason, 'reject_unauthorized_signer');

  // 完全没有授权签名，只带传输签名。
  const transportOnly: Envelope = {
    ...transfer,
    auth: [],
    delivery: { relay: 'evil', transportSignature: 'transport-sig' },
  };
  const transportResolution = resolveOwnership([create, transportOnly], fx.registry);
  assert.equal(
    decisionMap(transportResolution).get(transfer.eventHash)!.reason,
    'reject_transport_only',
  );
});

test('不同投递者转发相同原始签名：去重且判定一致（排列不变）', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u' },
    { arrivedAt: 1 },
  );
  const transfer = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { relay: 'r1', arrivedAt: 2 },
  );
  const replay = reDeliver(transfer, { relay: 'r2', transportSignature: 'ts2', arrivedAt: 5 });
  const replay2 = reDeliver(transfer, { relay: 'r3', transportSignature: 'ts3', arrivedAt: 6 });

  const base = [create, transfer, replay, replay2];
  const reference = resolveOwnership(base, fx.registry);
  assert.equal(reference.duplicateDeliveries, 2);
  const referenceDecision = decisionMap(reference).get(transfer.eventHash)!;

  for (const ordering of permutations(base)) {
    const resolution = resolveOwnership(ordering, fx.registry);
    assert.equal(resolution.duplicateDeliveries, 2);
    assert.deepEqual(decisionMap(resolution).get(transfer.eventHash), referenceDecision);
    const view = [...resolution.views.values()][0];
    assert.equal(view.holderKeyId, fx.bob.keyId);
  }
});

test('重放的传输签名不能改变对象身份或授权结果', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u1' },
    { arrivedAt: 1 },
  );
  const replayed = reDeliver(create, { relay: 'untrusted', transportSignature: 'forged-transport', arrivedAt: 9 });
  const resolution = resolveOwnership([create, replayed], fx.registry);
  assert.equal(resolution.duplicateDeliveries, 1);
  const view = [...resolution.views.values()][0];
  assert.equal(view.id.origin, 'origin.example');
  assert.equal(view.displayUrl, 'u1');
});
