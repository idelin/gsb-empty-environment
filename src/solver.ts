import type {
  Change,
  Instance,
  Literal,
  RuleCheck,
  SolveResult,
} from "./types.ts";

const MAX_VARIABLES = 25;

interface CompiledRule {
  index: number;
  id?: string;
  premise: Literal[];
  conclVar: string;
  conclValue: boolean;
  /** 前提中必须为 1 的位。 */
  posMask: number;
  /** 前提中必须为 0 的位。 */
  negMask: number;
  /** 结论变量的位号。 */
  conclBit: number;
  /** 该规则涉及的、可翻转变量的位置（指向 flip 数组的下标）。 */
  flips: number[];
}

interface Compiled {
  varNames: string[];
  base: number;
  flipVarIndex: number[];
  flipCost: number[];
  rules: CompiledRule[];
}

function fail(message: string): never {
  throw new Error(`invalid instance: ${message}`);
}

function compile(inst: Instance): Compiled {
  if (inst === null || typeof inst !== "object") fail("instance must be an object");
  const { variables, mutable = {}, rules } = inst;
  if (variables === null || typeof variables !== "object" || Array.isArray(variables))
    fail('"variables" must be an object of var -> boolean');
  if (!Array.isArray(rules)) fail('"rules" must be an array');
  if (mutable === null || typeof mutable !== "object" || Array.isArray(mutable))
    fail('"mutable" must be an object of var -> cost');

  const varNames = Object.keys(variables);
  if (varNames.length > MAX_VARIABLES)
    fail(`at most ${MAX_VARIABLES} variables allowed, got ${varNames.length}`);
  for (const [name, value] of Object.entries(variables))
    if (typeof value !== "boolean") fail(`variable "${name}" must be boolean`);

  const varIndex = new Map<string, number>();
  varNames.forEach((name, i) => varIndex.set(name, i));

  let base = 0;
  for (const [name, value] of Object.entries(variables))
    if (value) base |= 1 << (varIndex.get(name) as number);

  const flipVarIndex: number[] = [];
  const flipCost: number[] = [];
  const flipPosOfVar = new Map<string, number>();
  for (const [name, cost] of Object.entries(mutable)) {
    const idx = varIndex.get(name);
    if (idx === undefined) fail(`mutable variable "${name}" is not declared in "variables"`);
    if (typeof cost !== "number" || !Number.isFinite(cost) || cost < 0)
      fail(`flip cost of "${name}" must be a finite number >= 0`);
    flipPosOfVar.set(name, flipVarIndex.length);
    flipVarIndex.push(idx);
    flipCost.push(cost);
  }

  const compiledRules: CompiledRule[] = rules.map((rule, index) => {
    if (rule === null || typeof rule !== "object") fail(`rule ${index} must be an object`);
    const premise = rule.if;
    const concl = rule.then;
    if (!Array.isArray(premise)) fail(`rule ${index}: "if" must be an array of literals`);
    if (concl === null || typeof concl !== "object")
      fail(`rule ${index}: "then" must be a literal`);

    let posMask = 0;
    let negMask = 0;
    const flips = new Set<number>();
    const checkLiteral = (lit: Literal, where: string): number => {
      if (lit === null || typeof lit !== "object" || typeof lit.var !== "string" || typeof lit.value !== "boolean")
        fail(`rule ${index}: literal at ${where} must be {var: string, value: boolean}`);
      const idx = varIndex.get(lit.var);
      if (idx === undefined) fail(`rule ${index}: unknown variable "${lit.var}" at ${where}`);
      const fp = flipPosOfVar.get(lit.var);
      if (fp !== undefined) flips.add(fp);
      return idx;
    };

    premise.forEach((lit, i) => {
      const bit = 1 << checkLiteral(lit, `if[${i}]`);
      if (lit.value) {
        if (negMask & bit) fail(`rule ${index}: contradictory premise on "${lit.var}"`);
        posMask |= bit;
      } else {
        if (posMask & bit) fail(`rule ${index}: contradictory premise on "${lit.var}"`);
        negMask |= bit;
      }
    });
    const conclBit = checkLiteral(concl, "then");

    const compiled: CompiledRule = {
      index,
      premise,
      conclVar: concl.var,
      conclValue: concl.value,
      posMask,
      negMask,
      conclBit,
      flips: [...flips],
    };
    if (rule.id !== undefined) compiled.id = rule.id;
    return compiled;
  });

  return { varNames, base, flipVarIndex, flipCost, rules: compiledRules };
}

function isViolated(rule: CompiledRule, assignment: number): boolean {
  if ((assignment & rule.posMask) !== rule.posMask) return false;
  if ((assignment & rule.negMask) !== 0) return false;
  const actual = ((assignment >>> rule.conclBit) & 1) === 1;
  return actual !== rule.conclValue;
}

interface SearchOutcome {
  cost: number;
  flips: boolean[];
}

/**
 * 在 activeRules 上搜索最小成本翻转集合。
 * feasibilityOnly 为 true 时找到任意可行解即返回（用于冲突核心提取）。
 */
function searchMinCost(
  compiled: Compiled,
  activeRuleIdx: number[],
  feasibilityOnly: boolean,
): SearchOutcome | null {
  const { base, flipVarIndex, flipCost } = compiled;
  const k = flipVarIndex.length;
  const active = activeRuleIdx.map((i) => compiled.rules[i]);

  // 每个可翻转变量影响哪些规则（active 中的下标）。
  const rulesByFlip: number[][] = Array.from({ length: k }, () => []);
  active.forEach((rule, ai) => {
    for (const p of rule.flips) rulesByFlip[p].push(ai);
  });

  // suffixMin[ai][d]：修复该规则所需的、位置 >= d 的变量的最小成本。
  const suffixMin: Float64Array[] = active.map((rule) => {
    const arr = new Float64Array(k + 1).fill(Infinity);
    for (const p of rule.flips) arr[p] = Math.min(arr[p], flipCost[p]);
    for (let d = k - 1; d >= 0; d--) arr[d] = Math.min(arr[d], arr[d + 1]);
    return arr;
  });

  const violated = new Uint8Array(active.length);
  let violatedTotal = 0;
  let assignment = base;
  for (let ai = 0; ai < active.length; ai++) {
    if (isViolated(active[ai], assignment)) {
      violated[ai] = 1;
      violatedTotal++;
    }
  }

  let best = Infinity;
  let bestFlips: boolean[] | null = null;
  const flips = new Array<boolean>(k).fill(false);

  const applyFlip = (p: number): void => {
    assignment ^= 1 << flipVarIndex[p];
    for (const ai of rulesByFlip[p]) {
      const now = isViolated(active[ai], assignment);
      if (now && !violated[ai]) {
        violated[ai] = 1;
        violatedTotal++;
      } else if (!now && violated[ai]) {
        violated[ai] = 0;
        violatedTotal--;
      }
    }
  };

  // 贪心上界：反复修复第一条被违反的规则，选其中最便宜的未翻转变量。
  if (!feasibilityOnly) {
    let greedyAssign = base;
    let greedyCost = 0;
    const greedyFlips = new Array<boolean>(k).fill(false);
    for (let step = 0; step <= k; step++) {
      const bad = active.find((r) => isViolated(r, greedyAssign));
      if (!bad) {
        best = greedyCost;
        bestFlips = greedyFlips.slice();
        break;
      }
      let pick = -1;
      for (const p of bad.flips)
        if (!greedyFlips[p] && (pick < 0 || flipCost[p] < flipCost[pick])) pick = p;
      if (pick < 0) break;
      greedyFlips[pick] = true;
      greedyCost += flipCost[pick];
      greedyAssign ^= 1 << flipVarIndex[pick];
    }
  }

  const dfs = (d: number, cost: number): boolean => {
    if (violatedTotal === 0) {
      // 成本非负，继续翻转不会更优。
      if (cost < best) {
        best = cost;
        bestFlips = flips.slice();
      }
      return feasibilityOnly;
    }
    if (d === k) return false;

    // 下界：每条被违反的规则至少还需 suffixMin[ai][d]，取最大值。
    let lowerBound = 0;
    for (let ai = 0; ai < active.length; ai++) {
      if (!violated[ai]) continue;
      const need = suffixMin[ai][d];
      if (need === Infinity) return false; // 该规则无法再修复，死路。
      if (need > lowerBound) lowerBound = need;
    }
    if (cost + lowerBound >= best) return false;

    // 优先尝试让违反数更小的分支，尽早找到好解。
    const tryOrder: boolean[] = [];
    {
      flips[d] = true;
      applyFlip(d);
      const afterFlip = violatedTotal;
      flips[d] = false;
      applyFlip(d);
      tryOrder.push(afterFlip < violatedTotal, afterFlip >= violatedTotal);
    }

    for (const doFlip of tryOrder) {
      if (doFlip) {
        flips[d] = true;
        applyFlip(d);
        const abort = dfs(d + 1, cost + flipCost[d]);
        flips[d] = false;
        applyFlip(d);
        if (abort) return true;
      } else if (dfs(d + 1, cost)) {
        return true;
      }
    }
    return false;
  };

  dfs(0, 0);
  return bestFlips === null ? null : { cost: best, flips: bestFlips };
}

function buildVerification(compiled: Compiled, assignment: number): RuleCheck[] {
  return compiled.rules.map((rule) => {
    const premise = rule.premise.map((lit) => {
      const idx = compiled.varNames.indexOf(lit.var);
      const actual = ((assignment >>> idx) & 1) === 1;
      return { var: lit.var, expected: lit.value, actual, holds: actual === lit.value };
    });
    const premiseHolds = premise.every((p) => p.holds);
    const conclActual = ((assignment >>> rule.conclBit) & 1) === 1;
    const check: RuleCheck = {
      rule: rule.index,
      premise,
      premiseHolds,
      conclusion: { var: rule.conclVar, expected: rule.conclValue, actual: conclActual },
      satisfied: !premiseHolds || conclActual === rule.conclValue,
    };
    if (rule.id !== undefined) check.id = rule.id;
    return check;
  });
}

function assignmentRecord(compiled: Compiled, assignment: number): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  compiled.varNames.forEach((name, i) => {
    out[name] = ((assignment >>> i) & 1) === 1;
  });
  return out;
}

/** 删除式极小化：返回一个极小的不可满足规则子集（冲突核心）。 */
function minimizeConflictCore(compiled: Compiled, allIdx: number[]): number[] {
  let core = allIdx.slice();
  for (const r of allIdx) {
    if (!core.includes(r)) continue;
    const trial = core.filter((x) => x !== r);
    if (trial.length === 0) continue;
    if (searchMinCost(compiled, trial, true) === null) core = trial;
  }
  return core;
}

export function solve(inst: Instance): SolveResult {
  const compiled = compile(inst);
  const allIdx = compiled.rules.map((_, i) => i);
  const found = searchMinCost(compiled, allIdx, false);

  if (found) {
    let assignment = compiled.base;
    const changes: Change[] = [];
    found.flips.forEach((flipped, p) => {
      if (!flipped) return;
      const idx = compiled.flipVarIndex[p];
      const from = ((compiled.base >>> idx) & 1) === 1;
      assignment ^= 1 << idx;
      changes.push({
        var: compiled.varNames[idx],
        from,
        to: !from,
        cost: compiled.flipCost[p],
      });
    });
    return {
      status: changes.length === 0 ? "satisfied" : "repaired",
      totalCost: found.cost,
      changes,
      assignment: assignmentRecord(compiled, assignment),
      verification: buildVerification(compiled, assignment),
    };
  }

  const coreIdx = minimizeConflictCore(compiled, allIdx);
  const coreSet = new Set(coreIdx);
  const coreCompiled: Compiled = { ...compiled, rules: compiled.rules.filter((r) => coreSet.has(r.index)) };
  const mutableVars = new Set(
    compiled.flipVarIndex.map((idx) => compiled.varNames[idx]),
  );
  const immutableInCore = new Set<string>();
  for (const rule of coreCompiled.rules) {
    for (const lit of rule.premise) if (!mutableVars.has(lit.var)) immutableInCore.add(lit.var);
    if (!mutableVars.has(rule.conclVar)) immutableInCore.add(rule.conclVar);
  }
  return {
    status: "unsatisfiable",
    reason:
      "仅翻转可修改变量无法同时满足所有规则；以下为不可修改变量导致的极小冲突核心",
    conflictCore: buildVerification(coreCompiled, compiled.base),
    immutableVariablesInCore: [...immutableInCore].sort(),
  };
}
