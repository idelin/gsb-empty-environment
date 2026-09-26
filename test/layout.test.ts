import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ByteCache } from '../src/cache.ts';
import { FontRegistry } from '../src/fontRegistry.ts';
import { TextMeasurer } from '../src/measure.ts';
import { LayoutEngine } from '../src/layout.ts';
import { monoV1, sampleTree, serifV1 } from '../src/fixtures.ts';
import type { LayoutNode } from '../src/types.ts';

function makeEngine(maxPasses = 4) {
  const registry = new FontRegistry([serifV1, monoV1]);
  const cache = new ByteCache(500_000, { font: 100_000, cluster: 150_000, measure: 250_000 });
  const measurer = new TextMeasurer(registry, cache);
  return { measurer, engine: new LayoutEngine(measurer, maxPasses) };
}

test('无反馈：单轮布局测完全部文本节点', () => {
  const { engine } = makeEngine();
  const result = engine.layout(sampleTree);
  assert.equal(result.passes, 1);
  assert.equal(result.converged, true);
  assert.equal(result.measured.size, 3); // Hello / World! / ok
  assert.equal(result.requestedKeys.length, 3);
});

test('布局反馈收敛：第二轮后停止', () => {
  const { engine } = makeEngine();
  let calls = 0;
  const result = engine.layout(sampleTree, () => (++calls >= 2 ? null : sampleTree));
  assert.equal(result.passes, 2);
  assert.equal(result.converged, true);
});

test('布局反馈不得形成无界循环：永不收敛时受 maxPasses 约束', () => {
  const { engine, measurer } = makeEngine(4);
  // 反馈每轮改变字号 -> 每轮产生全新测量键，若不加约束将无限循环
  const feedback = (pass: { pass: number }): LayoutNode => ({
    kind: 'text',
    text: 'Hello',
    style: { fontId: 'serif', fontSize: 16 + pass.pass, letterSpacing: 0 },
  });
  const root: LayoutNode = {
    kind: 'text',
    text: 'Hello',
    style: { fontId: 'serif', fontSize: 16, letterSpacing: 0 },
  };
  const result = engine.layout(root, feedback);
  assert.equal(result.converged, false);
  assert.equal(result.passes, 4); // 严格有界
  assert.equal(result.requestedKeys.length, 4); // 每轮一个新键，总量有界
  // 调度器任务数同样有界
  assert.ok(measurer.scheduler.executorCalls <= 4 * 3); // 每轮至多 font+cluster+measure
});

test('反馈复用缓存：相同键不重复测量', () => {
  const { engine, measurer } = makeEngine(8);
  let calls = 0;
  // 反馈返回同一棵树：所有键命中缓存，第二轮无新任务
  const result = engine.layout(sampleTree, () => (++calls >= 4 ? null : sampleTree));
  assert.equal(result.passes, 4);
  assert.equal(result.converged, true);
  assert.equal(result.requestedKeys.length, 3); // 键集合不增长
  const callsAfter = measurer.scheduler.executorCalls;
  // 仅第一轮产生任务：font:serif（共享）+ font:mono + 3 簇 + 3 测量 = 8
  assert.equal(callsAfter, 8);
});
