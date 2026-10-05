// 小域穷举核对器。
//
// 与分析器相互独立地实现匹配语义：逐点枚举整个（小）有限输入空间，
// 用“首个命中规则”的暴力结果交叉核对符号分析报告。
// 仅适用于小域（测试 / 调试场景），大域请使用符号分析器本身。

import type { Condition, Input, Rule, Schema } from './model.ts';
import type { AnalysisReport, RuleStatus } from './analyzer.ts';
import type { SchemaIndex } from './box.ts';
import {
  compareInputs,
  containsPoint,
  formatInput,
  indexSchema,
  sumVolumes,
} from './box.ts';

/** 单条件匹配判定（独立实现，不复用盒运算）。 */
export function matchesCondition(cond: Condition, input: Input): boolean {
  const v = input[cond.attr];
  if (cond.kind === 'int') {
    return typeof v === 'number' && Number.isInteger(v) && v >= cond.lo && v < cond.hi;
  }
  return typeof v === 'string' && cond.in.includes(v);
}

export function matchesRule(rule: Rule, input: Input): boolean {
  return rule.conditions.every((c) => matchesCondition(c, input));
}

/** 首个命中规则的下标；无命中返回 -1。 */
export function firstMatch(rules: Rule[], input: Input): number {
  for (let i = 0; i < rules.length; i++) {
    if (matchesRule(rules[i], input)) return i;
  }
  return -1;
}

/** 按字典序枚举整个有限输入空间（要求域足够小）。 */
export function* enumerateInputs(schema: Schema): Generator<Input> {
  const attrs = schema.attrs;
  const input: Input = {};
  function* rec(pos: number): Generator<Input> {
    if (pos === attrs.length) {
      yield { ...input };
      return;
    }
    const attr = attrs[pos];
    if (attr.kind === 'int') {
      for (let v = attr.min; v <= attr.max; v++) {
        input[attr.name] = v;
        yield* rec(pos + 1);
      }
    } else {
      for (const v of attr.values) {
        input[attr.name] = v;
        yield* rec(pos + 1);
      }
    }
  }
  yield* rec(0);
}

export interface BruteForceResult {
  /** 每条规则：暴力意义下命中它的输入个数与最小输入。 */
  hitCount: number[];
  minHit: (Input | null)[];
  /** 每条规则：满足其自身条件（不考虑顺序）的输入个数。 */
  condMatchCount: number[];
  uncoveredCount: number;
  minUncovered: Input | null;
}

export function bruteForce(schema: Schema, rules: Rule[]): BruteForceResult {
  const hitCount = rules.map(() => 0);
  const minHit: (Input | null)[] = rules.map(() => null);
  const condMatchCount = rules.map(() => 0);
  let uncoveredCount = 0;
  let minUncovered: Input | null = null;

  for (const input of enumerateInputs(schema)) {
    for (let i = 0; i < rules.length; i++) {
      if (matchesRule(rules[i], input)) condMatchCount[i]++;
    }
    const idx = firstMatch(rules, input);
    if (idx === -1) {
      uncoveredCount++;
      if (minUncovered === null) minUncovered = input; // 字典序枚举，首个即最小
    } else {
      hitCount[idx]++;
      if (minHit[idx] === null) minHit[idx] = input;
    }
  }
  return { hitCount, minHit, condMatchCount, uncoveredCount, minUncovered };
}

/**
 * 交叉核对符号分析报告与暴力枚举结果。
 * 返回问题列表；空列表表示两者完全一致。
 */
export function crossCheck(schema: Schema, rules: Rule[], report: AnalysisReport): string[] {
  const idx: SchemaIndex = indexSchema(schema);
  const problems: string[] = [];
  const brute = bruteForce(schema, rules);

  if (report.rules.length !== rules.length) {
    problems.push(`report has ${report.rules.length} rule entries, expected ${rules.length}`);
    return problems;
  }

  // 逐输入核对归属：命中规则 i 的输入必须落在 reachableBoxes[i] 内，
  // 未覆盖输入必须落在 uncoveredBoxes 内，且两类盒互不相混。
  for (const input of enumerateInputs(schema)) {
    const hit = firstMatch(rules, input);
    const desc = formatInput(idx, input);
    if (hit === -1) {
      if (!report.uncoveredBoxes.some((b) => containsPoint(idx, b, input))) {
        problems.push(`uncovered input ${desc} is not inside any symbolic uncovered box`);
      }
    } else {
      if (!report.rules[hit].reachableBoxes.some((b) => containsPoint(idx, b, input))) {
        problems.push(`input ${desc} hits rule ${rules[hit].id} but is outside its reachable boxes`);
      }
      if (report.uncoveredBoxes.some((b) => containsPoint(idx, b, input))) {
        problems.push(`covered input ${desc} is inside a symbolic uncovered box`);
      }
    }
  }

  // 逐规则核对状态、最小反例、可达体积。
  for (let i = 0; i < rules.length; i++) {
    const ra = report.rules[i];
    const id = rules[i].id;
    const bruteEmpty = brute.condMatchCount[i] === 0;
    const bruteReachable = brute.hitCount[i] > 0;
    const brutePartial = bruteReachable && brute.condMatchCount[i] > brute.hitCount[i];

    let expected: RuleStatus;
    if (bruteEmpty) expected = 'empty';
    else if (!bruteReachable) expected = 'unreachable';
    else if (brutePartial) expected = 'partially-shadowed';
    else expected = 'reachable';

    if (ra.status !== expected) {
      problems.push(`rule ${id}: status ${ra.status}, brute-force says ${expected}`);
    }

    const expectedWitness = bruteReachable ? brute.minHit[i] : null;
    if (expectedWitness === null) {
      if (ra.witness !== null) {
        problems.push(`rule ${id}: symbolic witness ${formatInput(idx, ra.witness)} but rule is not reachable`);
      }
    } else {
      if (ra.witness === null) {
        problems.push(`rule ${id}: missing witness, brute-force minimal hit is ${formatInput(idx, expectedWitness)}`);
      } else if (compareInputs(idx, ra.witness, expectedWitness) !== 0) {
        problems.push(
          `rule ${id}: witness ${formatInput(idx, ra.witness)} != brute-force minimal ${formatInput(idx, expectedWitness)}`,
        );
      }
    }

    const reachVol = sumVolumes(idx, ra.reachableBoxes);
    if (reachVol !== brute.hitCount[i]) {
      problems.push(`rule ${id}: reachable volume ${reachVol} != brute-force hit count ${brute.hitCount[i]}`);
    }

    const totalVol = reachVol + sumVolumes(idx, ra.shadowedBoxes);
    if (totalVol !== brute.condMatchCount[i]) {
      problems.push(
        `rule ${id}: reachable+shadowed volume ${totalVol} != brute-force condition-match count ${brute.condMatchCount[i]}`,
      );
    }
  }

  // 未覆盖空间：体积与最小反例。
  const uncoveredVol = sumVolumes(idx, report.uncoveredBoxes);
  if (uncoveredVol !== brute.uncoveredCount) {
    problems.push(`uncovered volume ${uncoveredVol} != brute-force uncovered count ${brute.uncoveredCount}`);
  }
  if (brute.minUncovered === null) {
    if (report.uncoveredWitness !== null) {
      problems.push('symbolic uncovered witness exists but brute-force found full coverage');
    }
  } else {
    if (report.uncoveredWitness === null) {
      problems.push(`missing uncovered witness, brute-force minimal is ${formatInput(idx, brute.minUncovered)}`);
    } else if (compareInputs(idx, report.uncoveredWitness, brute.minUncovered) !== 0) {
      problems.push(
        `uncovered witness ${formatInput(idx, report.uncoveredWitness)} != brute-force minimal ${formatInput(idx, brute.minUncovered)}`,
      );
    }
  }
  if (report.fullyCovered !== (brute.uncoveredCount === 0)) {
    problems.push(`fullyCovered=${report.fullyCovered} disagrees with brute-force`);
  }

  return problems;
}
