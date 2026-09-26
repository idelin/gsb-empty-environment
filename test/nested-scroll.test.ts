import test from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { ScrollNode } from '../src/scroll-node.ts';
import { ScrollWorld } from '../src/world.ts';
import { buildFixtureWorld } from '../src/fixtures.ts';
import { vec } from '../src/types.ts';
import { assertConservation } from './helpers.ts';

test('内层未触边界时全部消费，外层收不到增量', () => {
  const world = buildFixtureWorld();
  const tx = world.dispatchWheel('inner', vec('0', '40'));
  assert.strictEqual(tx.layers.length, 1);
  assert.ok(tx.layers[0].consumed.y.eq(Rational.of('40')));
  assert.ok(world.mustGet('outer').scroll.y.isZero());
  assertConservation(tx);
});

test('内层到底后剩余量传递给外层', () => {
  const world = buildFixtureWorld();
  world.scrollTo('inner', vec('0', '340')); // inner maxScroll = 480-120 = 360
  const tx = world.dispatchWheel('inner', vec('0', '50'));
  const [innerLayer, outerLayer] = tx.layers;
  assert.strictEqual(tx.layers.length, 2);
  assert.ok(innerLayer.consumed.y.eq(Rational.of('20')));
  assert.ok(innerLayer.passed.y.eq(Rational.of('30')));
  assert.ok(outerLayer.received.y.eq(Rational.of('30')));
  assert.ok(outerLayer.consumed.y.eq(Rational.of('30')));
  assert.ok(world.mustGet('inner').scroll.y.eq(Rational.of('360')));
  assert.ok(world.mustGet('outer').scroll.y.eq(Rational.of('30')));
  assertConservation(tx);
});

test('横向可滚节点不误消费纵向增量', () => {
  const root = new ScrollNode('root', {
    viewport: vec('100', '100'),
    content: vec('100', '500'),
    axes: ['y'],
  });
  const hOnly = new ScrollNode('h', {
    viewport: vec('100', '100'),
    content: vec('500', '100'),
    axes: ['x'],
  });
  root.addChild(hOnly);
  const world = new ScrollWorld(root);

  const tx = world.dispatchWheel('h', vec('30', '50'));
  const [hLayer, rootLayer] = tx.layers;
  assert.ok(hLayer.consumed.x.eq(Rational.of('30')));
  assert.ok(hLayer.consumed.y.isZero(), '横向节点不得消费纵向增量');
  assert.ok(rootLayer.received.y.eq(Rational.of('50')), '纵向增量原样传给外层');
  assert.ok(rootLayer.received.x.isZero());
  assert.ok(hOnly.scroll.y.isZero());
  assert.ok(root.scroll.y.eq(Rational.of('50')));
  assertConservation(tx);
});

test('纵向节点收到混合增量时横向量原样穿透', () => {
  const world = buildFixtureWorld();
  const tx = world.dispatchWheel('inner', vec('30', '50'));
  const innerLayer = tx.layers[0];
  assert.ok(innerLayer.consumed.x.isZero());
  assert.ok(innerLayer.passed.x.eq(Rational.of('30')));
  assert.ok(innerLayer.consumed.y.eq(Rational.of('50')));
  assertConservation(tx);
});

test('反向小增量精确累计：可移动且能回到原点', () => {
  const node = new ScrollNode('n', {
    viewport: vec('100', '100'),
    content: vec('100', '200'),
    axes: ['y'],
  });
  const world = new ScrollWorld(node);

  for (let i = 0; i < 3; i++) world.dispatchWheel('n', vec('0', '1/3'));
  assert.ok(node.scroll.y.eq(Rational.of('1')), '三次 1/3 精确累计为 1');

  for (let i = 0; i < 3; i++) world.dispatchWheel('n', vec('0', '-1/3'));
  assert.ok(node.scroll.y.isZero(), '反向三次 1/3 精确回到 0');

  for (let i = 0; i < 10; i++) world.dispatchWheel('n', vec('0', '0.1'));
  assert.ok(node.scroll.y.eq(Rational.of('1')), '十次 0.1 精确累计为 1，无浮点残差');

  world.dispatchWheel('n', vec('0', '1/3'));
  world.dispatchWheel('n', vec('0', '-1/7'));
  assert.ok(node.scroll.y.eq(Rational.of('1').add(Rational.of('1/3')).sub(Rational.of('1/7'))));
});

test('轴变换每事务至多一次，纵量不会被重复消费', () => {
  const world = buildFixtureWorld();
  const tx = world.dispatchWheel('strip', vec('0', '60'));
  const stripLayer = tx.layers[0];
  assert.strictEqual(stripLayer.nodeId, 'strip');
  assert.ok(stripLayer.preTransform !== null, '变换 前的增量被记录');
  assert.ok(stripLayer.preTransform!.y.eq(Rational.of('60')));
  assert.ok(stripLayer.received.x.eq(Rational.of('60')), 'y→x 变换后作用在横轴');
  assert.ok(stripLayer.received.y.isZero());
  assert.ok(stripLayer.consumed.x.eq(Rational.of('60')));
  assert.strictEqual(tx.layers.filter((l) => l.preTransform !== null).length, 1);
  assert.ok(world.mustGet('strip').scroll.x.eq(Rational.of('60')));
  assertConservation(tx);
});

test('轴变换后的剩余量沿链传递且不再次变换', () => {
  const world = buildFixtureWorld();
  world.scrollTo('strip', vec('430', '0')); // strip maxScroll.x = 640-200 = 440
  const tx = world.dispatchWheel('strip', vec('0', '50'));
  const [stripLayer, outerLayer] = tx.layers;
  assert.ok(stripLayer.consumed.x.eq(Rational.of('10')));
  assert.ok(stripLayer.passed.x.eq(Rational.of('40')));
  assert.ok(outerLayer.received.x.eq(Rational.of('40')), '剩余量保持变换后的轴向');
  assert.ok(outerLayer.preTransform === null, '变换不重复应用');
  assertConservation(tx);
});
