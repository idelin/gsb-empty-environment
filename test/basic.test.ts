// 模式语义：窗口两端包含、C 的位置（含端点同时间）、多对组合、稳定告警 ID。

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

function run(log: InputRecord[], W = 10, L = 5) {
  const engine = new PatternEngine(W, L);
  const changes = engine.applyAll(log);
  return { engine, changes };
}

test('窗口两端均包含：B.time - A.time ∈ [0, W]', () => {
  const { engine } = run([up('a1', 'A', 0), up('b1', 'B', 10)], 10);
  assert.ok(engine.effectiveAlerts().has(alertIdOf('a1', 'b1')), 'B 在窗口右端点应匹配');

  const { engine: e2 } = run([up('a1', 'A', 0), up('b1', 'B', 11)], 10);
  assert.equal(e2.effectiveAlerts().size, 0, 'B 超出窗口不匹配');

  const { engine: e3 } = run([up('a1', 'A', 5), up('b1', 'B', 5)], 10);
  assert.ok(e3.effectiveAlerts().has(alertIdOf('a1', 'b1')), 'A、B 同时间（距离 0）应匹配');

  const { engine: e4 } = run([up('a1', 'A', 5), up('b1', 'B', 4)], 10);
  assert.equal(e4.effectiveAlerts().size, 0, 'B 早于 A 不匹配');
});

test('C 在 [A, B] 内（含与端点同时间）使匹配无效', () => {
  // C 在 A、B 之间
  const { engine: e1 } = run([up('a1', 'A', 0), up('c1', 'C', 3), up('b1', 'B', 5)]);
  assert.equal(e1.effectiveAlerts().size, 0);

  // C 与 A 同时间
  const { engine: e2 } = run([up('a1', 'A', 0), up('c1', 'C', 0), up('b1', 'B', 5)]);
  assert.equal(e2.effectiveAlerts().size, 0, 'C 与 A 同时间应否决');

  // C 与 B 同时间
  const { engine: e3 } = run([up('a1', 'A', 0), up('c1', 'C', 5), up('b1', 'B', 5)]);
  assert.equal(e3.effectiveAlerts().size, 0, 'C 与 B 同时间应否决');

  // C 在 A 之前或 B 之后不影响
  const { engine: e4 } = run([
    up('c0', 'C', 0),
    up('a1', 'A', 5),
    up('b1', 'B', 10),
    up('c9', 'C', 11),
  ]);
  assert.ok(e4.effectiveAlerts().has(alertIdOf('a1', 'b1')));
});

test('C 只否决其时间之后的 B，之前的 B 仍有效', () => {
  const { engine } = run([
    up('a1', 'A', 0),
    up('b1', 'B', 2),
    up('c1', 'C', 4),
    up('b2', 'B', 6),
  ]);
  const alerts = engine.effectiveAlerts();
  assert.ok(alerts.has(alertIdOf('a1', 'b1')), 'C 之前的 B 保留');
  assert.ok(!alerts.has(alertIdOf('a1', 'b2')), 'C 之后的 B 被否决');
});

test('多个 A/B 产生全部组合，告警 ID 稳定', () => {
  const log = [up('a1', 'A', 0), up('a2', 'A', 1), up('b1', 'B', 5), up('b2', 'B', 6)];
  const { engine, changes } = run(log);
  const alerts = engine.effectiveAlerts();
  assert.equal(alerts.size, 4);
  for (const a of ['a1', 'a2'])
    for (const b of ['b1', 'b2']) assert.ok(alerts.has(alertIdOf(a, b)), `${a}/${b}`);
  // 变更流中的告警 ID 与最终集合一致
  const upsertIds = new Set(changes.filter((c) => c.type === 'upsert').map((c) => c.alertId));
  assert.deepStrictEqual(upsertIds, new Set(alerts.keys()));
});

test('同 tick 多事件：与参考计算一致', () => {
  const log = [
    up('a2', 'A', 7),
    up('a1', 'A', 7),
    up('b2', 'B', 7),
    up('b1', 'B', 7),
    up('c1', 'C', 7),
    up('b3', 'B', 8),
  ];
  const { engine } = run(log);
  const ref = referenceAlerts(log, 10, 5);
  assertSameAlerts(engine.effectiveAlerts(), ref.alerts, '同 tick 混合事件');
  // C@7 否决所有 B.time >= 7 的匹配，故应为空
  assert.equal(engine.effectiveAlerts().size, 0);
});

test('基本语义对拍：流式 == 一次性参考', () => {
  const log = [
    up('a1', 'A', 0),
    up('b1', 'B', 3),
    up('c1', 'C', 4),
    up('b2', 'B', 2),
    up('a2', 'A', 6),
    up('b3', 'B', 16),
    up('b4', 'B', 17),
  ];
  const { engine } = run(log, 10, 5);
  const ref = referenceAlerts(log, 10, 5);
  assertSameAlerts(engine.effectiveAlerts(), ref.alerts, '基本对拍');
});
