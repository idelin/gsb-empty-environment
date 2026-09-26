import test from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { buildFixtureWorld } from '../src/fixtures.ts';
import { vec } from '../src/types.ts';
import type { ScrollEvent } from '../src/scroll-node.ts';

test('程序滚动回调带来源令牌，同令牌重入被阻止', () => {
  const world = buildFixtureWorld();
  const events: ScrollEvent[] = [];
  world.mustGet('inner').onScroll((e) => {
    events.push(e);
    if (e.source.kind === 'programmatic') {
      // 反馈尝试：回调内用同一令牌再次程序滚动 —— 必须被阻止
      const r = world.scrollTo('inner', vec('0', '999'), e.source.token);
      assert.ok(r.blocked, '同令牌重入应被阻止');
    }
  });

  const res = world.scrollTo('inner', vec('0', '100'));
  assert.ok(!res.blocked);
  assert.ok(world.mustGet('inner').scroll.y.eq(Rational.of('100')));
  assert.strictEqual(events.length, 1, '反馈被阻止，回调只触发一轮');
  assert.deepStrictEqual(events[0].source, { kind: 'programmatic', token: res.token });
});

test('isFeedback 让监听器主动忽略程序滚动', () => {
  const world = buildFixtureWorld();
  let userDriven = 0;
  let programmaticSeen = 0;
  world.mustGet('inner').onScroll((e) => {
    if (world.isFeedback(e.source)) {
      programmaticSeen++;
      return;
    }
    userDriven++;
  });

  world.scrollTo('inner', vec('0', '60'));
  assert.strictEqual(programmaticSeen, 1);
  assert.strictEqual(userDriven, 0);

  world.dispatchWheel('inner', vec('0', '10'));
  assert.strictEqual(userDriven, 1, '用户滚轮不被误判为反馈');
  assert.strictEqual(programmaticSeen, 1);
});

test('重入回调枚举：回调内再次 scrollTo 不递归触发反馈', () => {
  const world = buildFixtureWorld();
  let calls = 0;
  world.mustGet('inner').onScroll((e) => {
    calls++;
    if (calls > 10) throw new Error('feedback runaway');
    const token = e.source.kind === 'programmatic' ? e.source.token : undefined;
    world.scrollTo('inner', vec('0', '200'), token);
  });
  world.scrollTo('inner', vec('0', '100'));
  assert.strictEqual(calls, 1, '同令牌重入被挡，不递归');
  assert.ok(world.mustGet('inner').scroll.y.eq(Rational.of('100')), '被阻止的重入不改变位置');
});

test('程序滚动不进入滚轮事务流', () => {
  const world = buildFixtureWorld();
  const before = world.transactions.length;
  world.scrollTo('inner', vec('0', '42'));
  assert.strictEqual(world.transactions.length, before, '程序滚动不产生滚轮事务');
});
