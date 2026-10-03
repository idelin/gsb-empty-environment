/**
 * 结构模式 = 正规树自动机（regular tree automaton）的声明式描述：
 * - 每条规则是一个状态（以节点标签命名）；
 * - 规则的 children 正规式即"水平自动机"，约束子节点标签序列；
 * - text 约束叶子文本内容。
 */
import type { Regex } from './regex.ts';

export { choice, empty, label, opt, plus, seq, star } from './regex.ts';

export type TextPolicy = 'required' | 'optional' | 'forbidden';

export interface NodeRule {
  label: string;
  children: Regex;
  text: TextPolicy;
}

export interface RuleOptions {
  text?: TextPolicy;
}

export function rule(label: string, children: Regex, options: RuleOptions = {}): NodeRule {
  return { label, children, text: options.text ?? 'forbidden' };
}

export class Schema {
  readonly root: string;
  private readonly rules = new Map<string, NodeRule>();

  constructor(root: string, rules: NodeRule[]) {
    this.root = root;
    for (const nodeRule of rules) {
      if (this.rules.has(nodeRule.label)) {
        throw new Error(`重复定义的规则：${nodeRule.label}`);
      }
      this.rules.set(nodeRule.label, nodeRule);
    }
    if (!this.rules.has(root)) {
      throw new Error(`缺少根规则：${root}`);
    }
  }

  lookup(label: string): NodeRule | undefined {
    return this.rules.get(label);
  }
}
