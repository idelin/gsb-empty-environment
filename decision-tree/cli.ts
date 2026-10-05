/**
 * 命令行入口：
 *   node cli.mjs                 运行内置演示（含不可区分等价组）
 *   node cli.mjs problem.json    求解 JSON 描述的问题
 *   node cli.mjs --verify        随机实例对拍：记忆化 DP vs 无记忆朴素极小化极大
 *   node cli.mjs --bench18       18 个候选的随机基准（上限规模）
 *
 * problem.json 格式：
 *   {
 *     "costs": { "颜色": 1, "形状": 2 },
 *     "candidates": [ { "id": "A", "attrs": { "颜色": "红", "形状": "圆" } }, ... ]
 *   }
 */
import {
  Candidate,
  Problem,
  Solution,
  formatTree,
  simulate,
  solveProblem,
} from "./decision-tree";
import { readFileSync } from "fs";

function demoProblem(): Problem {
  return {
    costs: { 颜色: 1, 形状: 2, 材质: 3, 尺寸: 5 },
    candidates: [
      { id: "A", attrs: { 颜色: "红", 形状: "圆", 材质: "木", 尺寸: "大" } },
      { id: "B", attrs: { 颜色: "红", 形状: "圆", 材质: "木", 尺寸: "大" } }, // 与 A 不可区分
      { id: "C", attrs: { 颜色: "红", 形状: "方", 材质: "木", 尺寸: "小" } },
      { id: "D", attrs: { 颜色: "蓝", 形状: "圆", 材质: "铁", 尺寸: "大" } },
      { id: "E", attrs: { 颜色: "蓝", 形状: "方", 材质: "铁", 尺寸: "小" } },
      { id: "F", attrs: { 颜色: "绿", 形状: "圆", 材质: "木", 尺寸: "大" } },
      { id: "G", attrs: { 颜色: "绿", 形状: "方", 材质: "铁", 尺寸: "大" } },
      { id: "H", attrs: { 颜色: "绿", 形状: "圆", 材质: "铁", 尺寸: "小" } },
      { id: "I", attrs: { 颜色: "黄", 形状: "圆", 材质: "木", 尺寸: "小" } },
      { id: "J", attrs: { 颜色: "黄", 形状: "方", 材质: "木", 尺寸: "小" } },
      { id: "K", attrs: { 颜色: "黄", 形状: "方", 材质: "木", 尺寸: "小" } }, // 与 J 不可区分
      { id: "L", attrs: { 颜色: "黄", 形状: "方", 材质: "铁", 尺寸: "大" } },
    ],
  };
}

/** 独立实现的朴素极小化极大（无记忆化、无剪枝），用于对拍验证。 */
function naiveSolve(cands: Candidate[], costs: Record<string, number>): number {
  const attrs = Object.keys(costs);
  const sig = (c: Candidate) => JSON.stringify(attrs.map((a) => typeof c.attrs[a] + ":" + c.attrs[a]));
  if (cands.length <= 1) return 0;
  if (cands.every((c) => sig(c) === sig(cands[0]))) return 0;
  let best = Infinity;
  for (const a of attrs) {
    const groups = new Map<string, Candidate[]>();
    for (const c of cands) {
      const k = typeof c.attrs[a] + ":" + c.attrs[a];
      const g = groups.get(k);
      if (g) g.push(c);
      else groups.set(k, [c]);
    }
    if (groups.size < 2) continue;
    let worst = 0;
    for (const g of groups.values()) worst = Math.max(worst, naiveSolve(g, costs));
    best = Math.min(best, costs[a] + worst);
  }
  return best;
}

function randomProblem(rng: () => number, n: number, nAttrs: number, nValues: number): Problem {
  const costs: Record<string, number> = {};
  for (let a = 0; a < nAttrs; a++) costs["attr" + a] = 1 + Math.floor(rng() * 9);
  const candidates: Candidate[] = [];
  for (let i = 0; i < n; i++) {
    const attrs: Record<string, string> = {};
    for (let a = 0; a < nAttrs; a++) attrs["attr" + a] = "v" + Math.floor(rng() * nValues);
    candidates.push({ id: "c" + i, attrs });
  }
  return { candidates, costs };
}

/** 简单可复现的 LCG 随机数发生器。 */
function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 0x100000000;
  };
}

function checkSolution(problem: Problem, sol: Solution): string[] {
  const errors: string[] = [];
  // 1) 树模拟出的最坏路径成本必须等于 DP 值。
  const paths = simulate(sol.tree);
  const worst = Math.max(...paths.map((p) => p.pathCost));
  if (worst !== sol.worstCaseCost) {
    errors.push(`模拟最坏成本 ${worst} != DP 值 ${sol.worstCaseCost}`);
  }
  // 2) 叶子划分必须恰好等于“全属性签名”的等价类划分。
  const attrs = Object.keys(problem.costs);
  const sig = (c: Candidate) => JSON.stringify(attrs.map((a) => typeof c.attrs[a] + ":" + c.attrs[a]));
  const expected = new Map<string, string[]>();
  for (const c of problem.candidates) {
    const k = sig(c);
    const g = expected.get(k);
    if (g) g.push(c.id);
    else expected.set(k, [c.id]);
  }
  const actual = paths.map((p) => [...p.leaf].sort().join(",")).sort();
  const want = [...expected.values()].map((g) => [...g].sort().join(",")).sort();
  if (actual.join("|") !== want.join("|")) {
    errors.push(`叶子等价类划分不匹配：实际 ${actual.join("|")}，期望 ${want.join("|")}`);
  }
  return errors;
}

function verify(): void {
  const rng = lcg(20261005);
  let failed = 0;
  for (let t = 0; t < 300; t++) {
    const n = 2 + Math.floor(rng() * 7); // 2..8 个候选
    const nAttrs = 2 + Math.floor(rng() * 3); // 2..4 个属性
    const nValues = 1 + Math.floor(rng() * 3); // 1..3 个取值（常出现等价组）
    const problem = randomProblem(rng, n, nAttrs, nValues);
    const sol = solveProblem(problem);
    const naive = naiveSolve(problem.candidates, problem.costs);
    const errs = checkSolution(problem, sol);
    if (sol.worstCaseCost !== naive) {
      errs.push(`DP 值 ${sol.worstCaseCost} != 朴素极小化极大 ${naive}`);
    }
    if (errs.length) {
      failed++;
      console.error(`用例 ${t} 失败：\n  ${errs.join("\n  ")}`);
    }
  }
  console.log(failed === 0 ? "对拍通过：300 个随机实例全部一致。" : `失败 ${failed} 个用例。`);
  if (failed) process.exitCode = 1;
}

function bench18(): void {
  const rng = lcg(42);
  const problem = randomProblem(rng, 18, 5, 3);
  const t0 = Date.now();
  const sol = solveProblem(problem);
  const ms = Date.now() - t0;
  console.log(`18 候选 / 5 属性：最坏总成本 = ${sol.worstCaseCost}，状态数 = ${sol.statesExplored}，耗时 ${ms}ms`);
  const errs = checkSolution(problem, sol);
  if (errs.length) {
    console.error("校验失败：\n  " + errs.join("\n  "));
    process.exitCode = 1;
  } else {
    console.log("树结构校验通过（叶子划分 == 等价类划分，路径成本 == DP 值）。");
  }
}

function main(): void {
  const args = process.argv.slice(2);
  if (args.includes("--verify")) return verify();
  if (args.includes("--bench18")) return bench18();

  let problem: Problem;
  const file = args.find((a) => !a.startsWith("-"));
  if (file) {
    problem = JSON.parse(readFileSync(file, "utf8")) as Problem;
  } else {
    problem = demoProblem();
    console.log("内置演示（12 候选，含两组不可区分对象 {A,B} 与 {J,K}）：\n");
  }
  const sol = solveProblem(problem);
  console.log(formatTree(sol));
  const errs = checkSolution(problem, sol);
  if (errs.length) {
    console.error("\n校验失败：\n  " + errs.join("\n  "));
    process.exitCode = 1;
  } else {
    console.log("\n校验通过：叶子即等价类划分，各路径成本均不超过最坏总成本。");
  }
}

main();
