// 修订语义：改类型/时间/撤销、幂等重复、同 rev 冲突、迟到 C 与恢复 upsert。

import { test } from 'node:test';
import { strict as assert } from 'node:assert';
import { PatternEngine } from '../src/engine.ts';
import { referenceAlerts } from '../src/reference.ts';
import { alertIdOf } from '../src/types.ts';
import type { InputRecord, UpsertRecord } from '../src/types.ts';
import { assertSameAlerts } from './helpers.ts';

const up = (id: string, kind: 'A' | 'B' | 'C', time: number, rev = 1): UpsertRecord => ({
  type: 'upsert',
  id,
  rev,
  kind,
  time,
});
const del = (id: string, time: number, rev: number): InputRecord => ({
  type: 'retract',
  id,
  rev,
  time,
});

test('更高修订改类型：B→C 产生 retract，改回 B 产生 upsert（同告警 ID）', () => {
  const e = new PatternEngine(10, 100);
  e.applyAll([up('a1', 'A', 0), up('x', 'B', 5)]);
  assert.ok(e.effectiveAlerts().has(alertIdOf('a1', 'x')));

  const c1 = e.apply(up('x', 'C', 5, 2)); // B 变 C：匹配失效
  assert.deepStrictEqual(c1, [{ type: 'retract', alertId: alertIdOf('a1', 'x') }]);
  assert.equal(e.effectiveAlerts().size, 0);

  const c2 = e.apply(up('x', 'B', 5, 3)); // 恢复有效：upsert
  assert.deepStrictEqual(c2, [
    { type: 'upsert', alertId: alertIdOf('a1', 'x'), aId: 'a1', bId: 'x' },
  ]);
  assert.ok(e.effectiveAlerts().has(alertIdOf('a1', 'x')));
});

test('更高修订改时间：移出窗口 retract，移回窗口 upsert', () => {
  const e = new PatternEngine(10, 100);
  e.applyAll([up('a1', 'A', 0), up('b1', 'B', 5)]);
  assert.equal(e.effectiveAlerts().size, 1);

  e.apply(up('b1', 'B', 50, 2)); // 移出窗口
  assert.equal(e.effectiveAlerts().size, 0);

  e.apply(up('b1', 'B', 8, 3)); // 移回窗口
  assert.ok(e.effectiveAlerts().has(alertIdOf('a1', 'b1')));
});

test('撤销 A/B 产生 retract；更高修订恢复产生 upsert', () => {
  const e = new PatternEngine(10, 100);
  e.applyAll([up('a1', 'A', 0), up('b1', 'B', 5), up('b2', 'B', 6)]);
  assert.equal(e.effectiveAlerts().size, 2);

  const c1 = e.apply(del('b1', 5, 2)); // 撤销 B
  assert.deepStrictEqual(c1, [{ type: 'retract', alertId: alertIdOf('a1', 'b1') }]);
  assert.equal(e.effectiveAlerts().size, 1);

  const c2 = e.apply(del('a1', 0, 2)); // 撤销 A：其余告警全部 retract
  assert.deepStrictEqual(c2, [{ type: 'retract', alertId: alertIdOf('a1', 'b2') }]);
  assert.equal(e.effectiveAlerts().size, 0);

  e.apply(up('a1', 'A', 0, 3)); // 恢复 A：b2 仍在，重新匹配
  assert.ok(e.effectiveAlerts().has(alertIdOf('a1', 'b2')));
});

test('迟到的 C（在允许迟到范围内）使已建立告警 retract', () => {
  const e = new PatternEngine(10, 100);
  e.applyAll([up('a1', 'A', 0), up('b1', 'B', 5)]);
  assert.equal(e.effectiveAlerts().size, 1);

  const changes = e.apply(up('c1', 'C', 3)); // 乱序迟到的 C
  assert.deepStrictEqual(changes, [{ type: 'retract', alertId: alertIdOf('a1', 'b1') }]);
  assert.equal(e.effectiveAlerts().size, 0);

  const back = e.apply(del('c1', 3, 2)); // 再撤销 C：恢复有效
  assert.deepStrictEqual(back, [
    { type: 'upsert', alertId: alertIdOf('a1', 'b1'), aId: 'a1', bId: 'b1' },
  ]);
});

test('同 rev 同内容幂等；同 rev 不同内容冲突且不改状态；低 rev 过期忽略', () => {
  const e = new PatternEngine(10, 100);
  e.applyAll([up('a1', 'A', 0), up('b1', 'B', 5)]);
  const before = e.effectiveAlerts();

  const dup = e.apply(up('b1', 'B', 5, 1)); // 完全重复
  assert.deepStrictEqual(dup, []);
  assert.equal(e.stats.idempotentDuplicates, 1);

  const conflict = e.apply(up('b1', 'C', 9, 1)); // 同 rev 不同内容
  assert.deepStrictEqual(conflict, []);
  assert.equal(e.stats.conflicts, 1);
  assertSameAlerts(e.effectiveAlerts(), before, '冲突不得改变状态');

  const stale = e.apply(up('b1', 'C', 2, 0)); // 低 rev
  assert.deepStrictEqual(stale, []);
  assert.equal(e.stats.staleRevisions, 1);
  assertSameAlerts(e.effectiveAlerts(), before, '过期修订不得改变状态');

  const conflictRetract = e.apply(del('b1', 5, 1)); // 同 rev 的 retract 与 upsert 冲突
  assert.deepStrictEqual(conflictRetract, []);
  assert.equal(e.stats.conflicts, 2);
});

test('重复撤销幂等', () => {
  const e = new PatternEngine(10, 100);
  e.applyAll([up('a1', 'A', 0), up('b1', 'B', 5), del('b1', 5, 2)]);
  assert.equal(e.effectiveAlerts().size, 0);
  const again = e.apply(del('b1', 5, 2));
  assert.deepStrictEqual(again, []);
  assert.equal(e.stats.idempotentDuplicates, 1);
});

test('修订场景对拍：流式 == 一次性参考', () => {
  const log: InputRecord[] = [
    up('a1', 'A', 0),
    up('b1', 'B', 5),
    up('b1', 'C', 5, 2), // B→C
    up('a2', 'A', 1),
    up('b2', 'B', 8),
    del('b2', 8, 2), // 撤销 b2
    up('b1', 'B', 6, 3), // C→B 且改时间
    up('c1', 'C', 0), // 与 a1 同时间的 C
    up('c1', 'C', 100, 2), // C 移出所有窗口
    up('a1', 'A', 2, 2), // A 改时间
  ];
  const e = new PatternEngine(10, 100);
  const changes = e.applyAll(log);
  const ref = referenceAlerts(log, 10, 100);
  assertSameAlerts(e.effectiveAlerts(), ref.alerts, '修订对拍');
  assert.deepStrictEqual(e.stats, ref.stats, '接收统计应一致');
  assert.ok(changes.length > 0);
});
