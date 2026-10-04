import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  solveSeam,
  evaluateCosts,
  SeamInputError,
  type Label,
  type SeamProblem,
} from '../src/seam.ts';

/** mulberry32 确定性伪随机数发生器 */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomGrid(rand: () => number, height: number, width: number, max: number): number[][] {
  return Array.from({ length: height }, () =>
    Array.from({ length: width }, () => Math.floor(rand() * max)),
  );
}

function randomProblem(rand: () => number, width: number, height: number): SeamProblem {
  const problem: SeamProblem = {
    width,
    height,
    costA: randomGrid(rand, height, width, 20),
    costB: randomGrid(rand, height, width, 20),
    switchCostHorizontal: randomGrid(rand, height, Math.max(0, width - 1), 10),
    switchCostVertical: randomGrid(rand, Math.max(0, height - 1), width, 10),
    fixed: [],
  };
  // 随机加入约 1/4 像素的固定约束
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      if (rand() < 0.25) {
        problem.fixed!.push({ x, y, label: (rand() < 0.5 ? 0 : 1) as Label });
      }
    }
  }
  return problem;
}

/** 穷举全部 2^(w*h) 种标注，返回满足固定约束的最小能量 */
function bruteForceOptimum(problem: SeamProblem): number {
  const { width, height } = problem;
  const n = width * height;
  let best = Number.POSITIVE_INFINITY;
  for (let mask = 0; mask < 1 << n; mask++) {
    const labels: Label[][] = [];
    let ok = true;
    for (let y = 0; y < height && ok; y++) {
      const row: Label[] = [];
      for (let x = 0; x < width; x++) {
        row.push(((mask >> (y * width + x)) & 1) as Label);
      }
      labels.push(row);
    }
    for (const c of problem.fixed ?? []) {
      if (labels[c.y][c.x] !== c.label) {
        ok = false;
        break;
      }
    }
    if (!ok) continue;
    const cost = evaluateCosts(labels, problem).totalCost;
    if (cost < best) best = cost;
  }
  return best;
}

// ---------- 输入校验：负值 / 非有限值 / 维度 ----------

test('拒绝负的来源代价', () => {
  const base: SeamProblem = { width: 1, height: 1, costA: [[1]], costB: [[2]] };
  assert.throws(() => solveSeam({ ...base, costA: [[-0.5]] }), SeamInputError);
  assert.throws(() => solveSeam({ ...base, costB: [[-1]] }), /负值/);
});

test('拒绝负的切换代价', () => {
  const base: SeamProblem = {
    width: 2,
    height: 1,
    costA: [[1, 1]],
    costB: [[1, 1]],
  };
  assert.throws(() => solveSeam({ ...base, switchCostHorizontal: [[-3]] }), SeamInputError);
  assert.throws(
    () => solveSeam({ ...base, width: 1, height: 2, costA: [[1], [1]], costB: [[1], [1]], switchCostVertical: [[-1]] }),
    SeamInputError,
  );
});

test('拒绝 NaN 与 Infinity', () => {
  const base: SeamProblem = { width: 1, height: 1, costA: [[1]], costB: [[2]] };
  assert.throws(() => solveSeam({ ...base, costA: [[NaN]] }), SeamInputError);
  assert.throws(() => solveSeam({ ...base, costB: [[Infinity]] }), SeamInputError);
});

test('拒绝维度不符与非法尺寸', () => {
  assert.throws(
    () => solveSeam({ width: 2, height: 1, costA: [[1]], costB: [[1, 2]] }),
    SeamInputError,
  );
  assert.throws(
    () => solveSeam({ width: -1, height: 1, costA: [], costB: [] }),
    SeamInputError,
  );
  assert.throws(
    () =>
      solveSeam({
        width: 2,
        height: 2,
        costA: [[1, 1], [1, 1]],
        costB: [[1, 1], [1, 1]],
        switchCostHorizontal: [[1, 1], [1]], // 行宽应为 1
      }),
    SeamInputError,
  );
});

test('拒绝越界与冲突的固定约束', () => {
  const base: SeamProblem = { width: 2, height: 2, costA: [[1, 1], [1, 1]], costB: [[1, 1], [1, 1]] };
  assert.throws(() => solveSeam({ ...base, fixed: [{ x: 2, y: 0, label: 0 }] }), SeamInputError);
  assert.throws(
    () => solveSeam({ ...base, fixed: [{ x: 0, y: 0, label: 0 }, { x: 0, y: 0, label: 1 }] }),
    /冲突/,
  );
});

// ---------- 无重叠（空区域） ----------

test('无重叠：0×0 返回空分割与零费用', () => {
  const r = solveSeam({ width: 0, height: 0, costA: [], costB: [] });
  assert.deepEqual(r.labels, []);
  assert.equal(r.totalCost, 0);
  assert.equal(r.sourceCost, 0);
  assert.equal(r.switchCost, 0);
});

test('无重叠：宽度为 0 返回空行且费用为零', () => {
  const r = solveSeam({ width: 0, height: 3, costA: [[], [], []], costB: [[], [], []] });
  assert.deepEqual(r.labels, [[], [], []]);
  assert.equal(r.totalCost, 0);
});

// ---------- 固定约束 ----------

test('固定约束被严格遵守', () => {
  // 无约束时最优为全 B（costB 全 0），但 (0,0) 被固定为 A
  const r = solveSeam({
    width: 2,
    height: 1,
    costA: [[5, 5]],
    costB: [[0, 0]],
    switchCostHorizontal: [[100]],
    fixed: [{ x: 0, y: 0, label: 0 }],
  });
  assert.deepEqual(r.labels, [[0, 0]]); // 切换代价 100 迫使整行跟随 A
  assert.equal(r.totalCost, 10); // costA 5+5，无切换
});

// ---------- 并列解稳定性 ----------

test('并列解：相同输入多次求解结果完全一致', () => {
  const problem: SeamProblem = {
    width: 3,
    height: 3,
    costA: [[1, 1, 1], [1, 1, 1], [1, 1, 1]],
    costB: [[1, 1, 1], [1, 1, 1], [1, 1, 1]],
    switchCostHorizontal: [[1, 1], [1, 1], [1, 1]],
    switchCostVertical: [[1, 1, 1], [1, 1, 1]],
  };
  const first = solveSeam(problem);
  for (let i = 0; i < 10; i++) {
    assert.deepEqual(solveSeam(problem).labels, first.labels);
  }
  assert.equal(first.totalCost, 9); // 全 A 或全 B 均为最优
});

test('并列解：tieBreak 偏置给出规范解', () => {
  const problem: SeamProblem = {
    width: 2,
    height: 2,
    costA: [[1, 1], [1, 1]],
    costB: [[1, 1], [1, 1]],
    switchCostHorizontal: [[1], [1]],
    switchCostVertical: [[1, 1]],
  };
  assert.deepEqual(solveSeam(problem, { tieBreak: 'A' }).labels, [[0, 0], [0, 0]]);
  assert.deepEqual(solveSeam(problem, { tieBreak: 'B' }).labels, [[1, 1], [1, 1]]);
  // 偏置不污染报告费用
  assert.equal(solveSeam(problem, { tieBreak: 'A' }).totalCost, 4);
});

// ---------- 解析可验证的小例子 ----------

test('1×2：切换代价决定是否分割', () => {
  const base = { width: 2, height: 1, costA: [[0, 10]], costB: [[10, 0]] };
  const split = solveSeam({ ...base, switchCostHorizontal: [[5]] });
  assert.deepEqual(split.labels, [[0, 1]]);
  assert.equal(split.totalCost, 5);
  const merged = solveSeam({ ...base, switchCostHorizontal: [[20]] });
  assert.equal(merged.totalCost, 10);
  // AA 与 BB 并列最优（均为 10），tieBreak 选定规范解
  assert.deepEqual(solveSeam({ ...base, switchCostHorizontal: [[20]] }, { tieBreak: 'A' }).labels, [[0, 0]]);
  assert.deepEqual(solveSeam({ ...base, switchCostHorizontal: [[20]] }, { tieBreak: 'B' }).labels, [[1, 1]]);
});

test('费用明细自洽：total = source + switch，且与 evaluateCosts 一致', () => {
  const rand = rng(12345);
  const problem = randomProblem(rand, 4, 3);
  const r = solveSeam(problem);
  assert.ok(Math.abs(r.totalCost - (r.sourceCost + r.switchCost)) < 1e-9);
  assert.ok(Math.abs(r.sourceCost - (r.sourceCostA + r.sourceCostB)) < 1e-9);
  assert.ok(Math.abs(r.switchCost - (r.switchCostHorizontal + r.switchCostVertical)) < 1e-9);
  const check = evaluateCosts(r.labels, problem);
  assert.ok(Math.abs(check.totalCost - r.totalCost) < 1e-9);
});

// ---------- 小图穷举核对最优值 ----------

test('穷举核对：1×1、2×2、2×3、3×3 随机实例均达到最优', () => {
  const rand = rng(20261005);
  const sizes: Array<[number, number]> = [
    [1, 1],
    [2, 2],
    [3, 2],
    [3, 3],
    [4, 3],
  ];
  for (const [width, height] of sizes) {
    for (let trial = 0; trial < 30; trial++) {
      const problem = randomProblem(rand, width, height);
      const r = solveSeam(problem);
      const optimum = bruteForceOptimum(problem);
      assert.ok(
        Math.abs(r.totalCost - optimum) < 1e-9,
        `${width}×${height} trial ${trial}: 求解器 ${r.totalCost} ≠ 穷举 ${optimum}`,
      );
      // 固定约束确实被满足
      for (const c of problem.fixed ?? []) {
        assert.equal(r.labels[c.y][c.x], c.label);
      }
    }
  }
});

test('穷举核对：并列最优时求解器费用仍等于穷举最优值', () => {
  const rand = rng(777);
  for (let trial = 0; trial < 50; trial++) {
    // 小整数代价制造大量并列
    const problem = randomProblem(rand, 3, 3);
    problem.costA = randomGrid(rand, 3, 3, 3);
    problem.costB = randomGrid(rand, 3, 3, 3);
    const r = solveSeam(problem);
    const optimum = bruteForceOptimum(problem);
    assert.ok(Math.abs(r.totalCost - optimum) < 1e-9);
  }
});
