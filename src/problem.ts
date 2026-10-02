import type { CspProblem } from "./csp.ts";

/**
 * 验收基准：10 变量 CSP。
 *
 * 结构（层级 = 变量序号）：
 * - X1..X7 的取值与冲突无关：X3..X7 之间只有松弛的相邻不等约束，
 *   首轮取值即可顺利通过；
 * - X8 与 X2 之间存在硬约束 X8 <= X2 + 1，而 X8 的值域为 {3, 4}：
 *   X2 首选 1 时，X8 的每个值都只与 X2 冲突 —— 第 8 步死胡同的
 *   冲突集为 {X2}，CBJ 应直接跳过第 3..7 步回跳到第 2 步；
 * - X2 改取 2 后 X8 = 3 可行，X9、X10 顺利赋值，问题有解。
 *
 * 时序回溯则会在 X3..X7 的 3^5 棵子树里做大量无用功。
 */
export function buildBenchmarkProblem(): CspProblem {
  const varNames = Array.from({ length: 10 }, (_, k) => `X${k + 1}`);
  const domains = [
    [1, 2], // X1：自由变量
    [1, 2], // X2：首选 1 会在第 8 步引发冲突
    [1, 2, 3], // X3
    [1, 2, 3], // X4
    [1, 2, 3], // X5
    [1, 2, 3], // X6
    [1, 2, 3], // X7
    [3, 4], // X8：受 X2 钳制
    [1, 2, 3], // X9
    [1, 2, 3], // X10
  ];
  return {
    varNames,
    domains,
    constraints: [
      { a: 2, b: 3, name: "X3 != X4", holds: (x, y) => x !== y },
      { a: 3, b: 4, name: "X4 != X5", holds: (x, y) => x !== y },
      { a: 4, b: 5, name: "X5 != X6", holds: (x, y) => x !== y },
      { a: 5, b: 6, name: "X6 != X7", holds: (x, y) => x !== y },
      { a: 1, b: 7, name: "X8 <= X2 + 1", holds: (x2, x8) => x8 <= x2 + 1 },
      { a: 7, b: 8, name: "X8 != X9", holds: (x, y) => x !== y },
      { a: 8, b: 9, name: "X9 != X10", holds: (x, y) => x !== y },
    ],
  };
}

/** 不可满足变体：X2 只能取 1，第 8 步的矛盾无法通过回跳消除。 */
export function buildUnsatisfiableProblem(): CspProblem {
  const p = buildBenchmarkProblem();
  return { ...p, domains: p.domains.map((d, k) => (k === 1 ? [1] : d)) };
}
