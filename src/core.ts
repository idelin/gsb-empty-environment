import { search } from "./solver.ts";
import type { CompiledProblem, CompiledRule } from "./types.ts";

/**
 * 删除法提取最小冲突核心：在“允许翻转全部可修改变量也不可满足”的意义下，
 * 返回一个极小的规则子集——再删掉其中任意一条，剩余规则即可满足。
 * 仅在整组规则无解时调用。
 */
export function extractMinimalCore(
  problem: CompiledProblem,
  rules: CompiledRule[],
): CompiledRule[] {
  let core = rules.slice();
  let i = 0;
  while (i < core.length) {
    const candidate = core.slice(0, i).concat(core.slice(i + 1));
    if (!search(problem, candidate, "exists").satisfiable) {
      core = candidate;
    } else {
      i++;
    }
  }
  return core;
}
