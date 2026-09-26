import test from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { ScrollNode } from '../src/scroll-node.ts';
import { ScrollWorld } from '../src/world.ts';
import type { Axis, OverscrollPolicy } from '../src/types.ts';
import { vec, vecGet, vecSet, vecZero } from '../src/types.ts';
import { assertConservation } from './helpers.ts';

const POSITIONS: Readonly<Record<string, string>> = { start: '0', middle: '50', end: '100' };
const DELTAS = ['30', '-30'] as const;
const POLICIES: readonly OverscrollPolicy[] = ['chain', 'lock'];
const AXIS_LIST: readonly Axis[] = ['x', 'y'];
const MAX_SCROLL = Rational.of('100');

/** 精确有理参考：独立重算每层应有的消费/传递/舍弃。 */
function reference(position: Rational, delta: Rational, policy: OverscrollPolicy) {
  let next = position.add(delta);
  if (next.sign() < 0) next = Rational.zero();
  if (next.cmp(MAX_SCROLL) > 0) next = MAX_SCROLL;
  const consumed = next.sub(position);
  const rest = delta.sub(consumed);
  const discarded = policy === 'lock' ? rest : Rational.zero();
  const passed = rest.sub(discarded);
  return { consumed, passed, discarded, next };
}

test('枚举：边界位置 × 方向 × 锁定策略 × 轴，对照精确有理参考', () => {
  for (const axis of AXIS_LIST) {
    for (const [posName, posText] of Object.entries(POSITIONS)) {
      for (const deltaText of DELTAS) {
        for (const policy of POLICIES) {
          const root = new ScrollNode('root', {
            viewport: vec('100', '100'),
            content: vec('400', '400'),
            axes: ['x', 'y'],
          });
          const node = new ScrollNode('node', {
            viewport: vec('100', '100'),
            content: vec('200', '200'),
            axes: ['x', 'y'],
            overscroll: { [axis]: policy },
          });
          root.addChild(node);
          const world = new ScrollWorld(root);

          const position = Rational.of(posText);
          node.scroll = vecSet(vecZero(), axis, position);
          const delta = vecSet(vecZero(), axis, Rational.of(deltaText));
          const tx = world.dispatchWheel('node', delta);

          const ref = reference(position, Rational.of(deltaText), policy);
          const layer = tx.layers[0];
          const label = `${axis}@${posName} d=${deltaText} ${policy}`;
          assert.ok(vecGet(layer.consumed, axis).eq(ref.consumed), `${label}: consumed`);
          assert.ok(vecGet(layer.passed, axis).eq(ref.passed), `${label}: passed`);
          assert.ok(vecGet(layer.discarded, axis).eq(ref.discarded), `${label}: discarded`);
          assert.ok(vecGet(node.scroll, axis).eq(ref.next), `${label}: final position`);

          if (ref.passed.isZero()) {
            assert.strictEqual(tx.layers.length, 1, `${label}: 锁定或无剩余时不下传`);
          } else {
            assert.strictEqual(tx.layers.length, 2, `${label}: 剩余量传到外层`);
            assert.ok(vecGet(tx.layers[1].received, axis).eq(ref.passed), `${label}: outer received`);
          }
          assertConservation(tx);
        }
      }
    }
  }
});

test('边界锁定：剩余量在本层舍弃，外层收不到', () => {
  const world = buildLocked();
  const tx = world.dispatchWheel('locked', vec('0', '50'));
  assert.strictEqual(tx.layers.length, 1);
  assert.ok(tx.layers[0].consumed.y.eq(Rational.of('50')));
  assert.ok(tx.escaped.y.isZero());
  assertConservation(tx);

  world.scrollTo('locked', vec('0', '240')); // locked maxScroll = 360-120 = 240，到顶
  const tx2 = world.dispatchWheel('locked', vec('0', '30'));
  assert.strictEqual(tx2.layers.length, 1);
  assert.ok(tx2.layers[0].consumed.y.isZero());
  assert.ok(tx2.layers[0].discarded.y.eq(Rational.of('30')), '触边界后剩余量被锁定舍弃');
  assert.ok(world.mustGet('outer').scroll.y.isZero(), '外层收不到被锁定的增量');
  assertConservation(tx2);
});

function buildLocked(): ScrollWorld {
  const root = new ScrollNode('root', {
    viewport: vec('400', '300'),
    content: vec('400', '900'),
    axes: ['y'],
  });
  const outer = new ScrollNode('outer', {
    viewport: vec('360', '260'),
    content: vec('360', '780'),
    axes: ['y'],
  });
  const locked = new ScrollNode('locked', {
    viewport: vec('120', '120'),
    content: vec('120', '360'),
    axes: ['y'],
    overscroll: { y: 'lock' },
  });
  root.addChild(outer);
  outer.addChild(locked);
  return new ScrollWorld(root);
}
