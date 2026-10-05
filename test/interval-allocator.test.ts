import { test } from "node:test";
import assert from "node:assert/strict";
import {
  allocateResources,
  type Allocation,
  type IntervalSpec,
} from "../src/interval-allocator.ts";

/** 校验分配结果本身的合法性：固定保留、同资源无重叠、覆盖全部区间。 */
function assertValidAllocation(specs: IntervalSpec[], result: Allocation): void {
  assert.equal(result.feasible, true);
  assert.equal(result.conflicts.length, 0);
  assert.equal(result.assignment.size, specs.length);

  let scheduledCount = 0;
  for (const [resource, list] of result.schedules) {
    for (let i = 0; i < list.length; i += 1) {
      scheduledCount += 1;
      assert.equal(result.assignment.get(list[i].id), resource);
      for (let j = i + 1; j < list.length; j += 1) {
        const a = list[i];
        const b = list[j];
        assert.ok(
          !(Math.max(a.start, b.start) < Math.min(a.end, b.end)),
          `资源 ${resource} 上 ${a.id} 与 ${b.id} 重叠`,
        );
      }
    }
    // 时间表按 (start, end, id) 排序
    for (let i = 1; i < list.length; i += 1) {
      const prev = list[i - 1];
      const curr = list[i];
      assert.ok(
        prev.start < curr.start ||
          (prev.start === curr.start && prev.end <= curr.end),
        "时间表未排序",
      );
    }
  }
  assert.equal(scheduledCount, specs.length);
  assert.equal(result.resourceCount, result.schedules.size);

  for (const spec of specs) {
    if (spec.resource !== undefined) {
      assert.equal(
        result.assignment.get(spec.id),
        spec.resource,
        `固定绑定 ${spec.id} 未被保留`,
      );
    }
  }
}

test("半开区间：端点相接可复用同一资源", () => {
  const result = allocateResources([
    { id: "a", start: 0, end: 2 },
    { id: "b", start: 2, end: 4 },
    { id: "c", start: 4, end: 5 },
  ]);
  assert.equal(result.resourceCount, 1);
  assert.equal(result.optimal, true);
  assertValidAllocation(
    [
      { id: "a", start: 0, end: 2 },
      { id: "b", start: 2, end: 4 },
      { id: "c", start: 4, end: 5 },
    ],
    result,
  );
});

test("基本重叠需要两个资源", () => {
  const specs = [
    { id: "a", start: 0, end: 3 },
    { id: "b", start: 1, end: 2 },
    { id: "c", start: 3, end: 5 },
  ];
  const result = allocateResources(specs);
  assert.equal(result.resourceCount, 2);
  assertValidAllocation(specs, result);
});

test("零长区间不与任何区间重叠，可共享资源", () => {
  const specs = [
    { id: "base", start: 0, end: 10 },
    { id: "z1", start: 5, end: 5 },
    { id: "z2", start: 5, end: 5 },
    { id: "z3", start: 0, end: 0 },
  ];
  const result = allocateResources(specs);
  assert.equal(result.resourceCount, 1);
  assertValidAllocation(specs, result);
});

test("固定绑定被保留", () => {
  const specs: IntervalSpec[] = [
    { id: "a", start: 0, end: 2, resource: 3 },
    { id: "b", start: 1, end: 3 },
    { id: "c", start: 2, end: 4 },
  ];
  const result = allocateResources(specs);
  assert.equal(result.assignment.get("a"), 3);
  assert.equal(result.resourceCount, 2);
  assertValidAllocation(specs, result);
});

test("固定同资源重叠：直接返回全部冲突对", () => {
  const result = allocateResources([
    { id: "a", start: 0, end: 3, resource: 1 },
    { id: "b", start: 1, end: 2, resource: 1 },
    { id: "c", start: 0.5, end: 1.5, resource: 1 },
    { id: "d", start: 10, end: 12, resource: 2 },
    { id: "e", start: 11, end: 13, resource: 2 },
    { id: "f", start: 1, end: 4, resource: 3 }, // 与 a 重叠但资源不同，非冲突
  ]);
  assert.equal(result.feasible, false);
  assert.deepEqual(result.conflicts, [
    { resource: 1, a: "a", b: "b" },
    { resource: 1, a: "a", b: "c" },
    { resource: 1, a: "b", b: "c" },
    { resource: 2, a: "d", b: "e" },
  ]);
});

test("零长固定区间不产生冲突", () => {
  const result = allocateResources([
    { id: "a", start: 0, end: 10, resource: 2 },
    { id: "z", start: 5, end: 5, resource: 2 },
  ]);
  assert.equal(result.feasible, true);
  assert.equal(result.resourceCount, 1);
});

test("不能仅按最大重叠数推断可扩展性：深度 2 但需 3 个资源", () => {
  // A 与 B 不重叠却分别固定为不同资源，C 同时压住两者。
  const specs: IntervalSpec[] = [
    { id: "A", start: 0, end: 2, resource: 0 },
    { id: "B", start: 3, end: 5, resource: 1 },
    { id: "C", start: 1, end: 4 },
  ];
  const result = allocateResources(specs);
  assert.equal(result.lowerBound, 2, "最大重叠数仅为 2");
  assert.equal(result.resourceCount, 3, "预染色扩展迫使总数为 3");
  assert.equal(result.optimal, true);
  assertValidAllocation(specs, result);
});

test("资源编号存在空洞：新增分配优先填充最小编号空洞", () => {
  const specs: IntervalSpec[] = [
    { id: "fixed", start: 10, end: 11, resource: 7 },
    { id: "a", start: 0, end: 2 },
    { id: "b", start: 1, end: 3 },
  ];
  const result = allocateResources(specs);
  assert.equal(result.resourceCount, 2);
  assert.deepEqual([...result.schedules.keys()].sort((x, y) => x - y), [0, 7]);
  assertValidAllocation(specs, result);
});

test("多个固定色 + 空洞：调色板 = 固定色 ∪ 最小小新编号", () => {
  const specs: IntervalSpec[] = [
    { id: "f1", start: 0, end: 10, resource: 5 },
    { id: "f2", start: 0, end: 10, resource: 9 },
    { id: "u1", start: 1, end: 2 },
    { id: "u2", start: 1, end: 2 },
    { id: "u3", start: 1, end: 2 },
  ];
  const result = allocateResources(specs);
  // 点 1 处共 5 个区间重叠，需 5 个资源：{0,1,2,5,9}
  assert.equal(result.resourceCount, 5);
  assert.deepEqual([...result.schedules.keys()].sort((a, b) => a - b), [
    0, 1, 2, 5, 9,
  ]);
  assertValidAllocation(specs, result);
});

test("空实例", () => {
  const result = allocateResources([]);
  assert.equal(result.feasible, true);
  assert.equal(result.resourceCount, 0);
  assert.equal(result.schedules.size, 0);
});

test("非法输入抛错", () => {
  assert.throws(() => allocateResources([{ id: "x", start: 3, end: 2 }]));
  assert.throws(() =>
    allocateResources([
      { id: "x", start: 0, end: 1 },
      { id: "x", start: 0, end: 1 },
    ]),
  );
  assert.throws(() =>
    allocateResources([{ id: "x", start: 0, end: 1, resource: -1 }]),
  );
  assert.throws(() =>
    allocateResources([{ id: "x", start: 0, end: 1, resource: 1.5 }]),
  );
});

test("预算耗尽时回退贪心：结果合法但 optimal=false", () => {
  const specs: IntervalSpec[] = [];
  for (let i = 0; i < 60; i += 1) {
    const start = (i * 7) % 40;
    specs.push({ id: `u${i}`, start, end: start + 1 + (i % 5) });
  }
  const result = allocateResources(specs, { maxSearchNodes: 0 });
  assert.equal(result.optimal, false);
  assertValidAllocation(specs, result);
});

// ---------- 短实例穷举最优：与暴力参考实现对拍 ----------

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** 无预算限制的暴力最优（仅用于小规模对拍）。 */
function bruteForceOptimalCount(specs: IntervalSpec[]): number | null {
  const n = specs.length;
  const overlap: boolean[][] = Array.from({ length: n }, () =>
    new Array<boolean>(n).fill(false),
  );
  for (let i = 0; i < n; i += 1) {
    for (let j = i + 1; j < n; j += 1) {
      if (
        Math.max(specs[i].start, specs[j].start) <
        Math.min(specs[i].end, specs[j].end)
      ) {
        overlap[i][j] = overlap[j][i] = true;
      }
    }
  }
  const fixed = specs.map((s) => s.resource ?? null);
  for (let i = 0; i < n; i += 1) {
    for (let j = i + 1; j < n; j += 1) {
      if (overlap[i][j] && fixed[i] !== null && fixed[i] === fixed[j]) {
        return null; // 固定冲突
      }
    }
  }
  const fixedColors = new Set(fixed.filter((c): c is number => c !== null));
  const lower = Math.max(n > 0 ? 1 : 0, fixedColors.size);
  for (let k = lower; k <= Math.max(lower, n); k += 1) {
    // 调色板：固定色 + 最小新编号
    const palette = [...fixedColors];
    let candidate = 0;
    const taken = new Set(palette);
    while (palette.length < k) {
      if (!taken.has(candidate)) {
        palette.push(candidate);
        taken.add(candidate);
      }
      candidate += 1;
    }
    const idxOf = new Map(palette.map((r, i) => [r, i]));
    const colors = new Array<number>(n).fill(-1);
    let ok = true;
    for (let i = 0; i < n; i += 1) {
      if (fixed[i] !== null) colors[i] = idxOf.get(fixed[i]!)!;
    }
    const domains: number[][] = Array.from({ length: n }, (_, i) =>
      fixed[i] !== null
        ? [idxOf.get(fixed[i]!)!]
        : Array.from({ length: k }, (_, c) => c),
    );
    function backtrack(v: number): boolean {
      if (v === n) return true;
      if (fixed[v] !== null) return backtrack(v + 1);
      for (const c of domains[v]) {
        let conflict = false;
        for (let u = 0; u < n; u += 1) {
          if (overlap[v][u] && colors[u] === c) {
            conflict = true;
            break;
          }
        }
        if (conflict) continue;
        colors[v] = c;
        if (backtrack(v + 1)) return true;
        colors[v] = -1;
      }
      return false;
    }
    ok = backtrack(0);
    if (ok) return k;
  }
  return n; // 必然可达
}

test("短实例穷举最优：随机小实例与暴力参考对拍", () => {
  const rand = mulberry32(20261005);
  for (let trial = 0; trial < 300; trial += 1) {
    const n = Math.floor(rand() * 9); // 0..8 个区间
    const specs: IntervalSpec[] = [];
    for (let i = 0; i < n; i += 1) {
      const start = Math.floor(rand() * 8);
      const length = Math.floor(rand() * 5); // 0..4，含零长
      const spec: IntervalSpec = { id: `i${i}`, start, end: start + length };
      if (rand() < 0.35) {
        spec.resource = Math.floor(rand() * 5); // 含空洞的编号
      }
      specs.push(spec);
    }
    const expected = bruteForceOptimalCount(specs);
    const result = allocateResources(specs);
    if (expected === null) {
      assert.equal(result.feasible, false, `trial ${trial} 应报冲突`);
      assert.ok(result.conflicts.length > 0);
    } else {
      assert.equal(result.feasible, true, `trial ${trial} 应可行`);
      assert.equal(
        result.resourceCount,
        expected,
        `trial ${trial} 资源数非最优: ${JSON.stringify(specs)}`,
      );
      assert.equal(result.optimal, true);
      assertValidAllocation(specs, result);
    }
  }
});

test("逐资源时间表内容正确", () => {
  const specs: IntervalSpec[] = [
    { id: "a", start: 0, end: 2, resource: 4 },
    { id: "b", start: 2, end: 3 },
    { id: "c", start: 1, end: 4 },
    { id: "z", start: 1, end: 1 },
  ];
  const result = allocateResources(specs);
  assertValidAllocation(specs, result);
  const schedule4 = result.schedules.get(4)!;
  assert.deepEqual(
    schedule4.map((s) => s.id),
    ["a", "b"], // b 与 a 端点相接，复用资源 4
  );
  assert.equal(schedule4[0].fixed, true);
  assert.equal(schedule4[1].fixed, false);
  // 每个区间恰好出现在一个资源的时间表中
  const all = [...result.schedules.values()].flat().map((s) => s.id).sort();
  assert.deepEqual(all, ["a", "b", "c", "z"]);
});
