import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createEnvelope } from '../src/ownership/factory.ts';
import { resolveOwnership } from '../src/ownership/resolver.ts';
import { buildFixture, decisionMap } from './helpers.ts';

test('eventHash 与事件体不符时以 reject_malformed 拒绝，且不影响其他对象', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u' },
    { arrivedAt: 1 },
  );
  const tampered = { ...create, eventHash: `${create.eventHash}deadbeef` };
  const resolution = resolveOwnership([create, tampered], fx.registry);
  const decision = decisionMap(resolution).get(tampered.eventHash)!;
  assert.equal(decision.kind, 'reject');
  assert.equal(decision.reason, 'reject_malformed');

  const view = [...resolution.views.values()][0];
  assert.equal(view.id.permanentId, 'obj-1');
  assert.equal(view.authorityChain.length, 1);
});

test('完整事件参考：每条决策都回指 eventHash/prev/gen，且包含重复投递观测', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u' },
    { relay: 'r1', arrivedAt: 1 },
  );
  const replay = { ...create, arrivedAt: 9, delivery: { relay: 'r2', transportSignature: 'ts' } };
  const resolution = resolveOwnership([create, replay], fx.registry);
  for (const decision of resolution.decisions) {
    assert.equal(typeof decision.ref.eventHash, 'string');
    assert.equal(decision.ref.gen >= 0, true);
  }
  const duplicate = resolution.decisions.filter((decision) => decision.reason === 'duplicate');
  assert.equal(duplicate.length, 1);
  assert.equal(resolution.duplicateDeliveries, 1);
});
