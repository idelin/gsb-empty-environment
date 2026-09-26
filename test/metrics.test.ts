import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/index.ts';
import { metricsV1, metricsV2, longText } from '../fixtures/fixtures.ts';

test('旧版本度量被拒绝,不覆盖新布局', () => {
  const eng = new TruncationEngine(metricsV1);
  eng.layout(longText, { lineWidth: 20, maxLines: 2, lineHeight: 1, mode: 'end' });
  const up = eng.updateMetrics(metricsV2);
  assert.equal(up.accepted, true);
  const genAfterV2 = eng.current!.result.generation;
  // 旧版本(v1)与当前版本(v2 再投)都被拒绝
  assert.equal(eng.updateMetrics(metricsV1).accepted, false);
  assert.equal(eng.updateMetrics(metricsV2).accepted, false);
  assert.equal(eng.current!.result.generation, genAfterV2);
  assert.equal(eng.current!.result.metricsVersion, 2);
});

test('异步晚到的旧度量被丢弃:竞态下新布局不被覆盖', async () => {
  const eng = new TruncationEngine(metricsV1);
  eng.layout(longText, { lineWidth: 20, maxLines: 2, lineHeight: 1, mode: 'end' });
  // v3 慢到,v4 先到;v3 resolve 时版本已过期
  const v3 = { ...metricsV2, version: 3 };
  const v4 = { ...metricsV2, version: 4 };
  const slow = eng.updateMetricsAsync(v3, 30);
  const fast = eng.updateMetrics(v4);
  assert.equal(fast.accepted, true);
  const late = await slow;
  assert.equal(late.accepted, false);
  assert.equal(eng.current!.result.metricsVersion, 4);
});

test('度量版本切换触发增量重排:布局代次递增,宽度缓存复用', () => {
  const eng = new TruncationEngine(metricsV1);
  const r1 = eng.layout(longText, { lineWidth: 40, maxLines: 4, lineHeight: 1, mode: 'end' });
  const firstLineV1 = r1.lines[0].items.length;
  const r2 = eng.updateMetrics(metricsV2).result!;
  assert.equal(r2.generation, r1.generation + 1);
  assert.equal(r2.metricsVersion, 2);
  // 字母变宽 -> 每行容纳原子变少
  assert.ok(r2.lines[0].items.length < firstLineV1);
  // 增量证据:重复出现的簇命中缓存,没有全部重算
  assert.ok(eng.stats.reused > 0);
  assert.ok(eng.stats.computed < eng.current!.atoms.length);
});

test('宽度连续变化:不变量始终成立', () => {
  const eng = new TruncationEngine(metricsV1);
  for (let w = 6; w <= 60; w++) {
    const r = eng.layout(longText, { lineWidth: w, maxLines: 3, lineHeight: 1, mode: 'middle' });
    assert.ok(r.lines.length <= 3, `w=${w}`);
    for (const line of r.lines) {
      assert.ok(line.width <= w, `w=${w} line width ${line.width}`);
    }
    // 截断时末行必有省略号(省略号宽 2 <= w)
    if (r.truncated) {
      assert.ok(r.lines[r.lines.length - 1].items.some((i) => i.kind === 'ellipsis'), `w=${w}`);
    }
  }
});
