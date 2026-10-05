// 水位线：单调性、允许迟到边界、封闭归档、封闭区修订拒绝、热状态有界。

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
const wm = (time: number): InputRecord => ({ type: 'watermark', time });

test('水位线单调：较低或相等的水位线被忽略', () => {
  const e = new PatternEngine(10, 5);
  e.apply(wm(100));
  assert.equal(e.currentWatermark, 100);
  e.apply(wm(90));
  e.apply(wm(100));
  assert.equal(e.currentWatermark, 100);
  e.apply(wm(101));
  assert.equal(e.currentWatermark, 101);
});

test('允许迟到边界：time == wm - L 接受，time == wm - L - 1 拒绝', () => {
  const W = 10;
  const L = 5;
  const e = new PatternEngine(W, L);
  e.applyAll([up('a1', 'A', 0), wm(100)]);

  const atBoundary = e.apply(up('b1', 'B', 95)); // 95 == 100 - 5：恰好不迟到
  assert.equal(e.stats.rejectedLate, 0);
  assert.equal(atBoundary.length, 0, 'b1 不在任何窗口内，仅应被接收');
  assert.equal(e.stats.accepted, 2);

  e.apply(up('b2', 'B', 94)); // 94 < 100 - 5：迟到，拒绝
  assert.equal(e.stats.rejectedLate, 1);
  assert.equal(e.stats.accepted, 2, '被拒绝的修订不改变状态');

  // 边界上的事件仍可参与匹配：A@95 的窗口 [95,105] 包含 b1@95（距离 0）
  e.apply(up('a2', 'A', 95));
  assert.ok(e.effectiveAlerts().has(alertIdOf('a2', 'b1')));
  assert.ok(!e.effectiveAlerts().has(alertIdOf('a2', 'b2')), '被拒绝的事件不参与匹配');
});

test('迟到的撤销同样被拒绝且不改变状态', () => {
  const e = new PatternEngine(10, 5);
  e.applyAll([up('a1', 'A', 0), up('b1', 'B', 5), wm(1000)]);
  const before = e.effectiveAlerts();
  const changes = e.apply({ type: 'retract', id: 'b1', rev: 2, time: 5 });
  assert.deepStrictEqual(changes, []);
  assert.equal(e.stats.rejectedLate, 1);
  assertSameAlerts(e.effectiveAlerts(), before, '迟到撤销不得生效');
});

test('封闭告警归档但保留在有效集合；封闭区修订被拒绝', () => {
  const W = 10;
  const L = 5;
  const e = new PatternEngine(W, L);
  e.applyAll([up('a1', 'A', 0), up('b1', 'B', 5)]);
  assert.equal(e.openAlertCount, 1);

  // horizon = 100 - 5 - 10 = 85 > a1.time：告警封闭
  e.apply(wm(100));
  assert.equal(e.openAlertCount, 0, '开放告警已归档');
  assert.equal(e.archivedAlertCount, 1);
  assert.ok(e.effectiveAlerts().has(alertIdOf('a1', 'b1')), '归档告警仍有效');
  assert.equal(e.hotEventCount, 0, '封闭区事件已移出热索引');

  // 封闭区事件的修订（即使新时间在窗口内）被拒绝：封闭即最终
  const c1 = e.apply(up('a1', 'A', 99, 2));
  assert.deepStrictEqual(c1, []);
  assert.equal(e.stats.rejectedLate, 1);
  const c2 = e.apply(up('b1', 'C', 99, 2));
  assert.deepStrictEqual(c2, []);
  assert.equal(e.stats.rejectedLate, 2);
  assert.ok(e.effectiveAlerts().has(alertIdOf('a1', 'b1')), '封闭告警不受修订影响');
});

test('仍影响开放窗口的事件不被丢弃', () => {
  const W = 10;
  const L = 5;
  const e = new PatternEngine(W, L);
  // wm=100 时 horizon = 100-5-10 = 85：a1@94、a2@89、c1@93 都 >= 85，必须保留
  e.applyAll([up('a1', 'A', 94), up('a2', 'A', 89), up('c1', 'C', 93), wm(100)]);
  assert.equal(e.hotEventCount, 3, '开放窗口影响范围内的事件保留在热索引');

  // 新到的 b1@99：与 a1@94 匹配（距离 5，且 [94,99] 内无 C）；
  // 与 a2@89 虽距离 10 在窗口内，但 c1@93 ∈ [89,99] 使其无效（C 必须仍参与判定）。
  e.apply(up('b1', 'B', 99));
  assert.ok(e.effectiveAlerts().has(alertIdOf('a1', 'b1')), '保留的 A 仍应产生匹配');
  assert.ok(!e.effectiveAlerts().has(alertIdOf('a2', 'b1')), '保留的 C 仍应否决匹配');

  // 水位线推进到封闭线越过所有事件后，热索引清空、告警归档保留
  e.apply(wm(200));
  assert.equal(e.hotEventCount, 0);
  assert.ok(e.effectiveAlerts().has(alertIdOf('a1', 'b1')), '归档后有效集合不变');
});

test('水位线场景对拍：流式 == 一次性参考', () => {
  const log: InputRecord[] = [
    up('a1', 'A', 0),
    up('b1', 'B', 5),
    wm(50),
    up('b2', 'B', 46), // 恰好 wm - L = 45 之内
    up('a2', 'A', 44), // 44 < 45：迟到拒绝
    up('c1', 'C', 47),
    wm(200),
    up('a3', 'A', 196),
    up('b3', 'B', 205),
    up('a1', 'A', 199, 2), // 封闭区修订：拒绝
    up('b4', 'B', 194), // 194 < 195：迟到拒绝
  ];
  const e = new PatternEngine(10, 5);
  e.applyAll(log);
  const ref = referenceAlerts(log, 10, 5);
  assertSameAlerts(e.effectiveAlerts(), ref.alerts, '水位线对拍');
  assert.deepStrictEqual(e.stats, ref.stats, '接收统计应一致');
  assert.ok(e.archivedAlertCount > 0, '应有归档告警');
});
