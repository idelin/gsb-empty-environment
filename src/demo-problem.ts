import type { CspProblem } from "./csp.ts";

/**
 * 验收场景：10 变量 CSP。
 * - X1..X7, X9, X10 域为 {1,2,3}，X8 域为 {1}。
 * - X3..X7 无任何约束（纯过渡变量）。
 * - 唯一关键约束 X8 ≠ X2：搜索按字典序先取 X2 = 1，
 *   到第 8 步 X8 唯一取值 1 与之冲突，域耗尽。
 * - 冲突分析：X8 的 conflict set = {X2}，最深层根源是第 2 步，
 *   CBJ 直接跳过第 3~7 步回跳到 X2。
 */
export function buildDemoProblem(): CspProblem {
  const full = [1, 2, 3] as const;
  return {
    numVars: 10,
    varNames: Array.from({ length: 10 }, (_, i) => `X${i + 1}`),
    domains: [full, full, full, full, full, full, full, [1], full, full],
    constraints: [
      { a: 7, b: 1, check: (x8, x2) => x8 !== x2, label: "X8 ≠ X2" },
      { a: 8, b: 0, check: (x9, x1) => x9 !== x1, label: "X9 ≠ X1" },
      { a: 9, b: 8, check: (x10, x9) => x10 !== x9, label: "X10 ≠ X9" },
      { a: 9, b: 1, check: (x10, x2) => x10 !== x2, label: "X10 ≠ X2" },
    ],
  };
}
