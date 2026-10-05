import type {
  CompiledProblem,
  CompiledRule,
  Literal,
  ProblemInput,
  RuleInput,
} from "./types.ts";

export const MAX_VARIABLES = 25;

const NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;

/** 解析字面量，支持 "x" 与 "!x" 两种形式。 */
export function parseLiteral(text: unknown): Literal {
  if (typeof text !== "string" || text.length === 0) {
    throw new Error(`字面量必须是非空字符串，收到：${JSON.stringify(text)}`);
  }
  const negated = text.startsWith("!");
  const variable = negated ? text.slice(1) : text;
  if (!NAME_PATTERN.test(variable)) {
    throw new Error(`非法的字面量：${JSON.stringify(text)}`);
  }
  return { variable, negated };
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** 校验并编译问题输入，非法输入抛出带中文说明的 Error。 */
export function compileProblem(input: unknown): CompiledProblem {
  if (!isPlainObject(input)) {
    throw new Error("输入必须是一个 JSON 对象");
  }

  const rawVariables = input.variables;
  if (!Array.isArray(rawVariables) || rawVariables.length === 0) {
    throw new Error("variables 必须是非空数组");
  }
  const varNames: string[] = [];
  const varIndex = new Map<string, number>();
  for (const name of rawVariables) {
    if (typeof name !== "string" || !NAME_PATTERN.test(name)) {
      throw new Error(`非法的变量名：${JSON.stringify(name)}`);
    }
    if (varIndex.has(name)) {
      throw new Error(`变量名重复：${name}`);
    }
    varIndex.set(name, varNames.length);
    varNames.push(name);
  }
  if (varNames.length > MAX_VARIABLES) {
    throw new Error(`变量数 ${varNames.length} 超过上限 ${MAX_VARIABLES}`);
  }

  const rawAssignment = input.assignment;
  if (!isPlainObject(rawAssignment)) {
    throw new Error("assignment 必须是对象，覆盖每个变量的当前取值");
  }
  const initial = new Uint8Array(varNames.length);
  for (let i = 0; i < varNames.length; i++) {
    const value = rawAssignment[varNames[i]];
    if (typeof value !== "boolean") {
      throw new Error(`assignment 缺少变量 ${varNames[i]} 的布尔取值`);
    }
    initial[i] = value ? 1 : 0;
  }
  for (const key of Object.keys(rawAssignment)) {
    if (!varIndex.has(key)) {
      throw new Error(`assignment 中出现未声明的变量：${key}`);
    }
  }

  const fixed = new Array<boolean>(varNames.length).fill(true);
  const flipCost = new Array<number>(varNames.length).fill(0);
  const rawModifiable = input.modifiable ?? {};
  if (!isPlainObject(rawModifiable)) {
    throw new Error("modifiable 必须是对象：变量名 -> 翻转成本");
  }
  for (const [name, cost] of Object.entries(rawModifiable)) {
    const idx = varIndex.get(name);
    if (idx === undefined) {
      throw new Error(`modifiable 中出现未声明的变量：${name}`);
    }
    if (typeof cost !== "number" || !Number.isFinite(cost) || cost < 0) {
      throw new Error(`变量 ${name} 的翻转成本必须是非负有限数，收到：${JSON.stringify(cost)}`);
    }
    fixed[idx] = false;
    flipCost[idx] = cost;
  }

  const rawRules = input.rules;
  if (!Array.isArray(rawRules)) {
    throw new Error("rules 必须是数组");
  }
  const usedNames = new Set<string>();
  const rules: CompiledRule[] = rawRules.map((raw: unknown, i: number) => {
    const rule = raw as RuleInput;
    if (!isPlainObject(rule)) {
      throw new Error(`第 ${i + 1} 条规则必须是对象`);
    }
    const name = rule.name ?? `r${i + 1}`;
    if (typeof name !== "string" || name.length === 0) {
      throw new Error(`第 ${i + 1} 条规则的 name 非法`);
    }
    if (usedNames.has(name)) {
      throw new Error(`规则名重复：${name}`);
    }
    usedNames.add(name);
    if (!Array.isArray(rule.premise)) {
      throw new Error(`规则 ${name} 的 premise 必须是字面量数组`);
    }
    const premiseLits = rule.premise.map(parseLiteral);
    const conclusionLit = parseLiteral(rule.conclusion);

    const toIndex = (lit: Literal): number => {
      const idx = varIndex.get(lit.variable);
      if (idx === undefined) {
        throw new Error(`规则 ${name} 引用了未声明的变量：${lit.variable}`);
      }
      return idx;
    };

    return {
      name,
      premiseVars: premiseLits.map(toIndex),
      premiseVals: premiseLits.map((lit) => (lit.negated ? 0 : 1)),
      conclVar: toIndex(conclusionLit),
      conclVal: conclusionLit.negated ? 0 : 1,
      premiseText: premiseLits.map((lit) => (lit.negated ? `!${lit.variable}` : lit.variable)),
      conclusionText: conclusionLit.negated ? `!${conclusionLit.variable}` : conclusionLit.variable,
    };
  });

  return { varNames, initial, fixed, flipCost, rules };
}
