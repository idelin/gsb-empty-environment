import type { CompiledProblem, CompiledRule } from "./types.ts";

export interface SearchOutcome {
  satisfiable: boolean;
  /** optimize 模式下的最低成本；无解时为 Infinity。 */
  cost: number;
  /** 找到的一组赋值（下标与 problem.varNames 对齐）；无解时为 null。 */
  values: Uint8Array | null;
}

interface Occurrence {
  rule: number;
  desired: number;
}

/**
 * 分支限界搜索。
 * - optimize：枚举可修改变量的翻转组合，返回总成本最低的满足赋值；
 * - exists：找到任意一个满足赋值即停（用于冲突核心提取）。
 *
 * 规则 (p1 ∧ … ∧ pk → c) 被违反 当且仅当 前提全部为真且结论为假。
 * 搜索过程中用增量计数器维护每条规则的状态：
 *   premiseFalse[r] 前提中已确定为假的字面量数（>0 则规则必满足）；
 *   premiseOpen[r]  前提中尚未决定的字面量数；
 *   conclState[r]   结论状态（0 未定，1 成立，-1 被否定）。
 * 三者共同判定规则是否已无可挽回地被违反，从而提前剪枝。
 */
export function search(
  problem: CompiledProblem,
  rules: CompiledRule[],
  mode: "optimize" | "exists",
): SearchOutcome {
  const varCount = problem.varNames.length;
  const ruleCount = rules.length;

  const premiseOcc: Occurrence[][] = Array.from({ length: varCount }, () => []);
  const conclOcc: Occurrence[][] = Array.from({ length: varCount }, () => []);
  rules.forEach((rule, r) => {
    rule.premiseVars.forEach((v, i) => {
      premiseOcc[v].push({ rule: r, desired: rule.premiseVals[i] });
    });
    conclOcc[rule.conclVar].push({ rule: r, desired: rule.conclVal });
  });

  const decided = new Uint8Array(varCount);
  const values = new Uint8Array(varCount);
  for (let v = 0; v < varCount; v++) {
    if (problem.fixed[v]) {
      decided[v] = 1;
      values[v] = problem.initial[v];
    }
  }

  const premiseFalse = new Int32Array(ruleCount);
  const premiseOpen = new Int32Array(ruleCount);
  const conclState = new Int8Array(ruleCount);
  for (let r = 0; r < ruleCount; r++) {
    const rule = rules[r];
    for (let i = 0; i < rule.premiseVars.length; i++) {
      const v = rule.premiseVars[i];
      if (decided[v]) {
        if (values[v] !== rule.premiseVals[i]) premiseFalse[r]++;
      } else {
        premiseOpen[r]++;
      }
    }
    if (decided[rule.conclVar]) {
      conclState[r] = values[rule.conclVar] === rule.conclVal ? 1 : -1;
    }
  }

  const isDead = (r: number): boolean =>
    premiseFalse[r] === 0 && premiseOpen[r] === 0 && conclState[r] === -1;

  for (let r = 0; r < ruleCount; r++) {
    if (isDead(r)) {
      return { satisfiable: false, cost: Number.POSITIVE_INFINITY, values: null };
    }
  }

  const order: number[] = [];
  for (let v = 0; v < varCount; v++) {
    if (!problem.fixed[v]) order.push(v);
  }
  order.sort((a, b) => {
    const impactA = premiseOcc[a].length + conclOcc[a].length;
    const impactB = premiseOcc[b].length + conclOcc[b].length;
    if (impactB !== impactA) return impactB - impactA;
    return problem.flipCost[a] - problem.flipCost[b];
  });

  let bestCost = Number.POSITIVE_INFINITY;
  let bestFlips = Number.POSITIVE_INFINITY;
  let bestValues: Uint8Array | null = null;
  let flips = 0;

  const assign = (v: number, val: number): boolean => {
    decided[v] = 1;
    values[v] = val;
    let consistent = true;
    for (const occ of premiseOcc[v]) {
      premiseOpen[occ.rule]--;
      if (val !== occ.desired) premiseFalse[occ.rule]++;
      if (consistent && isDead(occ.rule)) consistent = false;
    }
    for (const occ of conclOcc[v]) {
      conclState[occ.rule] = val === occ.desired ? 1 : -1;
      if (consistent && isDead(occ.rule)) consistent = false;
    }
    return consistent;
  };

  const unassign = (v: number, val: number): void => {
    for (const occ of premiseOcc[v]) {
      premiseOpen[occ.rule]++;
      if (val !== occ.desired) premiseFalse[occ.rule]--;
    }
    for (const occ of conclOcc[v]) {
      conclState[occ.rule] = 0;
    }
    decided[v] = 0;
  };

  /** 返回 true 表示已找到结果，调用方应立即停止（仅 exists 模式）。 */
  const dfs = (i: number, cost: number): boolean => {
    if (cost > bestCost) return false;
    if (cost === bestCost && flips >= bestFlips) return false;
    if (i === order.length) {
      bestCost = cost;
      bestFlips = flips;
      bestValues = values.slice();
      return mode === "exists";
    }
    const v = order[i];
    const orig = problem.initial[v];

    // 优先尝试保持原值（零成本），再尝试翻转。
    if (assign(v, orig)) {
      if (dfs(i + 1, cost)) return true;
    }
    unassign(v, orig);

    const flipCost = cost + problem.flipCost[v];
    if (flipCost < bestCost || (flipCost === bestCost && flips + 1 < bestFlips)) {
      if (assign(v, 1 - orig)) {
        flips++;
        if (dfs(i + 1, flipCost)) return true;
        flips--;
      }
      unassign(v, 1 - orig);
    }
    return false;
  };

  dfs(0, 0);

  if (bestValues === null) {
    return { satisfiable: false, cost: Number.POSITIVE_INFINITY, values: null };
  }
  return { satisfiable: true, cost: bestCost, values: bestValues };
}
