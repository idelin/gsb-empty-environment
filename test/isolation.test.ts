import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  addAliasEnvelope,
  createEnvelope,
  rehostEnvelope,
  transferEnvelope,
  updateEnvelope,
} from '../src/ownership/factory.ts';
import { buildLookupIndex, resolveOwnership } from '../src/ownership/resolver.ts';
import type { Envelope } from '../src/ownership/types.ts';
import { buildFixture, decisionMap, permutations } from './helpers.ts';

test('分叉只隔离冲突对象及其后代，其他对象正常发布', () => {
  const fx = buildFixture();
  const parent = fx.objectId;
  const child = { origin: 'origin.example', permanentId: 'child-1' };
  const sibling = { origin: 'origin.example', permanentId: 'sib-1' };

  const parentCreate = createEnvelope(
    { id: parent, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'p' },
    { arrivedAt: 1 },
  );
  const childCreate = createEnvelope(
    { id: child, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'c', parentObject: parent },
    { arrivedAt: 2 },
  );
  const siblingCreate = createEnvelope(
    { id: sibling, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 's' },
    { arrivedAt: 3 },
  );

  const branchA = transferEnvelope(
    { id: parent, prev: parentCreate.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { arrivedAt: 4 },
  );
  const branchB = transferEnvelope(
    { id: parent, prev: parentCreate.eventHash, gen: 1, from: fx.alice, to: fx.carol },
    { arrivedAt: 5 },
  );
  const childUpdate = updateEnvelope(
    { id: child, prev: childCreate.eventHash, gen: 0, holder: fx.alice, fields: { k: 'v' } },
    { arrivedAt: 6 },
  );
  const siblingUpdate = updateEnvelope(
    { id: sibling, prev: siblingCreate.eventHash, gen: 0, holder: fx.alice, fields: { k: 'w' } },
    { arrivedAt: 7 },
  );

  const events: Envelope[] = [
    parentCreate,
    childCreate,
    siblingCreate,
    branchA,
    branchB,
    childUpdate,
    siblingUpdate,
  ];
  const resolution = resolveOwnership(events, fx.registry);
  assert.deepEqual([...resolution.isolated].sort(), [
    'origin.example#child-1',
    'origin.example#obj-1',
  ]);

  const decisions = decisionMap(resolution);
  assert.equal(decisions.get(childUpdate.eventHash)!.reason, 'wait_descendant_isolated');
  assert.equal(decisions.get(siblingUpdate.eventHash)!.kind, 'publish');

  const siblingView = resolution.views.get('origin.example#sib-1')!;
  assert.deepEqual(siblingView.fields, { k: 'w' });
  const childView = resolution.views.get('origin.example#child-1')!;
  assert.deepEqual(childView.fields, {});

  for (const ordering of permutations(events)) {
    const rerun = resolveOwnership(ordering, fx.registry);
    assert.deepEqual([...rerun.isolated].sort(), resolution.isolated);
  }
});

test('同一展示 URL 可被两个不同对象同时声明，索引返回两条且身份互不覆盖', () => {
  const fx = buildFixture();
  const id1 = { origin: 'a.example', permanentId: 'p1' };
  const id2 = { origin: 'b.example', permanentId: 'p2' };
  const create1 = createEnvelope(
    { id: id1, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'https://shared/u' },
    { arrivedAt: 1 },
  );
  const create2 = createEnvelope(
    { id: id2, holder: fx.bob, recoveryPolicy: fx.policy, displayUrl: 'https://shared/u' },
    { arrivedAt: 2 },
  );
  const resolution = resolveOwnership([create1, create2], fx.registry);
  const index = buildLookupIndex(resolution).filter((entry) => entry.kind === 'url');
  assert.equal(index.length, 2);
  assert.equal(new Set(index.map((entry) => entry.objectKey)).size, 2);

  // 重新托管只改变展示 URL，不改变身份。
  const rehost = rehostEnvelope(
    { id: id1, prev: create1.eventHash, gen: 0, holder: fx.alice, displayUrl: 'https://cdn/new' },
    { arrivedAt: 3 },
  );
  const moved = resolveOwnership([create1, create2, rehost], fx.registry);
  const movedView = moved.views.get('a.example#p1')!;
  assert.equal(movedView.displayUrl, 'https://cdn/new');
  assert.equal(movedView.id.origin, 'a.example');
});

test('更新、撤销、重托管、增加别名乱序到达，最终视图确定一致', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u0' },
    { arrivedAt: 1 },
  );
  const transfer = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { arrivedAt: 2 },
  );
  const update = updateEnvelope(
    { id: fx.objectId, prev: transfer.eventHash, gen: 1, holder: fx.bob, fields: { title: 'x' } },
    { arrivedAt: 3 },
  );
  const rehost = rehostEnvelope(
    { id: fx.objectId, prev: transfer.eventHash, gen: 1, holder: fx.bob, displayUrl: 'u1' },
    { arrivedAt: 4 },
  );
  const alias = addAliasEnvelope(
    { id: fx.objectId, prev: transfer.eventHash, gen: 1, holder: fx.bob, alias: 'friendly' },
    { arrivedAt: 5 },
  );
  const events: Envelope[] = [create, transfer, update, rehost, alias];
  const reference = resolveOwnership(events, fx.registry);
  const refView = [...reference.views.values()][0];
  for (const ordering of permutations(events)) {
    const rerun = resolveOwnership(ordering, fx.registry);
    const view = [...rerun.views.values()][0];
    assert.equal(view.displayUrl, refView.displayUrl);
    assert.deepEqual(view.aliases, refView.aliases);
    assert.deepEqual(view.fields, refView.fields);
  }
});
