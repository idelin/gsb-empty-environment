/**
 * 树文法（Tree Grammar）：一组产生式  标签 -> 子节点序列正规模式。
 * 整体构成一个自下而上可判定的树自动机：
 *   - 每个标签对应自动机的一个状态；
 *   - 每条产生式的 Pattern 编译为 NFA，作为该状态的水平转移函数。
 */
import { compilePattern } from './pattern.ts';
import type { Pattern, NFA } from './pattern.ts';

export interface Rule {
  label: string;
  pattern: Pattern;
  nfa: NFA;
}

export interface Grammar {
  name: string;
  root: string;
  rules: Map<string, Rule>;
}

export function defineGrammar(
  name: string,
  root: string,
  productions: Record<string, Pattern>,
): Grammar {
  const rules = new Map<string, Rule>();
  for (const [label, pattern] of Object.entries(productions)) {
    rules.set(label, { label, pattern, nfa: compilePattern(pattern) });
  }
  if (!rules.has(root)) {
    throw new Error(`根标签 '${root}' 没有对应的产生式`);
  }
  return { name, root, rules };
}
