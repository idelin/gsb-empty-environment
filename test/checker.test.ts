import assert from 'node:assert/strict';
import { test } from 'node:test';

import { canonical, key } from '../src/canon.ts';
import { check } from '../src/checker.ts';
import { initialState, successors, violation } from '../src/model.ts';
import type { State } from '../src/model.ts';

const OPTS = { netCap: 3, buggy: false };

test('正确协议：状态空间穷尽，给出无限执行证明', () => {
  const r = check(OPTS);
  assert.equal(r.type, 'proof');
  assert.ok(r.explored > 0);
});

test('buggy 协议（允许同任期重复投票）：给出最短反例，且轨迹逐步合法', () => {
  const r = check({ ...OPTS, buggy: true });
  assert.equal(r.type, 'counterexample');
  if (r.type !== 'counterexample') return;

  const trace = r.trace;
  assert.ok(trace.length >= 2);

  // 末态确实违反安全性质
  assert.ok(violation(trace[trace.length - 1].state) !== null);

  // 轨迹中相邻状态间确实存在转移（按规范化后的键匹配）
  for (let i = 1; i < trace.length; i++) {
    const prev: State = trace[i - 1].state;
    const nextKey = key(trace[i].state);
    const ok = successors(prev, { ...OPTS, buggy: true }).some(
      (s) => key(s.state) === nextKey,
    );
    assert.ok(ok, `步骤 ${i} 不是合法转移: ${trace[i].label}`);
  }

  // BFS 最短性：深度 1 内不应存在反例
  const shallow = check({ ...OPTS, buggy: true }, { maxSteps: 1 });
  assert.equal(shallow.type, 'bounded');
});

test('步数截断：结论为 bounded 而非 proof', () => {
  const r = check(OPTS, { maxSteps: 2 });
  assert.equal(r.type, 'bounded');
});

test('状态数截断：结论为 bounded 而非 proof', () => {
  const r = check(OPTS, { maxStates: 10 });
  assert.equal(r.type, 'bounded');
});

test('规范化是幂等的，且初始状态规范化后无消息', () => {
  const c1 = canonical(initialState());
  const c2 = canonical(c1);
  assert.equal(key(c1), key(c2));
  assert.equal(c1.net.length, 0);
});
