import { test } from 'node:test';
import assert from 'node:assert/strict';
import { solve, matchPattern, type Rule, type Sample } from '../src/solver.ts';

const rules = (defs: [string, 'allow' | 'deny', string][]): Rule[] =>
  defs.map(([id, action, pattern]) => ({ id, action, pattern }));

test('通配符匹配', () => {
  assert.ok(matchPattern('user:*', 'user:123'));
  assert.ok(matchPattern('a?c', 'abc'));
  assert.ok(!matchPattern('user:*', 'admin:1'));
  assert.ok(matchPattern('*', 'anything'));
});

test('已满足时交换次数为 0', () => {
  const r = rules([
    ['r1', 'deny', 'admin:*'],
    ['r2', 'allow', '*'],
  ]);
  const s: Sample[] = [
    { id: 's1', input: 'admin:root', expect: 'deny' },
    { id: 's2', input: 'user:1', expect: 'allow' },
  ];
  const res = solve(r, s);
  assert.ok(res.ok);
  assert.equal(res.swaps, 0);
  assert.deepEqual(res.order, ['r1', 'r2']);
  assert.equal(res.hits.find((h) => h.sampleId === 's1')!.hitRuleId, 'r1');
  assert.equal(res.hits.find((h) => h.sampleId === 's2')!.hitRuleId, 'r2');
});

test('需要一次相邻交换', () => {
  // 原顺序 allow * 在前会拦截 admin，必须交换
  const r = rules([
    ['allow-all', 'allow', '*'],
    ['deny-admin', 'deny', 'admin:*'],
  ]);
  const s: Sample[] = [{ id: 's1', input: 'admin:root', expect: 'deny' }];
  const res = solve(r, s);
  assert.ok(res.ok);
  assert.equal(res.swaps, 1);
  assert.deepEqual(res.order, ['deny-admin', 'allow-all']);
  assert.equal(res.hits[0].hitRuleId, 'deny-admin');
});

test('最少交换：优先移动小代价规则', () => {
  // r1 allow a:*, r2 allow b:*, r3 deny *
  // 样例要求 b:1 命中 deny -> 需 r3 在 r2 前。
  // 方案A: r3 前移两位（2 次交换）；方案B: r2 后移一位（1 次交换）。应选 B。
  const r = rules([
    ['r1', 'allow', 'a:*'],
    ['r2', 'allow', 'b:*'],
    ['r3', 'deny', '*'],
  ]);
  const s: Sample[] = [{ id: 's1', input: 'b:1', expect: 'deny' }];
  const res = solve(r, s);
  assert.ok(res.ok);
  assert.equal(res.swaps, 1);
  assert.deepEqual(res.order, ['r1', 'r3', 'r2']);
});

test('无解时给出极小冲突子集', () => {
  // s1 要求 x 命中 allow（r1 在前），s2 要求 x 命中 deny（r2 在前），矛盾
  const r = rules([
    ['r1', 'allow', 'x'],
    ['r2', 'deny', 'x'],
  ]);
  const s: Sample[] = [
    { id: 's0', input: 'y', expect: 'allow' }, // 无规则匹配 y？不，加一条兜底
    { id: 's1', input: 'x', expect: 'allow' },
    { id: 's2', input: 'x', expect: 'deny' },
  ];
  const r2 = [...r, { id: 'r3', action: 'allow' as const, pattern: 'y' }];
  const res = solve(r2, s);
  assert.ok(!res.ok);
  const ids = res.conflictSamples.map((c) => c.id).sort();
  assert.deepEqual(ids, ['s1', 's2']); // 极小冲突对，s0 被剔除
});

test('无匹配规则的样例直接报冲突', () => {
  const r = rules([['r1', 'allow', 'a']]);
  const s: Sample[] = [{ id: 's1', input: 'zzz', expect: 'deny' }];
  const res = solve(r, s);
  assert.ok(!res.ok);
  assert.equal(res.conflictSamples.length, 1);
  assert.match(res.reason, /没有任何规则匹配/);
});

test('多条样例共享命中，顺序正确', () => {
  const r = rules([
    ['allow-all', 'allow', '*'],
    ['deny-evil', 'deny', 'evil:*'],
    ['deny-admin-del', 'deny', 'admin:delete'],
  ]);
  const s: Sample[] = [
    { id: 's1', input: 'evil:1', expect: 'deny' },
    { id: 's2', input: 'admin:delete', expect: 'deny' },
    { id: 's3', input: 'user:1', expect: 'allow' },
  ];
  const res = solve(r, s);
  assert.ok(res.ok);
  assert.equal(res.swaps, 2); // 两条 deny 各越过 allow-all
  assert.deepEqual(res.order, ['deny-evil', 'deny-admin-del', 'allow-all']);
  assert.equal(res.hits.find((h) => h.sampleId === 's3')!.hitRuleId, 'allow-all');
});

test('15 条规则上限内可解', () => {
  const defs: [string, 'allow' | 'deny', string][] = [];
  for (let i = 0; i < 15; i++) defs.push([`r${i}`, i % 2 ? 'deny' : 'allow', `p${i}:*`]);
  const r = rules(defs);
  const s: Sample[] = [{ id: 's1', input: 'p14:x', expect: 'allow' }];
  const res = solve(r, s);
  assert.ok(res.ok);
});

test('超过 15 条规则报错', () => {
  const defs: [string, 'allow' | 'deny', string][] = [];
  for (let i = 0; i < 16; i++) defs.push([`r${i}`, 'allow', `p${i}`]);
  assert.throws(() => solve(rules(defs), []), /超过上限 15/);
});
