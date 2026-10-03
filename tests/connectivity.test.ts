import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  connectedComponents,
  traceBoundary,
  floodedRegions,
  signedArea,
} from '../src/connectivity.ts';

function mask(rows: number, cols: number, flooded: Array<[number, number]>) {
  const data = new Uint8Array(rows * cols);
  for (const [r, c] of flooded) data[r * cols + c] = 1;
  return { rows, cols, data };
}

test('4 连通分量：分离区域各自独立', () => {
  const m = mask(3, 5, [
    [0, 0], [0, 1], [1, 0], // 区域 A（3 格）
    [2, 4],                 // 区域 B（1 格）
    [1, 3],                 // 区域 C（1 格，与 B 对角不算连通）
  ]);
  const comps = connectedComponents(m);
  assert.equal(comps.length, 3);
  const sizes = comps.map((c) => c.length).sort((a, b) => a - b);
  assert.deepEqual(sizes, [1, 1, 3]);
});

test('单单元区域边界为单位正方形', () => {
  const { outer, holes } = traceBoundary([1 * 3 + 1], 3, 2); // cellSize=2
  assert.equal(holes.length, 0);
  assert.equal(outer.length, 4);
  assert.ok(Math.abs(Math.abs(signedArea(outer)) - 4) < 1e-9); // 2m x 2m
  const xs = outer.map((p) => p.x).sort((a, b) => a - b);
  const ys = outer.map((p) => p.y).sort((a, b) => a - b);
  assert.deepEqual(xs, [2, 2, 4, 4]);
  assert.deepEqual(ys, [2, 2, 4, 4]);
});

test('2x2 区块边界为 4 顶点矩形，面积为 4 个单元', () => {
  const cells = [0, 1, 4, 5]; // 4 列栅格中的 2x2 块
  const { outer, holes } = traceBoundary(cells, 4, 1);
  assert.equal(holes.length, 0);
  assert.equal(outer.length, 4);
  assert.ok(Math.abs(Math.abs(signedArea(outer)) - 4) < 1e-9);
});

test('环形区域产生外环与一个孔洞', () => {
  // 3x3 全部淹没、中心不淹没
  const cells: number[] = [];
  for (let r = 0; r < 3; r++) {
    for (let c = 0; c < 3; c++) {
      if (r === 1 && c === 1) continue;
      cells.push(r * 3 + c);
    }
  }
  const { outer, holes } = traceBoundary(cells, 3, 1);
  assert.equal(outer.length, 4); // 3x3 外轮廓
  assert.ok(Math.abs(Math.abs(signedArea(outer)) - 9) < 1e-9);
  assert.equal(holes.length, 1);
  assert.ok(Math.abs(Math.abs(signedArea(holes[0])) - 1) < 1e-9);
});

test('L 形区域边界顶点数与面积正确', () => {
  // L 形：(0,0),(1,0),(1,1) —— 3 格
  const cells = [0, 3, 4]; // cols=3
  const { outer } = traceBoundary(cells, 3, 1);
  assert.ok(Math.abs(Math.abs(signedArea(outer)) - 3) < 1e-9);
  assert.equal(outer.length, 6); // L 形凹六边形（共线点已简化）
});

test('floodedRegions 汇总面积与最大深度', () => {
  const m = mask(2, 4, [[0, 0], [0, 1], [1, 3]]);
  const depth = new Float64Array([0.3, 0.5, 0, 0, 0, 0, 0, 0.2]);
  const regions = floodedRegions(m, depth, 2);
  assert.equal(regions.length, 2);
  const big = regions.find((r) => r.cellCount === 2);
  assert.ok(big);
  assert.equal(big.areaSqm, 8); // 2 格 * 4m²
  assert.ok(Math.abs(big.maxDepth - 0.5) < 1e-12);
  const small = regions.find((r) => r.cellCount === 1);
  assert.ok(small);
  assert.ok(Math.abs(small.maxDepth - 0.2) < 1e-12);
});
