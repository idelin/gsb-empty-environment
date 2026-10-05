/**
 * 小域穷举核对器：在有限小域上枚举全部输入，暴力计算首个命中规则，
 * 与符号分析结果逐项对拍，验证其精确性（而非抽样推断）。
 */

import type { Domain, Input, Rule } from './model.ts';
import type { Analysis } from './analyzer.ts';
import { boxSize, contains } from './region.ts';

export function enumerateInputs(domain: Domain): Input[] {
  let acc: Input[] = [{}];
  for (const attr of domain.attrs) {
    const values: (number | string)[] =
      attr.kind === 'int'
        ? Array.from({ length: attr.max - attr.min + 1 }, (_, i) => attr.min + i)
        : [...attr.values];
    const next: Input[] = [];
    for (const partial of acc) for (const v of values) next.push({ ...partial, [attr.name]: v });
    acc = next;
  }
  return acc;
}

export function ruleMatches(rule: Rule, domain: Domain, input: Input): boolean {
  return rule.conds.every((cond) => {
    const v = input[cond.attr];
    if (cond.kind === 'int') {
      if (typeof v !== 'number') return false;
      const lo = cond.loOpen ? cond.lo + 1 : cond.lo;
      const hi = cond.hiOpen ? cond.hi - 1 : cond.hi;
      return v >= lo && v <= hi;
    }
    return typeof v === 'string' && cond.values.includes(v);
  });
}

/** 首个命中的规则下标；无命中返回 -1 */
export function firstMatch(rules: readonly Rule[], domain: Domain, input: Input): number {
  for (let i = 0; i < rules.length; i++) {
    if (ruleMatches(rules[i], domain, input)) return i;
  }
  return -1;
}

export type BruteForce = {
  /** firstHit[i]：首次命中规则 i 的输入列表 */
  firstHit: Input[][];
  /** 未被任何规则命中的输入列表 */
  uncovered: Input[];
};

export function bruteForce(domain: Domain, rules: readonly Rule[]): BruteForce {
  const firstHit: Input[][] = rules.map(() => []);
  const uncovered: Input[] = [];
  for (const input of enumerateInputs(domain)) {
    const hit = firstMatch(rules, domain, input);
    if (hit < 0) uncovered.push(input);
    else firstHit[hit].push(input);
  }
  return { firstHit, uncovered };
}

/**
 * 对拍符号分析与暴力枚举，返回不一致项列表（空列表 = 完全一致）。
 * 仅适用于小域（枚举规模有限）。
 */
export function crossCheck(domain: Domain, rules: readonly Rule[], analysis: Analysis): string[] {
  const problems: string[] = [];
  const brute = bruteForce(domain, rules);

  analysis.rules.forEach((report, i) => {
    const hits = brute.firstHit[i];
    // 1) 首次命中集合必须精确等于 liveBoxes 覆盖的集合
    const liveSet = new Set(hits.map((h) => JSON.stringify(h)));
    for (const input of enumerateInputs(domain)) {
      const inBoxes = report.liveBoxes.some((b) => contains(b, domain, input));
      const inBrute = liveSet.has(JSON.stringify(input));
      if (inBoxes !== inBrute) {
        problems.push(`rule ${i} (${report.id}): liveBoxes mismatch at ${JSON.stringify(input)}`);
      }
    }
    // 2) 状态与暴力计数一致
    const bruteLive = hits.length;
    if (report.status === 'empty') {
      if (rules[i].conds.length >= 0 && bruteLive !== 0) problems.push(`rule ${i}: empty but has hits`);
    } else if (report.status === 'unreachable' && bruteLive !== 0) {
      problems.push(`rule ${i}: status=unreachable but brute live=${bruteLive}`);
    } else if ((report.status === 'live' || report.status === 'partially-shadowed') && bruteLive === 0) {
      problems.push(`rule ${i}: status=${report.status} but brute live=0`);
    }
    // 3) 见证点合法性
    if (report.liveExample && firstMatch(rules, domain, report.liveExample) !== i) {
      problems.push(`rule ${i}: liveExample not first-matched by this rule`);
    }
    if (report.shadowedExample) {
      const hit = firstMatch(rules, domain, report.shadowedExample);
      if (!(hit >= 0 && hit < i) || !ruleMatches(rules[i], domain, report.shadowedExample)) {
        problems.push(`rule ${i}: shadowedExample invalid`);
      }
    }
    // 4) liveBoxes 测度 = 暴力计数
    const measure = report.liveBoxes.reduce((acc, b) => acc + boxSize(b), 0);
    if (measure !== bruteLive) {
      problems.push(`rule ${i}: live measure ${measure} != brute ${bruteLive}`);
    }
  });

  // 5) 未覆盖集合与计数
  const uncoveredSet = new Set(brute.uncovered.map((u) => JSON.stringify(u)));
  for (const input of enumerateInputs(domain)) {
    const inBoxes = analysis.uncoveredBoxes.some((b) => contains(b, domain, input));
    if (inBoxes !== uncoveredSet.has(JSON.stringify(input))) {
      problems.push(`uncoveredBoxes mismatch at ${JSON.stringify(input)}`);
    }
  }
  if (analysis.uncoveredCount !== brute.uncovered.length) {
    problems.push(`uncoveredCount ${analysis.uncoveredCount} != brute ${brute.uncovered.length}`);
  }
  if (brute.uncovered.length > 0) {
    if (!analysis.uncoveredExample || firstMatch(rules, domain, analysis.uncoveredExample) !== -1) {
      problems.push('uncoveredExample invalid');
    }
  } else if (analysis.uncoveredExample !== undefined) {
    problems.push('uncoveredExample present but brute found none');
  }
  return problems;
}
