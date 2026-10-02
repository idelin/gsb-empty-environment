import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TileGrid } from '../src/grid.ts';
import { BoundedMinHeap } from '../src/minHeap.ts';
import { SpatioTemporalCube, DensityGridAccumulator, HOUR_MS } from '../src/cube.ts';

const CITY = { minLng: 116.0, minLat: 39.6, maxLng: 116.8, maxLat: 40.2, cols: 100, rows: 100 };

test('TileGrid: 经纬度与网格编号映射及中心回算', () => {
  const grid = new TileGrid({ ...CITY });
  assert.equal(grid.cellCount, 10_000);
  const idx = grid.cellIndexOf(116.4, 39.9);
  assert.ok(idx >= 0);
  const center = grid.cellCenter(idx);
  // 中心点必须落回原网格
  assert.equal(grid.cellIndexOf(center.lng, center.lat), idx);
  // 边界点
  assert.equal(grid.cellIndexOf(116.0, 39.6), 0);
  assert.equal(grid.cellIndexOf(116.799, 40.199), 9999);
});

test('TileGrid: 越界点返回 -1', () => {
  const grid = new TileGrid({ ...CITY });
  assert.equal(grid.cellIndexOf(115.9, 39.9), -1);
  assert.equal(grid.cellIndexOf(116.4, 40.3), -1);
  assert.equal(grid.cellIndexOf(116.8, 39.9), -1); // 上边界开区间
});

test('DensityGridAccumulator: 权重累加与重置', () => {
  const acc = new DensityGridAccumulator(4);
  acc.add(1, 2);
  acc.add(1, 3);
  acc.add(2, 1);
  assert.equal(acc.weights[1], 5);
  assert.equal(acc.weights[2], 1);
  acc.reset();
  assert.equal(acc.weights[1], 0);
});

test('BoundedMinHeap: 流式选出 Top-K 且按权重降序', () => {
  const heap = new BoundedMinHeap(3);
  const data: Array<[number, number]> = [[5, 0], [1, 1], [9, 2], [7, 3], [3, 4], [8, 5]];
  for (const [k, v] of data) heap.offer(k, v);
  assert.equal(heap.length, 3);
  const top = heap.drainSortedDesc();
  assert.deepEqual(top.map((i) => i.key), [9, 8, 7]);
  assert.deepEqual(top.map((i) => i.value), [2, 5, 3]);
});

test('Cube: 流式落桶累加，同一网格跨小时分别计数', () => {
  const cube = new SpatioTemporalCube({ ...CITY, retentionHours: 24 });
  const t0 = Date.UTC(2026, 8, 25, 10, 0, 0); // 某小时
  cube.addPoint(116.401, 39.901, t0, 1);
  cube.addPoint(116.402, 39.902, t0 + 60_000, 2); // 同小时同网格
  cube.addPoint(116.401, 39.901, t0 + HOUR_MS, 5); // 下一小时

  const sameHour = cube.topK(t0, t0 + HOUR_MS, 1);
  assert.equal(sameHour[0].weight, 3);
  const nextHour = cube.topK(t0 + HOUR_MS, t0 + 2 * HOUR_MS, 1);
  assert.equal(nextHour[0].weight, 5);
  const both = cube.topK(t0, t0 + 2 * HOUR_MS, 1);
  assert.equal(both[0].weight, 8);
});

test('Cube: Top-K 热点返回网格中心经纬度且按权重降序', () => {
  const cube = new SpatioTemporalCube({ ...CITY, retentionHours: 24 });
  const t0 = Date.UTC(2026, 8, 25, 18, 0, 0);
  const grid = cube.grid;
  // 人工制造 3 个热点，权重 300 / 200 / 100
  const spots: Array<[number, number, number]> = [[116.32, 39.98, 300], [116.45, 39.92, 200], [116.55, 40.0, 100]];
  for (const [lng, lat, w] of spots) cube.addPoint(lng, lat, t0, w);
  // 撒 1000 个权重为 1 的随机背景点（确定性伪随机）
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let i = 0; i < 1000; i++) {
    cube.addPoint(116.0 + rand() * 0.79, 39.6 + rand() * 0.59, t0, 1);
  }
  const top = cube.topK(t0, t0 + HOUR_MS, 3);
  assert.equal(top.length, 3);
  assert.deepEqual(top.map((h) => h.weight), [300, 200, 100]);
  assert.equal(top[0].cellIndex, grid.cellIndexOf(116.32, 39.98));
  // 返回的中心必须位于对应网格内
  for (const h of top) {
    assert.equal(grid.cellIndexOf(h.lng, h.lat), h.cellIndex);
    assert.equal(h.rank, top.indexOf(h) + 1);
  }
});

test('Cube: 查询窗口只统计窗口内小时', () => {
  const cube = new SpatioTemporalCube({ ...CITY, retentionHours: 48 });
  const t0 = Date.UTC(2026, 8, 25, 0, 0, 0);
  cube.addPoint(116.1, 39.7, t0 + 8 * HOUR_MS, 1000);  // 08:00 高峰
  cube.addPoint(116.1, 39.7, t0 + 18 * HOUR_MS, 10);   // 18:00 同网格
  const morning = cube.topK(t0 + 8 * HOUR_MS, t0 + 9 * HOUR_MS, 1);
  assert.equal(morning[0].weight, 1000);
  const evening = cube.topK(t0 + 18 * HOUR_MS, t0 + 19 * HOUR_MS, 1);
  assert.equal(evening[0].weight, 10);
});

test('Cube: 环形缓冲逐出最旧小时，内存上界恒定', () => {
  const retention = 24;
  const cube = new SpatioTemporalCube({ ...CITY, retentionHours: retention });
  const t0 = Date.UTC(2026, 8, 25, 0, 0, 0);
  // 连续写入 72 小时，每小时一个点
  for (let h = 0; h < 72; h++) {
    cube.addPoint(116.4, 39.9, t0 + h * HOUR_MS, 1);
  }
  const stats = cube.stats();
  assert.equal(stats.activeHours, retention); // 槽位数恒定
  assert.equal(stats.acceptedPoints, 72);
  // 最旧的第 0 小时已被逐出
  const evicted = cube.topK(t0, t0 + HOUR_MS, 1);
  assert.equal(evicted.length, 0);
  // 最近一小时仍可查
  const latest = cube.topK(t0 + 71 * HOUR_MS, t0 + 72 * HOUR_MS, 1);
  assert.equal(latest[0].weight, 1);
  // 内存预算与网格规模、保留时长成线性关系
  assert.equal(
    stats.memoryBudgetBytes,
    10_000 * 8 * (retention + 1) + retention * 4,
  );
});

test('Cube: 越界点与过期点被丢弃并计数', () => {
  const cube = new SpatioTemporalCube({ ...CITY, retentionHours: 2 });
  const t0 = Date.UTC(2026, 8, 25, 0, 0, 0);
  assert.equal(cube.addPoint(115.0, 39.9, t0), false); // 越界
  cube.addPoint(116.4, 39.9, t0 + 5 * HOUR_MS, 1);     // 推进环形缓冲
  assert.equal(cube.addPoint(116.4, 39.9, t0, 1), false); // 已过期
  const stats = cube.stats();
  assert.equal(stats.droppedPoints, 2);
  assert.equal(stats.acceptedPoints, 1);
});

test('Cube: 百万级点流式导入后秒级完成 Top-10 查询', () => {
  const cube = new SpatioTemporalCube({ ...CITY, retentionHours: 24 * 7 });
  const t0 = Date.UTC(2026, 8, 25, 0, 0, 0); // 周五 00:00 UTC
  let seed = 7;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;

  const n = 1_000_000;
  const ingestStart = performance.now();
  for (let i = 0; i < n; i++) {
    const hour = Math.floor(rand() * 24);
    cube.addPoint(116.0 + rand() * 0.79, 39.6 + rand() * 0.59, t0 + hour * HOUR_MS, 1);
  }
  const ingestMs = performance.now() - ingestStart;
  assert.equal(cube.stats().acceptedPoints, n);

  const queryStart = performance.now();
  const top = cube.topK(t0 + 18 * HOUR_MS, t0 + 20 * HOUR_MS, 10); // 晚高峰 18:00-20:00
  const queryMs = performance.now() - queryStart;

  assert.equal(top.length, 10);
  for (let i = 1; i < top.length; i++) {
    assert.ok(top[i - 1].weight >= top[i].weight);
  }
  console.log(`    [perf] 导入 ${n} 点: ${ingestMs.toFixed(0)}ms, Top-10 查询: ${queryMs.toFixed(1)}ms`);
  assert.ok(queryMs < 1000, `查询耗时 ${queryMs}ms 超过 1 秒`);
});
