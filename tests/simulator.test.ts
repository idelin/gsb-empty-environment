import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FloodSimulator } from '../src/simulator.ts';
import { demFromArray, createGrid } from '../src/dem.ts';

const flatDem = (rows: number, cols: number, elev = 0, cellSize = 1) =>
  demFromArray(Array.from({ length: rows }, () => Array(cols).fill(elev)), cellSize);

test('降雨在平坦地形上均匀累积（mm -> m）', () => {
  const sim = new FloodSimulator({
    dem: flatDem(3, 3),
    rainfall: { mmPerTick: [10, 10] },
  });
  sim.run(2);
  for (const d of sim.currentDepth) {
    assert.ok(Math.abs(d - 0.02) < 1e-12, `期望 0.02，实际 ${d}`);
  }
  // 总体积 = 0.02m * 9 单元 * 1m² = 0.18 m³
  assert.ok(Math.abs(sim.totalWaterVolume() - 0.18) < 1e-12);
});

test('入渗按速率扣减积水且不为负', () => {
  const dem = flatDem(2, 2);
  const infiltration = createGrid(2, 2, 0.005); // 5mm/tick
  const sim = new FloodSimulator({
    dem,
    infiltration,
    rainfall: { mmPerTick: [10, 0, 0] },
  });
  sim.step(); // 10mm 降雨 - 5mm 入渗 = 5mm
  for (const d of sim.currentDepth) assert.ok(Math.abs(d - 0.005) < 1e-12);
  sim.step(); // 无雨，再渗 5mm -> 0
  sim.step();
  for (const d of sim.currentDepth) assert.equal(d, 0);
});

test('水沿坡度向低处漫延且体积守恒', () => {
  // 1x3 斜坡，高程 2 -> 1 -> 0
  const dem = demFromArray([[2, 1, 0]], 1);
  const sim = new FloodSimulator({
    dem,
    rainfall: { mmPerTick: [10] },
    flowFactor: 0.5,
  });
  sim.step();
  const d = sim.currentDepth;
  // cell0: 0.01 - 0.005 = 0.005；cell1: 0.01 +0.005 -0.005 = 0.01；cell2: 0.01 + 0.005 = 0.015
  assert.ok(Math.abs(d[0] - 0.005) < 1e-12, `d0=${d[0]}`);
  assert.ok(Math.abs(d[1] - 0.01) < 1e-12, `d1=${d[1]}`);
  assert.ok(Math.abs(d[2] - 0.015) < 1e-12, `d2=${d[2]}`);
  assert.ok(Math.abs(sim.totalWaterVolume() - 0.03) < 1e-12, '体积守恒');
});

test('多 tick 演化全程体积守恒（无入渗）', () => {
  const dem = demFromArray(
    [
      [3, 2, 3, 4],
      [2, 0, 1, 3],
      [3, 1, 2, 4],
    ],
    2,
  );
  const rain = [20, 15, 0, 0, 5];
  const sim = new FloodSimulator({ dem, rainfall: { mmPerTick: rain } });
  sim.run(5);
  const totalRainM = rain.reduce((a, b) => a + b, 0) / 1000;
  const expected = totalRainM * 12 * 4; // 12 单元 * (2m)²
  assert.ok(Math.abs(sim.totalWaterVolume() - expected) < 1e-9);
});

test('历史快照支持任意历史时刻查询', () => {
  const sim = new FloodSimulator({
    dem: flatDem(2, 2),
    rainfall: { mmPerTick: [10, 10, 10] },
  });
  sim.run(3);
  assert.equal(sim.tick, 3);
  assert.ok(Math.abs(sim.depthAt(0)[0] - 0) < 1e-12);
  assert.ok(Math.abs(sim.depthAt(1)[0] - 0.01) < 1e-12);
  assert.ok(Math.abs(sim.depthAt(2)[0] - 0.02) < 1e-12);
  assert.ok(Math.abs(sim.depthAt(3)[0] - 0.03) < 1e-12);
  assert.throws(() => sim.depthAt(4), /无 tick=4/);
});

test('cellStateAt 返回水面高程与单元状态', () => {
  const dem = demFromArray([[5, 6], [7, 8]], 1);
  const sim = new FloodSimulator({ dem, rainfall: { mmPerTick: [10] } });
  sim.step();
  const s = sim.cellStateAt(1, 0, 0);
  assert.equal(s.elevation, 5);
  // (0,0) 是最低点，除自身降雨外还接收高处来水
  assert.ok(s.waterDepth > 0.01, `waterDepth=${s.waterDepth}`);
  assert.ok(Math.abs(s.waterLevel - (s.elevation + s.waterDepth)) < 1e-12);
  assert.throws(() => sim.cellStateAt(1, 9, 9), /越界/);
});

test('淹没掩码按阈值判定', () => {
  const sim = new FloodSimulator({
    dem: flatDem(1, 2),
    rainfall: { mmPerTick: [10] },
    floodThreshold: 0.05, // 50mm
  });
  sim.step(); // 10mm < 50mm
  assert.deepEqual([...sim.floodedMaskAt(1)], [0, 0]);
  const sim2 = new FloodSimulator({
    dem: flatDem(1, 2),
    rainfall: { mmPerTick: [100] },
    floodThreshold: 0.05,
  });
  sim2.step();
  assert.deepEqual([...sim2.floodedMaskAt(1)], [1, 1]);
});

test('非法参数抛出错误', () => {
  assert.throws(
    () => new FloodSimulator({ dem: flatDem(2, 2), rainfall: { mmPerTick: [] }, flowFactor: 0.9 }),
    /flowFactor/,
  );
  assert.throws(
    () =>
      new FloodSimulator({
        dem: flatDem(2, 2),
        infiltration: createGrid(3, 3),
        rainfall: { mmPerTick: [] },
      }),
    /尺寸/,
  );
});
