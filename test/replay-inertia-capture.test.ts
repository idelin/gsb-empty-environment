import test from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { buildFixtureWorld } from '../src/fixtures.ts';
import { vec, vecEq } from '../src/types.ts';
import { assertConservation } from './helpers.ts';

test('事务可重放：逐层记录与末态完全一致', () => {
  const world = buildFixtureWorld();
  world.scrollTo('inner', vec('0', '340'));
  const tx1 = world.dispatchWheel('inner', vec('0', '50')); // 内层到底 → 外层
  const tx2 = world.dispatchWheel('strip', vec('0', '60')); // 轴变换
  const tx3 = world.dispatchWheel('both', vec('30', '40')); // 双轴

  for (const tx of [tx1, tx2, tx3]) {
    const { tx: reTx, identical } = world.replay(tx.id);
    assert.ok(reTx.id !== tx.id, '重放产生新事务 id');
    assert.ok(identical, `tx#${tx.id} 重放记录不一致`);
    assertConservation(reTx);
  }
});

test('重放后的世界状态等于原事务后的状态', () => {
  const world = buildFixtureWorld();
  const tx = world.dispatchWheel('inner', vec('0', '100'));
  const afterOriginal = world.snapshot();
  world.dispatchWheel('inner', vec('0', '25')); // 干扰后续状态
  const { identical } = world.replay(tx.id);
  assert.ok(identical);
  const afterReplay = world.snapshot();
  for (const [id, pos] of afterOriginal) {
    assert.ok(vecEq(afterReplay.get(id)!, pos), `${id} 重放后位置不一致`);
  }
});

test('惯性：衰减序列独立成事务，原增量不被重复消费', () => {
  const world = buildFixtureWorld();
  const userTx = world.dispatchWheel('inner', vec('0', '40'));
  const inertiaTxs = world.startInertia('inner', vec('0', '32'), {
    decay: Rational.of('1/2'),
    maxSteps: 5,
    originTx: userTx.id,
  });

  assert.strictEqual(inertiaTxs.length, 5);
  const expectedSteps = ['32', '16', '8', '4', '2'];
  inertiaTxs.forEach((tx, i) => {
    assert.deepStrictEqual(tx.source, { kind: 'inertia', of: userTx.id });
    assert.ok(tx.initialDelta.y.eq(Rational.of(expectedSteps[i])), `惯性步 ${i} 的增量`);
    assertConservation(tx);
  });

  // 位置 = 用户增量 + 惯性增量之和，每个增量只交付一次
  const total = inertiaTxs.reduce((acc, tx) => acc.add(tx.initialDelta.y), Rational.of('40'));
  assert.ok(world.mustGet('inner').scroll.y.eq(total));
  assert.ok(world.mustGet('inner').scroll.y.eq(Rational.of('102')));
});

test('捕获容器变化：剩余增量重定向，已访问节点不重复消费', () => {
  const world = buildFixtureWorld();
  world.mustGet('inner').onScroll((e) => {
    if (e.source.kind === 'user') world.requestCapture('both');
  });

  // inner 余量 360，both 余量 300，共 660；增量 700 → outer 收 40
  const tx = world.dispatchWheel('inner', vec('0', '700'));
  const ids = tx.layers.map((l) => l.nodeId);
  assert.deepStrictEqual(ids, ['inner', 'both', 'outer']);
  assert.ok(!tx.layers[0].rerouted);
  assert.ok(tx.layers[1].rerouted, '重定向到达的层被标记');
  assert.ok(tx.layers[1].consumed.y.eq(Rational.of('300')));
  assert.ok(tx.layers[2].consumed.y.eq(Rational.of('40')));
  assert.ok(tx.escaped.y.isZero());
  assert.strictEqual(world.captureNodeId, 'both', '捕获容器已切换');
  assertConservation(tx);

  // 捕获生效期间，滚轮改派给捕获容器
  const tx2 = world.dispatchWheel('inner', vec('0', '10'));
  assert.strictEqual(tx2.layers[0].nodeId, 'both');
  assertConservation(tx2);
  world.releaseCapture();
});

test('捕获重定向到已访问节点：跳过 visited，增量沿其祖先链继续', () => {
  const world = buildFixtureWorld();
  world.mustGet('inner').onScroll((e) => {
    if (e.source.kind === 'user') world.requestCapture('inner'); // 重定向回自身
  });
  const tx = world.dispatchWheel('inner', vec('0', '400')); // inner 余量 360
  const ids = tx.layers.map((l) => l.nodeId);
  assert.deepStrictEqual(ids, ['inner', 'outer'], 'inner 不重复出现');
  assert.ok(tx.layers[0].consumed.y.eq(Rational.of('360')));
  assert.ok(tx.layers[1].received.y.eq(Rational.of('40')));
  assertConservation(tx);
});
