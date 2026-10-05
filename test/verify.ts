/**
 * 对拍测试：
 * 1. 随机生成小规模问题（n <= 7），用全排列暴力枚举验证 DP 结果的最优性与可行性判断。
 * 2. 无解时验证返回的冲突子集确实不可满足，且去掉任意一个样例后即可满足（包含极小）。
 */

import { solve, ruleMatches, feasibleSubset } from '../src/sorter.ts';
import type { Problem, Rule, Sample } from '../src/sorter.ts';

function bruteForceMinSwaps(problem: Problem): number | null {
  const n = problem.rules.length;
  const perm = Array.from({ length: n }, (_, i) => i);
  let best: number | null = null;

  const valid = (p: number[]): boolean =>
    problem.samples.every((s) => {
      for (const ri of p) {
        if (ruleMatches(problem.rules[ri], s)) {
          return problem.rules[ri].action === s.expect;
        }
      }
      return false; // 无规则匹配 -> 样例无法满足
    });

  const inversions = (p: number[]): number => {
    let inv = 0;
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        if (p[i] > p[j]) inv++;
      }
    }
    return inv;
  };

  const visit = (k: number): void => {
    if (k === n) {
      if (valid(perm)) {
        const inv = inversions(perm);
        if (best === null || inv < best) best = inv;
      }
      return;
    }
    for (let i = k; i < n; i++) {
      [perm[k], perm[i]] = [perm[i], perm[k]];
      visit(k + 1);
      [perm[k], perm[i]] = [perm[i], perm[k]];
    }
  };
  visit(0);
  return best;
}

// 确定性伪随机数（可复现）
function makeRng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}

function randomProblem(rng: () => number): Problem {
  const pick = <T>(arr: T[]): T => arr[Math.floor(rng() * arr.length)];
  const n = 2 + Math.floor(rng() * 6); // 2..7 条规则
  const users = ['alice', 'bob', 'carol'];
  const ops = ['read', 'write'];
  const envs = ['prod', 'dev'];

  const rules: Rule[] = Array.from({ length: n }, (_, i) => {
    const when: Record<string, string> = {};
    if (rng() < 0.7) when.user = rng() < 0.3 ? '*' : pick(users);
    if (rng() < 0.6) when.op = rng() < 0.3 ? '*' : pick(ops);
    if (rng() < 0.4) when.env = rng() < 0.3 ? '*' : pick(envs);
    return { id: `r${i}`, action: rng() < 0.5 ? 'allow' : 'deny', when };
  });

  const m = 1 + Math.floor(rng() * 8); // 1..8 个样例
  const samples: Sample[] = Array.from({ length: m }, (_, i) => ({
    id: `s${i}`,
    expect: rng() < 0.5 ? 'allow' : 'deny',
    attrs: { user: pick(users), op: pick(ops), env: pick(envs) },
  }));

  return { rules, samples };
}

function inversionsOfOrder(problem: Problem, order: string[]): number {
  const pos = new Map(problem.rules.map((r, i) => [r.id, i]));
  const seq = order.map((id) => pos.get(id)!);
  let inv = 0;
  for (let i = 0; i < seq.length; i++) {
    for (let j = i + 1; j < seq.length; j++) {
      if (seq[i] > seq[j]) inv++;
    }
  }
  return inv;
}

let failures = 0;
const CASES = 300;
for (let t = 0; t < CASES; t++) {
  const problem = randomProblem(makeRng(t * 7919 + 13));
  const result = solve(problem);
  const brute = bruteForceMinSwaps(problem);

  if (brute === null) {
    if (result.ok) {
      console.error(`case ${t}: DP 判有解但暴力判无解`);
      failures++;
      continue;
    }
    // 冲突子集本身必须不可满足
    const idxs = result.conflictSamples.map((cs) =>
      problem.samples.findIndex((s) => s.id === cs.id),
    );
    if (feasibleSubset(problem, idxs)) {
      console.error(`case ${t}: 冲突子集实际可满足`);
      failures++;
      continue;
    }
    // 包含极小性：去掉任意一个后必须可满足
    for (let k = 0; k < idxs.length; k++) {
      const rest = [...idxs.slice(0, k), ...idxs.slice(k + 1)];
      if (!feasibleSubset(problem, rest)) {
        console.error(`case ${t}: 冲突子集非极小（去掉 ${problem.samples[idxs[k]].id} 仍无解）`);
        failures++;
        break;
      }
    }
  } else {
    if (!result.ok) {
      console.error(`case ${t}: DP 判无解但暴力最优 = ${brute}`);
      failures++;
      continue;
    }
    if (result.swaps !== brute) {
      console.error(`case ${t}: 交换次数不一致 dp=${result.swaps} brute=${brute}`);
      failures++;
      continue;
    }
    // 新顺序必须是原规则的一个排列，且逆序对数等于报告的交换次数
    const sorted = [...result.order].sort();
    const expectSorted = problem.rules.map((r) => r.id).sort();
    if (JSON.stringify(sorted) !== JSON.stringify(expectSorted)) {
      console.error(`case ${t}: 输出顺序不是原规则集的排列`);
      failures++;
      continue;
    }
    if (inversionsOfOrder(problem, result.order) !== result.swaps) {
      console.error(`case ${t}: 输出顺序的逆序对数与报告交换次数不符`);
      failures++;
      continue;
    }
    // 每个样例的命中规则动作必须等于期望
    for (const hit of result.hits) {
      if (hit.action !== hit.expect) {
        console.error(`case ${t}: 样例 ${hit.sampleId} 命中动作错误`);
        failures++;
        break;
      }
    }
  }
}

if (failures > 0) {
  console.error(`失败 ${failures} 处`);
  process.exit(1);
}
console.log(`全部 ${CASES} 个随机用例通过（DP 与暴力枚举一致，冲突子集满足极小性）`);
