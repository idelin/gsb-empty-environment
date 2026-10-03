import { test } from 'node:test';
import assert from 'node:assert/strict';
import { findRoadBreakpoints, sampleRoad } from '../src/roads.ts';
import { FloodSimulator } from '../src/simulator.ts';
import { floodedRegions } from '../src/connectivity.ts';
import { demFromArray } from '../src/dem.ts';
import type { Road } from '../src/types.ts';

const ctx = { rows: 4, cols: 4, cellSize: 1, floodThreshold: 0.05 };

test('sampleRoad 按间距采样折线', () => {
  const road: Road = { id: 'R1', vertices: [{ x: 0, y: 0 }, { x: 4, y: 0 }, { x: 4, y: 2 }] };
  const pts = sampleRoad(road, 1);
  assert.equal(pts[pts.length - 1].x, 4);
  assert.equal(pts[pts.length - 1].y, 2);
  // 第一段 4m + 第二段 2m，步长 1 -> 至少 6 段
  assert.ok(pts.length >= 7);
});

test('穿越淹没区的道路产生断点，未穿越则无', () => {
  // 淹没中间 2x2（行1-2, 列1-2）
  const depth = new Float64Array(16);
  for (const [r, c] of [[1, 1], [1, 2], [2, 1], [2, 2]]) depth[r * 4 + c] = 0.3;
  const crossing: Road = { id: 'cross', vertices: [{ x: 0.5, y: 1.5 }, { x: 3.5, y: 1.5 }] };
  const away: Road = { id: 'away', vertices: [{ x: 0.5, y: 0.2 }, { x: 3.5, y: 0.2 }] };
  const bps = findRoadBreakpoints([crossing, away], depth, 7, ctx);
  assert.equal(bps.length, 1);
  assert.equal(bps[0].roadId, 'cross');
  assert.equal(bps[0].tick, 7);
  assert.ok(bps[0].waterDepth > 0.05);
  assert.equal(bps[0].regionId, 0);
  // 断点应位于淹没区中部（x≈2）
  assert.ok(Math.abs(bps[0].position.x - 2) < 0.6, `x=${bps[0].position.x}`);
});

test('同一道路上两处分离水塘产生两个断点', () => {
  const depth = new Float64Array(16);
  depth[1 * 4 + 0] = 0.2; // 左水塘
  depth[1 * 4 + 3] = 0.2; // 右水塘
  const road: Road = { id: 'R', vertices: [{ x: 0.2, y: 1.5 }, { x: 3.8, y: 1.5 }] };
  const bps = findRoadBreakpoints([road], depth, 3, ctx);
  assert.equal(bps.length, 2);
  assert.notEqual(bps[0].regionId, bps[1].regionId);
});

test('端到端：仿真 -> 任意历史 tick 连通区域多边形 + 道路断点', () => {
  // 6x6 洼地：中心低四周高
  const elev: number[][] = [];
  for (let r = 0; r < 6; r++) {
    const row: number[] = [];
    for (let c = 0; c < 6; c++) {
      row.push(Math.max(Math.abs(r - 2.5), Math.abs(c - 2.5)));
    }
    elev.push(row);
  }
  const dem = demFromArray(elev, 10);
  const sim = new FloodSimulator({
    dem,
    rainfall: { mmPerTick: [300, 300, 100, 0, 0] },
    flowFactor: 0.5,
    floodThreshold: 0.1,
  });
  sim.run(5);

  // 历史 tick=2 与当前 tick=5 都可查询
  for (const t of [2, 5]) {
    const mask = { rows: 6, cols: 6, data: sim.floodedMaskAt(t) };
    const regions = floodedRegions(mask, sim.depthAt(t), 10);
    assert.ok(regions.length >= 1, `tick=${t} 应至少有一个淹没区域`);
    for (const r of regions) {
      assert.ok(r.outerRing.length >= 4);
      assert.ok(r.areaSqm > 0);
    }
  }

  // 道路横穿洼地，应被阻断
  const road: Road = { id: 'valley-road', vertices: [{ x: 5, y: 25 }, { x: 55, y: 25 }] };
  const bps = findRoadBreakpoints(
    [road],
    sim.depthAt(5),
    5,
    { rows: 6, cols: 6, cellSize: 10, floodThreshold: 0.1 },
  );
  assert.ok(bps.length >= 1, '洼地积水应阻断道路');
  assert.equal(bps[0].roadId, 'valley-road');
  assert.ok(bps[0].waterDepth > 0.1);
});
