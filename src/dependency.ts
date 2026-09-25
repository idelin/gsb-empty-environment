import type {
  AliasConflict,
  CycleReport,
  IdempotencyReport,
  OrderViolation,
  Rule,
  RuleDependency,
  RuleSet,
  RuleSetAnalysis,
  TriggerEdge,
} from "./types.ts";
import { parsePointer, patternsOverlap } from "./pointer.ts";
import {
  WHITESPACE_PROBES,
  applyDate,
  applyEnum,
  applyWhitespace,
  dateDomain,
  enumDomain,
} from "./rules.ts";

/** 规则声明的有限输入域（空白/日期/枚举都在“支持的有限输入域”内搜索）。 */
export function declaredDomain(rule: Rule): string[] {
  switch (rule.type) {
    case "whitespace":
      return [...WHITESPACE_PROBES];
    case "date":
      return dateDomain(rule);
    case "enum":
      return enumDomain(rule);
  }
}

function applyRule(rule: Rule, value: string): { ok: boolean; value?: string } {
  switch (rule.type) {
    case "whitespace":
      return applyWhitespace(rule, value);
    case "date":
      return applyDate(rule, value);
    case "enum":
      return applyEnum(rule, value);
  }
}

/** 规则成功后的全部输出（有限域上的像集）。 */
function outputImage(rule: Rule): string[] {
  const out = new Set<string>();
  for (const v of declaredDomain(rule)) {
    const r = applyRule(rule, v);
    if (r.ok && r.value !== undefined) out.add(r.value);
  }
  return [...out];
}

/**
 * 触发边 A -> B：存在值 v，A 成功产出 w，而 B 作用在 w 上会“再次生效”
 * （成功且改变值）。仅当两条规则路径模式可能相交时才算——
 * 读写路径依赖必须同字段才存在。
 */
export function computeTriggerEdges(rules: Rule[]): TriggerEdge[] {
  const edges: TriggerEdge[] = [];
  for (let i = 0; i < rules.length; i++) {
    for (let j = 0; j < rules.length; j++) {
      const a = rules[i];
      const b = rules[j];
      if (!patternsOverlap(a.path, b.path)) continue;
      const outputs = outputImage(a);
      for (const w of outputs) {
        const r = applyRule(b, w);
        if (r.ok && r.value !== undefined && r.value !== w) {
          edges.push({
            from: a.id,
            to: b.id,
            reason: `${a.type} 规则 "${a.id}" 的输出可能被 ${b.type} 规则 "${b.id}" 再次改写`,
            witness: w,
          });
          break;
        }
      }
    }
  }
  return edges;
}

function findCycles(rules: Rule[], edges: TriggerEdge[]): CycleReport[] {
  const adj = new Map<string, string[]>();
  for (const e of edges) {
    if (!adj.has(e.from)) adj.set(e.from, []);
    adj.get(e.from)!.push(e.to);
  }
  const cycles: CycleReport[] = [];
  const reported = new Set<string>();
  const color = new Map<string, 0 | 1 | 2>(); // 0 未访问 1 在栈上 2 已完成
  const stack: string[] = [];

  const dfs = (node: string): void => {
    color.set(node, 1);
    stack.push(node);
    for (const next of adj.get(node) ?? []) {
      const c = color.get(next) ?? 0;
      if (c === 0) {
        dfs(next);
      } else if (c === 1) {
        const start = stack.indexOf(next);
        const cyc = [...stack.slice(start), next];
        const key = [...cyc].sort().join("->");
        if (!reported.has(key)) {
          reported.add(key);
          cycles.push({
            rules: cyc,
            message: `检测到相互触发环: ${cyc.join(" -> ")}（同一字段上规则反复改写，单遍管道无法收敛）`,
          });
        }
      }
    }
    stack.pop();
    color.set(node, 2);
  };

  for (const r of rules) {
    if ((color.get(r.id) ?? 0) === 0) dfs(r.id);
  }
  return cycles;
}

/** 别名冲突：路径相交的枚举规则声明了同一别名却映射到不同规范值。 */
export function computeAliasConflicts(rules: Rule[]): AliasConflict[] {
  const enums = rules.filter((r): r is Extract<Rule, { type: "enum" }> => r.type === "enum");
  const conflicts: AliasConflict[] = [];
  const reported = new Set<string>();
  for (let i = 0; i < enums.length; i++) {
    for (let j = i + 1; j < enums.length; j++) {
      const a = enums[i];
      const b = enums[j];
      if (!patternsOverlap(a.path, b.path)) continue;
      const aliases = new Set([...Object.keys(a.map), ...Object.keys(b.map)]);
      for (const alias of aliases) {
        const va = a.map[alias];
        const vb = b.map[alias];
        if (va !== undefined && vb !== undefined && va !== vb) {
          const key = `${a.id}|${b.id}|${alias}`;
          if (reported.has(key)) continue;
          reported.add(key);
          conflicts.push({
            paths: [a.path, b.path],
            rules: [a.id, b.id],
            alias,
            canonical: [va, vb],
            message: `别名 "${alias}" 在规则 ${a.id} 与 ${b.id} 中分别映射为 "${va}" / "${vb}"`,
          });
        }
      }
    }
  }
  return conflicts;
}

function validateShape(rs: RuleSet): string[] {
  const errors: string[] = [];
  if (!rs || typeof rs !== "object" || !Array.isArray(rs.rules)) {
    errors.push("规则集必须包含 rules 数组");
    return errors;
  }
  rs.rules.forEach((r, i) => {
    if (!r || typeof r !== "object" || typeof r.id !== "string" || !r.id) {
      errors.push(`第 ${i} 条规则缺少字符串 id`);
      return;
    }
    if (typeof r.path !== "string" || (!r.path.startsWith("/") && r.path !== "")) {
      errors.push(`规则 ${r.id} 的 path 必须是 JSON Pointer（以 / 开头）`);
    }
    if (r.type === "whitespace" && r.mode !== "trim" && r.mode !== "collapse") {
      errors.push(`规则 ${r.id} 的 whitespace.mode 必须是 trim 或 collapse`);
    }
    if (r.type === "date" && (!Array.isArray(r.formats) || r.formats.length === 0)) {
      errors.push(`规则 ${r.id} 的 date.formats 不能为空`);
    }
    if (r.type === "enum" && (!r.map || typeof r.map !== "object")) {
      errors.push(`规则 ${r.id} 的 enum.map 必须是对象`);
    }
    if (!["whitespace", "date", "enum"].includes(r.type)) {
      errors.push(`规则 ${r.id} 的 type 非法: ${(r as { type?: unknown }).type}`);
    }
  });
  return errors;
}

/**
 * 规则组合的静态分析（规则顺序显式：数组下标即管道顺序）。
 * 在规范化或发布之前提前发现：重复 id、别名冲突、相互触发环、
 * 以及“后写触发先读”的顺序违例。
 */
function validPath(r: Rule): boolean {
  if (typeof r.path !== "string") return false;
  if (r.path === "") return false; // 规则不允许作用于根
  if (!r.path.startsWith("/")) return false;
  try {
    parsePointer(r.path);
    return true;
  } catch {
    return false;
  }
}

export function analyzeRuleset(rs: RuleSet): RuleSetAnalysis {
  const shapeErrors = validateShape(rs);
  const allRules = Array.isArray(rs.rules) ? rs.rules : [];
  const rules = allRules.filter((r) => r && r.id && validPath(r));
  if (allRules.length !== rules.length) {
    for (const r of allRules) {
      if (!rules.includes(r)) shapeErrors.push(`规则 ${r?.id ?? "<无 id>"} 的 path 非法或为空: ${String((r as { path?: unknown })?.path)}`);
    }
  }

  const duplicateIds: string[] = [];
  const seenIds = new Set<string>();
  for (const r of rules) {
    if (!r?.id) continue;
    if (seenIds.has(r.id) && !duplicateIds.includes(r.id)) duplicateIds.push(r.id);
    seenIds.add(r.id);
  }

  const overlapPairs: Array<[string, string]> = [];
  for (let i = 0; i < rules.length; i++) {
    for (let j = i + 1; j < rules.length; j++) {
      if (patternsOverlap(rules[i].path, rules[j].path)) {
        overlapPairs.push([rules[i].id, rules[j].id]);
      }
    }
  }

  const aliasConflicts = computeAliasConflicts(rules);
  const triggerEdges = computeTriggerEdges(rules);
  const cycles = findCycles(rules, triggerEdges);

  const orderIndex = new Map(rules.map((r, i) => [r.id, i]));
  const orderViolations: OrderViolation[] = [];
  for (const edge of triggerEdges) {
    const ai = orderIndex.get(edge.from);
    const bi = orderIndex.get(edge.to);
    if (ai !== undefined && bi !== undefined && ai >= bi) {
      orderViolations.push({
        edge,
        message: `顺序违例：规则 "${edge.from}"(#${ai}) 的输出需要先被 "${edge.to}"(#${bi}) 处理，但后者排在前面`,
      });
    }
  }

  const dependencies: RuleDependency[] = rules.map((r, i) => ({
    ruleId: r.id,
    ruleType: r.type,
    order: i,
    path: r.path,
    reads: [r.path],
    writes: [r.path],
    declaredDomain: declaredDomain(r),
  }));

  const errors = [
    ...shapeErrors,
    ...duplicateIds.map((id) => `规则 id 重复: ${id}`),
    ...aliasConflicts.map((c) => c.message),
    ...cycles.map((c) => c.message),
    ...orderViolations.map((v) => v.message),
  ];

  return {
    ok: errors.length === 0,
    order: rules.map((r) => r.id),
    dependencies,
    overlapPairs,
    aliasConflicts,
    triggerEdges,
    cycles,
    orderViolations,
    duplicateIds,
    errors,
  };
}

/* -------------------------- 幂等反例搜索 -------------------------- */

interface IdempotencyOptions {
  budget?: number;
}

/**
 * 在支持的有限输入域上搜索幂等性反例。
 * 把规则按“代表字段”分组（同组模式在同一具体字段上可能同时命中），
 * 在组内串联的有限域并集上逐个取值，模拟一次完整的有序管道，
 * 再对结果跑第二遍并比较。
 *
 * - 找到第二次仍改变的值 => refuted（附反例）
 * - 预算内穷尽且无反例 => proved
 * - 预算耗尽仍未穷尽   => undecided（预算未决，绝不标 proved）
 */
export function searchIdempotency(
  rs: RuleSet,
  options: IdempotencyOptions = {},
): IdempotencyReport {
  const budget = options.budget ?? 20000;
  const rules = rs.rules;

  // 代表模式分组：把 "*" 替换为固定代表段 "0"。
  const groups = new Map<string, Rule[]>();
  for (const r of rules) {
    const rep = r.path.replace(/[^/]+/g, (seg) => (seg === "*" ? "0" : seg));
    if (!groups.has(rep)) groups.set(rep, []);
    groups.get(rep)!.push(r);
  }

  let domainSize = 0;
  const counterexamples = [];
  let evaluated = 0;

  for (const groupRules of groups.values()) {
    const domain = new Set<string>();
    for (const r of groupRules) for (const v of declaredDomain(r)) domain.add(v);
    // 加入各规则的输出像集（规范值/日期输出等是第二遍的关键候选）。
    for (const r of groupRules) for (const v of outputImage(r)) domain.add(v);
    const values = [...domain];
    domainSize += values.length;

    const runOnce = (start: string): string => {
      let current = start;
      for (const r of groupRules) {
        const res = applyRule(r, current);
        if (res.ok && res.value !== undefined) current = res.value;
        else return current; // 出错保留当前值，后续规则不再作用
      }
      return current;
    };

    for (const v of values) {
      if (evaluated >= budget) {
        return {
          status: "undecided",
          domainSize,
          evaluated,
          budget,
          counterexamples,
          note: `搜索预算 ${budget} 已耗尽，有限域共 ${domainSize} 个候选，尚有未验证取值；不得视为已证明`,
        };
      }
      evaluated += 1;
      const once = runOnce(v);
      const twice = runOnce(once);
      if (once !== twice) {
        counterexamples.push({ input: v, once, twice });
      }
    }
  }

  if (counterexamples.length > 0) {
    return {
      status: "refuted",
      domainSize,
      evaluated,
      budget,
      counterexamples,
      note: `在有限输入域上发现 ${counterexamples.length} 个幂等反例`,
    };
  }
  return {
    status: "proved",
    domainSize,
    evaluated,
    budget,
    counterexamples,
    note: `有限输入域（${domainSize} 个候选）已穷尽，两遍结果一致；证明范围仅限该声明域`,
  };
}
