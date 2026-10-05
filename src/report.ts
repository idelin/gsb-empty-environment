import type {
  CompiledProblem,
  CompiledRule,
  RuleCheck,
  SolveResult,
} from "./types.ts";

function ruleText(rule: CompiledRule): string {
  const premise = rule.premiseText.length > 0 ? rule.premiseText.join(" ∧ ") : "⊤";
  return `${premise} → ${rule.conclusionText}`;
}

function boolText(value: boolean): string {
  return value ? "true" : "false";
}

/** 变量在当前解中的标注：固定 / 已翻转 / 可改未动。 */
function varTag(problem: CompiledProblem, name: string, flipped: boolean): string {
  const idx = problem.varNames.indexOf(name);
  if (problem.fixed[idx]) return "固定";
  return flipped ? "已翻转" : "可改";
}

function renderCheckLine(
  problem: CompiledProblem,
  rule: CompiledRule,
  check: RuleCheck,
  flippedVars: Set<string>,
): string {
  const premiseDetail = check.premiseValues
    .map((p) => {
      const varName = p.text.startsWith("!") ? p.text.slice(1) : p.text;
      return `${p.text}=${boolText(p.value)}[${varTag(problem, varName, flippedVars.has(varName))}]`;
    })
    .join(", ");
  const conclusionVar = rule.conclusionText.startsWith("!")
    ? rule.conclusionText.slice(1)
    : rule.conclusionText;
  const conclusionDetail =
    `${rule.conclusionText}=${boolText(check.conclusionValue)}` +
    `[${varTag(problem, conclusionVar, flippedVars.has(conclusionVar))}]`;

  const mark = check.satisfied ? "✓" : "✗";
  let reason: string;
  if (!check.premiseHolds) {
    reason = "前提不成立，规则满足";
  } else if (check.conclusionHolds) {
    reason = "前提成立且结论成立，规则满足";
  } else {
    reason = "前提成立但结论不成立，规则被违反";
  }
  const premisePart = check.premiseValues.length > 0 ? `前提（${premiseDetail}）` : "前提（空，恒真）";
  return `  ${mark} ${check.name} [${ruleText(rule)}]：${premisePart}；结论 ${conclusionDetail} —— ${reason}`;
}

/** 生成人类可读的求解报告。 */
export function renderReport(problem: CompiledProblem, result: SolveResult): string {
  const lines: string[] = [];
  const modifiable = problem.varNames.filter((_, i) => !problem.fixed[i]);
  lines.push(
    `变量 ${problem.varNames.length} 个（可修改 ${modifiable.length} 个，固定 ${problem.varNames.length - modifiable.length} 个），规则 ${problem.rules.length} 条`,
  );

  if (result.satisfiable) {
    const flippedVars = new Set(result.flips.map((f) => f.variable));
    lines.push("", `修复成功：最低成本 = ${result.cost}，共 ${result.flips.length} 处改动`, "");
    lines.push("【改动列表】");
    if (result.flips.length === 0) {
      lines.push("  （无需改动，原始赋值已满足全部规则）");
    } else {
      result.flips.forEach((flip, i) => {
        lines.push(
          `  ${i + 1}. ${flip.variable}: ${boolText(flip.from)} → ${boolText(flip.to)}（成本 ${flip.cost}）`,
        );
      });
    }
    lines.push("", `【规则复核】共 ${result.ruleChecks.length} 条，全部满足`);
    result.ruleChecks.forEach((check, i) => {
      lines.push(renderCheckLine(problem, problem.rules[i], check, flippedVars));
    });
    return lines.join("\n");
  }

  lines.push("", "无法修复：只翻转可修改变量，不存在满足全部规则的组合", "");
  lines.push(
    `【冲突核心】${result.core.length} 条规则（全部规则共 ${problem.rules.length} 条；核心中再删去任意一条即恢复可解）`,
  );
  lines.push("（以下为核心规则在原始赋值下的复核）");
  const coreFixedVars = new Set<string>();
  const emptyFlips = new Set<string>();
  for (const coreRule of result.core) {
    const check = result.ruleChecks.find((c) => c.name === coreRule.name);
    if (check) {
      lines.push(renderCheckLine(problem, coreRule, check, emptyFlips));
    }
    const involved = [...coreRule.premiseText, coreRule.conclusionText].map((t) =>
      t.startsWith("!") ? t.slice(1) : t,
    );
    for (const name of involved) {
      const idx = problem.varNames.indexOf(name);
      if (problem.fixed[idx]) coreFixedVars.add(name);
    }
  }
  lines.push("");
  if (coreFixedVars.size > 0) {
    const detail = [...coreFixedVars]
      .map((name) => `${name}=${boolText(result.finalAssignment[name])}`)
      .join(", ");
    lines.push(`核心涉及的不可修改变量：${detail}`);
    lines.push("上述变量被固定，核心内的规则因此无法同时满足，即冲突根源。");
  } else {
    lines.push("核心内不含不可修改变量（理论上不应出现，请检查输入）。");
  }
  return lines.join("\n");
}
