/**
 * 顺序规则覆盖分析器。
 *
 * 语义：规则按下标顺序匹配，首个命中的规则决定动作。
 * 分析完全基于盒代数精确计算（非抽样）：
 *  - empty-rule            规则自身条件区域为空（空区间/空枚举）
 *  - unreachable           规则区域被更靠前的规则完全遮蔽
 *  - partially-shadowed    规则区域被部分遮蔽，给出两侧最小见证点
 *  - uncovered             全部规则都未覆盖的输入，给出最小反例与精确计数
 *
 * 增删规则后做增量式后缀重算：缓存每条规则之前的已覆盖区域，
 * 追加规则 O(1) 扩展，删除/插入仅从该下标起重算，全程符号运算。
 */

import type { Domain, Input, Rule } from './model.ts';
import type { Box } from './region.ts';
import {
  boxFromRule,
  boxSize,
  fullBox,
  intersect,
  isEmpty,
  minPointOfBoxes,
  subtractMany,
} from './region.ts';

export type RuleStatus = 'empty' | 'unreachable' | 'partially-shadowed' | 'live';

export type RuleReport = {
  index: number;
  id: string;
  action: string;
  status: RuleStatus;
  /** 遮蔽本规则的更靠前规则下标 */
  shadowedBy: number[];
  /** 本规则独有（首次命中）的最小见证点 */
  liveExample?: Input;
  /** 本规则想匹配、但被更早规则抢走的最小见证点 */
  shadowedExample?: Input;
  /** 本规则首次命中的精确区域（盒并集，符号表示） */
  liveBoxes: Box[];
  /** 被更早规则遮蔽的精确区域 */
  shadowedBoxes: Box[];
};

export type Analysis = {
  domain: Domain;
  rules: RuleReport[];
  /** 未被任何规则覆盖的精确区域 */
  uncoveredBoxes: Box[];
  /** 未覆盖输入的最小反例 */
  uncoveredExample?: Input;
  /** 未覆盖输入的精确个数 */
  uncoveredCount: number;
};

export class Analyzer {
  private readonly domain: Domain;
  private rules: Rule[] = [];
  /** newly[i]：规则 i 首次命中的互不相交盒列表 */
  private newly: Box[][] = [];
  /** coveredBefore[i]：规则 i 之前的已覆盖盒并集 */
  private coveredBefore: Box[][] = [[]];

  constructor(domain: Domain) {
    this.domain = domain;
  }

  get ruleCount(): number {
    return this.rules.length;
  }

  getRule(index: number): Rule {
    return this.rules[index];
  }

  /** 追加或插入规则，返回最新分析 */
  addRule(rule: Rule, index?: number): Analysis {
    const at = index ?? this.rules.length;
    if (at < 0 || at > this.rules.length) throw new RangeError(`bad index ${at}`);
    this.rules.splice(at, 0, rule);
    this.recomputeFrom(at);
    return this.analyze();
  }

  /** 删除规则，返回最新分析 */
  removeRule(index: number): Analysis {
    if (index < 0 || index >= this.rules.length) throw new RangeError(`bad index ${index}`);
    this.rules.splice(index, 1);
    this.recomputeFrom(index);
    return this.analyze();
  }

  /** 从下标 k 起重算后缀；复用 coveredBefore[k] 之前的状态 */
  private recomputeFrom(k: number): void {
    let covered: Box[] = k < this.coveredBefore.length ? this.coveredBefore[k] : [];
    this.newly.length = k;
    this.coveredBefore.length = k;
    for (let i = k; i < this.rules.length; i++) {
      this.coveredBefore[i] = covered;
      const box = boxFromRule(this.domain, this.rules[i]);
      const fresh = isEmpty(box) ? [] : subtractMany(box, covered);
      this.newly[i] = fresh;
      covered = [...covered, ...fresh];
    }
    this.coveredBefore[this.rules.length] = covered;
  }

  /** 生成完整分析报告（纯函数，不修改状态） */
  analyze(): Analysis {
    const reports: RuleReport[] = this.rules.map((rule, i) => {
      const box = boxFromRule(this.domain, rule);
      const liveBoxes = this.newly[i];
      if (isEmpty(box)) {
        return { index: i, id: rule.id, action: rule.action, status: 'empty', shadowedBy: [], liveBoxes, shadowedBoxes: [] };
      }
      // 被遮蔽区域 = 规则区域 ∩ 各更早规则的首次命中区域（彼此不相交）
      const shadowedBoxes: Box[] = [];
      const shadowedBy: number[] = [];
      for (let j = 0; j < i; j++) {
        for (const earlier of this.newly[j]) {
          const hit = intersect(box, earlier);
          if (hit) {
            shadowedBoxes.push(hit);
            if (!shadowedBy.includes(j)) shadowedBy.push(j);
          }
        }
      }
      const liveExample = minPointOfBoxes(liveBoxes, this.domain);
      const shadowedExample = minPointOfBoxes(shadowedBoxes, this.domain);
      const status: RuleStatus =
        liveBoxes.length === 0 ? 'unreachable' : shadowedBoxes.length > 0 ? 'partially-shadowed' : 'live';
      return { index: i, id: rule.id, action: rule.action, status, shadowedBy, liveExample, shadowedExample, liveBoxes, shadowedBoxes };
    });

    const covered = this.coveredBefore[this.rules.length];
    const uncoveredBoxes = subtractMany(fullBox(this.domain), covered);
    return {
      domain: this.domain,
      rules: reports,
      uncoveredBoxes,
      uncoveredExample: minPointOfBoxes(uncoveredBoxes, this.domain),
      uncoveredCount: uncoveredBoxes.reduce((acc, b) => acc + boxSize(b), 0),
    };
  }
}

export function formatInput(domain: Domain, input: Input): string {
  return `{ ${domain.attrs.map((a) => `${a.name}=${String(input[a.name])}`).join(', ')} }`;
}

export function formatReport(analysis: Analysis): string {
  const lines: string[] = [];
  for (const r of analysis.rules) {
    const head = `#${r.index} ${r.id} -> ${r.action}: ${r.status}`;
    const details: string[] = [];
    if (r.shadowedBy.length > 0) details.push(`shadowedBy=[${r.shadowedBy.join(',')}]`);
    if (r.liveExample) details.push(`live=${formatInput(analysis.domain, r.liveExample)}`);
    if (r.shadowedExample) details.push(`shadowed=${formatInput(analysis.domain, r.shadowedExample)}`);
    lines.push(details.length > 0 ? `${head}  ${details.join(' ')}` : head);
  }
  if (analysis.uncoveredCount > 0) {
    lines.push(
      `uncovered: ${analysis.uncoveredCount} input(s), minimal=${formatInput(analysis.domain, analysis.uncoveredExample!)}`,
    );
  } else {
    lines.push('uncovered: none (full coverage)');
  }
  return lines.join('\n');
}
