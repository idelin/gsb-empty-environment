/**
 * 任务排期场景验收测试：
 *   - 3 条硬约束绝对成立；
 *   - 全局最优恰好违反 2 条次要软约束（S3、S5），总惩罚 3；
 *   - 与独立暴力枚举交叉验证；
 *   - 分支定界剪枝有效。
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { buildSchedulingSolver, TASKS } from "../src/scheduling.ts";
import type { Assignment } from "../src/maxcsp.ts";

const solution = buildSchedulingSolver().solve();

/** 独立暴力枚举：遍历 5! 排列，硬约束过滤后取最小惩罚。 */
function bruteForceBest(): { cost: number; schedules: Record<string, number>[] } {
  const slots = [0, 1, 2, 3, 4];
  const permutations: number[][] = [];
  const permute = (prefix: number[], rest: number[]): void => {
    if (rest.length === 0) {
      permutations.push(prefix);
      return;
    }
    for (let i = 0; i < rest.length; i++) {
      permute([...prefix, rest[i]!], [...rest.slice(0, i), ...rest.slice(i + 1)]);
    }
  };
  permute([], slots);

  const penalty = (a: Record<string, number>): number => {
    let cost = 0;
    if (a.T1 !== 0) cost += 10; // S1
    if (a.T2 !== 1) cost += 8; // S2
    if (a.T3 !== 2) cost += 2; // S3
    if (a.T4 !== 3) cost += 4; // S4
    if (a.T5 !== a.T3! + 2) cost += 1; // S5
    return cost;
  };

  let best = Infinity;
  let schedules: Record<string, number>[] = [];
  for (const p of permutations) {
    const a: Record<string, number> = { T1: p[0]!, T2: p[1]!, T3: p[2]!, T4: p[3]!, T5: p[4]! };
    if (a.T1! > 2) continue; // H2
    if (!(a.T4! < a.T3!)) continue; // H3（H1 由排列天然满足）
    const cost = penalty(a);
    if (cost < best) {
      best = cost;
      schedules = [a];
    } else if (cost === best) {
      schedules.push(a);
    }
  }
  return { cost: best, schedules };
}

test("求解成功且总惩罚最小（= 暴力枚举最优值 3）", () => {
  assert.ok(solution, "问题应当可行");
  const brute = bruteForceBest();
  assert.equal(brute.cost, 3, "暴力枚举的全局最优惩罚应为 3");
  assert.equal(solution!.cost, brute.cost, "分支定界结果应等于暴力枚举最优值");
});

test("恰好违反 2 条次要软约束：S3 与 S5", () => {
  assert.ok(solution);
  assert.equal(solution!.violations.length, 2);
  const names = solution!.violations.map((v) => v.name).sort();
  assert.deepEqual(names, ["S3", "S5"]);
  // 两条均为低权重次要约束（权重 2 与 1，为全部软约束中最低的两条）。
  const weights = solution!.violations.map((v) => v.weight).sort((x, y) => x - y);
  assert.deepEqual(weights, [1, 2]);
});

test("3 条硬约束在最优解中绝对成立", () => {
  assert.ok(solution);
  const a: Assignment = solution!.assignment;
  // H1 资源独占：5 个任务占用 5 个不同时段。
  const used = TASKS.map((t) => a[t.id]);
  assert.equal(new Set(used).size, TASKS.length);
  // H2 硬性截止：需求评审 ≤ 周三（slot 2）。
  assert.ok((a.T1 as number) <= 2);
  // H3 合规前置：宣传物料早于版本上线。
  assert.ok((a.T4 as number) < (a.T3 as number));
});

test("最优排期表内容符合预期", () => {
  assert.ok(solution);
  assert.deepEqual(solution!.assignment, {
    T1: 0, // 需求评审 → 周一（S1 满足）
    T2: 1, // 原型设计 → 周二（S2 满足）
    T5: 2, // 备份演练 → 周三
    T4: 3, // 宣传物料 → 周四（S4 满足）
    T3: 4, // 版本上线 → 周五（S3 被违反）
  });
});

test("分支定界剪枝显著减少搜索空间", () => {
  assert.ok(solution);
  const { stats } = solution!;
  assert.equal(stats.totalLeafSpace, 5 ** 5);
  assert.ok(stats.nodesPruned > 0, "应发生下界剪枝");
  assert.ok(stats.hardPruned > 0, "应发生硬约束剪枝");
  assert.ok(
    stats.leavesVisited <= 120,
    `硬约束过滤后叶子不应超过 5! = 120，实际 ${stats.leavesVisited}`,
  );
  assert.ok(
    stats.nodesExplored < 1000,
    `展开节点应远小于蛮力规模，实际 ${stats.nodesExplored}`,
  );
});
