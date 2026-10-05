// 规则覆盖分析器。
//
// 语义：规则按顺序匹配，首个命中的规则决定动作（first-match-wins）。
// 分析基于盒的精确集合运算（交 / 差），对整个有限输入空间成立，
// 不做任何逐点抽样推断。增删规则后调用 analyze() 重新计算即可，
// 结果与一次性全量分析完全一致。

import type { Input, Rule, Schema } from './model.ts';
import type { Box, SchemaIndex } from './box.ts';
import {
  boxesOverlap,
  fullBox,
  indexSchema,
  isEmptyBox,
  minPointOfBoxes,
  ruleBox,
  subtractMany,
} from './box.ts';

export type RuleStatus =
  | 'reachable' // 规则空间完全不被前序规则遮蔽
  | 'partially-shadowed' // 部分被前序规则遮蔽，仍有输入能命中本规则
  | 'unreachable' // 非空但完全被前序规则遮蔽，永远不可达
  | 'empty'; // 条件自身矛盾（互斥条件 / 空区间 / 空枚举集），不匹配任何输入

export interface RuleAnalysis {
  index: number;
  ruleId: string;
  action: string;
  status: RuleStatus;
  /** 命中本规则的最小输入（最小反例）；不可达或空规则为 null。 */

  witness: Input | null;
  /** 与本规则空间重叠的前序规则 id（遮蔽来源）。 */
  shadowedBy: string[];
  /** 本规则空间中真正可达的部分（互不相交的盒）。 */
  reachableBoxes: Box[];
  /** 本规则空间中被前序规则遮蔽的部分（互不相交的盒）。 */
  shadowedBoxes: Box[];
}

export interface AnalysisReport {
  rules: RuleAnalysis[];
  /** 未被任何规则覆盖的输入空间（互不相交的盒）。 */
  uncoveredBoxes: Box[];
  /** 最小未覆盖输入；完全覆盖时为 null。 */
  uncoveredWitness: Input | null;
  fullyCovered: boolean;
}

export class RuleCoverageAnalyzer {
  readonly idx: SchemaIndex;
  private rules: Rule[] = [];

  constructor(schema: Schema, rules: Rule[] = []) {
    this.idx = indexSchema(schema);
    const seen = new Set<string>();
    for (const rule of rules) {
      if (seen.has(rule.id)) throw new Error(`duplicate rule id: ${rule.id}`);
      seen.add(rule.id);
      this.validateRule(rule);
    }
    this.rules = [...rules];
  }

  private validateRule(rule: Rule): void {
    // 触发条件合法性检查（未知属性 / 类型不符 / 枚举值越界都会抛错）。
    ruleBox(this.idx, rule);
  }

  getRules(): Rule[] {
    return [...this.rules];
  }

  /** 新增规则；默认追加到末尾（最低优先级），可指定插入位置。 */
  addRule(rule: Rule, index?: number): void {
    if (this.rules.some((r) => r.id === rule.id)) {
      throw new Error(`duplicate rule id: ${rule.id}`);
    }
    this.validateRule(rule);
    const at = index === undefined ? this.rules.length : index;
    if (at < 0 || at > this.rules.length) throw new Error(`insert index out of range: ${at}`);
    this.rules.splice(at, 0, rule);
  }

  /** 按 id 删除规则；返回是否删除成功。 */
  removeRule(id: string): boolean {
    const at = this.rules.findIndex((r) => r.id === id);
    if (at === -1) return false;
    this.rules.splice(at, 1);
    return true;
  }

  /** 全量精确重分析。增删规则后直接再次调用即可获得更新后的结果。 */
  analyze(): AnalysisReport {
    const idx = this.idx;
    const boxes = this.rules.map((r) => ruleBox(idx, r));
    const earlier: Box[] = []; // 前序非空盒（可互相重叠，subtractMany 顺序差集仍精确）
    const earlierIds: string[] = [];

    const analyses: RuleAnalysis[] = [];
    for (let i = 0; i < this.rules.length; i++) {
      const rule = this.rules[i];
      const box = boxes[i];

      if (isEmptyBox(box)) {
        analyses.push({
          index: i,
          ruleId: rule.id,
          action: rule.action,
          status: 'empty',
          witness: null,
          shadowedBy: [],
          reachableBoxes: [],
          shadowedBoxes: [],
        });
        continue;
      }

      const reachableBoxes = subtractMany(idx, box, earlier);
      const shadowedBoxes = subtractMany(idx, box, reachableBoxes);
      const shadowedBy: string[] = [];
      for (let j = 0; j < earlier.length; j++) {
        if (boxesOverlap(earlier[j], box)) shadowedBy.push(earlierIds[j]);
      }

      let status: RuleStatus;
      let witness: Input | null = null;
      if (reachableBoxes.length === 0) {
        status = 'unreachable';
      } else {
        status = shadowedBoxes.length === 0 ? 'reachable' : 'partially-shadowed';
        witness = minPointOfBoxes(idx, reachableBoxes);
      }

      analyses.push({
        index: i,
        ruleId: rule.id,
        action: rule.action,
        status,
        witness,
        shadowedBy,
        reachableBoxes,
        shadowedBoxes,
      });

      earlier.push(box);
      earlierIds.push(rule.id);
    }

    const nonEmpty = boxes.filter((b) => !isEmptyBox(b));
    const uncoveredBoxes = subtractMany(idx, fullBox(idx), nonEmpty);
    const uncoveredWitness = minPointOfBoxes(idx, uncoveredBoxes);

    return {
      rules: analyses,
      uncoveredBoxes,
      uncoveredWitness,
      fullyCovered: uncoveredBoxes.length === 0,
    };
  }
}

/** 便捷一次性分析。 */
export function analyzeRules(schema: Schema, rules: Rule[]): AnalysisReport {
  return new RuleCoverageAnalyzer(schema, rules).analyze();
}
