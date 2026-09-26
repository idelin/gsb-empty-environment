import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ByteCache } from '../src/cache.ts';
import { FontRegistry } from '../src/fontRegistry.ts';
import {
  PARTITION_MEASURE,
  TextMeasurer,
  measureKey,
} from '../src/measure.ts';
import { monoV1, serifV1, serifV2 } from '../src/fixtures.ts';
import type { MeasureEvent, StyleSpec } from '../src/types.ts';

const STYLE_LS0: StyleSpec = { fontId: 'serif', fontSize: 16, letterSpacing: 0 };
const STYLE_LS2: StyleSpec = { fontId: 'serif', fontSize: 16, letterSpacing: 2 };
const MONO_STYLE: StyleSpec = { fontId: 'mono', fontSize: 12, letterSpacing: 0 };

/** serif v1 'Hello': (720+500+240+240+560) * 16/1000 = 36.16 */
const HELLO_V1_WIDTH = 2260 * 0.016;
/** serif v2 'Hello': (760+520+260+260+600) * 16/1000 = 38.4 */
const HELLO_V2_WIDTH = 2400 * 0.016;

function makeMeasurer() {
  const registry = new FontRegistry([serifV1, monoV1]);
  const cache = new ByteCache(200_000, { font: 40_000, cluster: 60_000, measure: 100_000 });
  const measurer = new TextMeasurer(registry, cache);
  return { registry, cache, measurer };
}

function drive(measurer: TextMeasurer): void {
  for (;;) {
    const started = measurer.scheduler.pump();
    const delivered = measurer.scheduler.flush();
    if (started === 0 && delivered === 0) break;
  }
}

function measureAll(measurer: TextMeasurer, text: string, style: StyleSpec): MeasureEvent[] {
  const events: MeasureEvent[] = [];
  measurer.measureText(text, style, (e) => events.push(e));
  drive(measurer);
  return events;
}

test('预估与最终测量分开发布：预估同步、最终异步且数值不同', () => {
  const { measurer } = makeMeasurer();
  const events: MeasureEvent[] = [];
  measurer.measureText('Hello', STYLE_LS0, (e) => events.push(e));
  // 预估同步发布，最终尚未到达
  assert.deepEqual(events.map((e) => e.phase), ['estimate']);
  drive(measurer);
  assert.deepEqual(events.map((e) => e.phase), ['estimate', 'final']);
  assert.notEqual(events[0].result.width, events[1].result.width);
  assert.equal(events[1].result.width, HELLO_V1_WIDTH);
});

test('相同文本不同字距：结果与缓存条目都不得共享', () => {
  const { measurer, cache } = makeMeasurer();
  const ls0 = measureAll(measurer, 'Hello', STYLE_LS0);
  const ls2 = measureAll(measurer, 'Hello', STYLE_LS2);
  const w0 = ls0.find((e) => e.phase === 'final')!.result.width;
  const w2 = ls2.find((e) => e.phase === 'final')!.result.width;
  assert.equal(w0, HELLO_V1_WIDTH);
  assert.equal(w2, HELLO_V1_WIDTH + 2 * 4); // 5 个字符 4 个字距间隙
  assert.notEqual(measureKey(STYLE_LS0, 1, 'Hello'), measureKey(STYLE_LS2, 1, 'Hello'));
  assert.ok(cache.peek(PARTITION_MEASURE, measureKey(STYLE_LS0, 1, 'Hello')));
  assert.ok(cache.peek(PARTITION_MEASURE, measureKey(STYLE_LS2, 1, 'Hello')));
});

test('缓存命中：重复测量直接发布最终值，不再调度任务', () => {
  const { measurer } = makeMeasurer();
  measureAll(measurer, 'Hello', STYLE_LS0);
  const callsBefore = measurer.scheduler.executorCalls;
  const events = measureAll(measurer, 'Hello', STYLE_LS0);
  assert.deepEqual(events.map((e) => e.phase), ['estimate', 'final']);
  assert.equal(events[1].result.width, HELLO_V1_WIDTH);
  assert.equal(measurer.scheduler.executorCalls, callsBefore); // 无新任务
});

test('字体版本变化精确失效：目标字体条目清除，其他字体保留', () => {
  const { measurer, cache, registry } = makeMeasurer();
  measureAll(measurer, 'Hello', STYLE_LS0);
  measureAll(measurer, 'ok', MONO_STYLE);
  registry.hotReload(serifV2);
  assert.equal(cache.peek(PARTITION_MEASURE, measureKey(STYLE_LS0, 1, 'Hello')), undefined);
  assert.ok(cache.peek(PARTITION_MEASURE, measureKey(MONO_STYLE, 1, 'ok'))); // mono 不受影响
  // 重新测量得到新版本宽度
  const events = measureAll(measurer, 'Hello', STYLE_LS0);
  assert.equal(events.find((e) => e.phase === 'final')!.result.width, HELLO_V2_WIDTH);
});

test('异步旧回包不能覆盖新版本：stale 丢弃后按新代际重测', () => {
  const { measurer, cache, registry } = makeMeasurer();
  const events: MeasureEvent[] = [];
  measurer.measureText('Hello', STYLE_LS0, (e) => events.push(e));
  measurer.scheduler.pump(); // 任务在代际 0 启动
  registry.hotReload(serifV2); // 热更新：代际前进
  drive(measurer); // 旧回包到达 -> stale -> 自动按新代际重发
  const finals = events.filter((e) => e.phase === 'final');
  assert.equal(finals.length, 1); // 旧版本结果从未作为最终值发布
  assert.equal(finals[0].result.width, HELLO_V2_WIDTH);
  assert.equal(finals[0].generation, 1);
  // 缓存中只有新版本条目
  assert.equal(cache.peek(PARTITION_MEASURE, measureKey(STYLE_LS0, 1, 'Hello')), undefined);
  const v2 = cache.peek(PARTITION_MEASURE, measureKey(STYLE_LS0, 2, 'Hello'));
  assert.ok(v2);
  assert.equal(v2.meta.fontVersion, 2);
});

test('取消订阅不影响其他等待相同测量的订阅者', () => {
  const { measurer } = makeMeasurer();
  const a: MeasureEvent[] = [];
  const b: MeasureEvent[] = [];
  const handleA = measurer.measureText('Hello', STYLE_LS0, (e) => a.push(e));
  measurer.measureText('Hello', STYLE_LS0, (e) => b.push(e));
  handleA.cancel();
  drive(measurer);
  assert.deepEqual(a.map((e) => e.phase), ['estimate']); // 只有同步预估
  assert.deepEqual(b.map((e) => e.phase), ['estimate', 'final']);
});
