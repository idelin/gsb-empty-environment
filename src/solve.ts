import { extractMinimalCore } from "./core.ts";
import { search } from "./solver.ts";
import type {
  CompiledProblem,
  CompiledRule,
  Flip,
  RuleCheck,
  SolveResult,
} from "./types.ts";

/** 在给定赋值下逐条复核规则。 */
export function checkRules(
  problem: CompiledProblem,
  rules: CompiledRule[],
  values: Uint8Array,
): RuleCheck[] {
  return rules.map((rule) => {
    const premiseValues = rule.premiseVars.map((v, i) => ({
      text: rule.premiseText[i],
      value: values[v] === rule.premiseVals[i],
    }));
    const premiseHolds = premiseValues.every((p) => p.value);
    const conclusionHolds = values[rule.conclVar] === rule.conclVal;
    return {
      name: rule.name,
      satisfied: !premiseHolds || conclusionHolds,
      premiseHolds,
      conclusionHolds,
      premiseValues,
      conclusionValue: conclusionHolds,
    };
  });
}

function toAssignment(problem: CompiledProblem, values: Uint8Array): Record<string, boolean> {
  const result: Record<string, boolean> = {};
  problem.varNames.forEach((name, i) => {
    result[name] = values[i] === 1;
  });
  return result;
}

/** 求解问题：成功时给出最低成本翻转方案，失败时给出最小冲突核心。 */
export function solveProblem(problem: CompiledProblem): SolveResult {
  const outcome = search(problem, problem.rules, "optimize");

  if (outcome.satisfiable && outcome.values !== null) {
    const finalValues = outcome.values;
    const flips: Flip[] = [];
    for (let v = 0; v < problem.varNames.length; v++) {
      if (finalValues[v] !== problem.initial[v]) {
        flips.push({
          variable: problem.varNames[v],
          from: problem.initial[v] === 1,
          to: finalValues[v] === 1,
          cost: problem.flipCost[v],
        });
      }
    }
    return {
      satisfiable: true,
      cost: outcome.cost,
      flips,
      finalAssignment: toAssignment(problem, finalValues),
      ruleChecks: checkRules(problem, problem.rules, finalValues),
      core: [],
    };
  }

  const core = extractMinimalCore(problem, problem.rules);
  return {
    satisfiable: false,
    cost: Number.POSITIVE_INFINITY,
    flips: [],
    finalAssignment: toAssignment(problem, problem.initial),
    ruleChecks: checkRules(problem, problem.rules, problem.initial),
    core,
  };
}
