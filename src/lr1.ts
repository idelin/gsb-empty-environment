/**
 * LR(1) 项、项集闭包（closure）、goto 转移与规范 LR(1) 项集族（自动机）构建。
 */

import {
  END,
  type Grammar,
  augmentGrammar,
  computeFirst,
  firstOfSequence,
} from "./grammar.ts";

/** LR(1) 项：产生式 id + 圆点位置 + 向前看符号 */
export interface Item {
  prod: number;
  dot: number;
  lookahead: string;
}

export function itemKey(item: Item): string {
  return `${item.prod}:${item.dot}:${item.lookahead}`;
}

/** 一个 LR(1) 状态（项集闭包） */
export interface State {
  id: number;
  /** 完整闭包项集（含内核项），按 (prod, dot, lookahead) 排序 */
  items: Item[];
  /** 内核项（初始项或圆点不在最左端的项） */
  kernel: Item[];
  /** 符号 -> 目标状态 id */
  transitions: Map<string, number>;
}

export interface Automaton {
  /** 增广文法 */
  grammar: Grammar;
  /** 原始（未增广）文法 */
  sourceGrammar: Grammar;
  first: Map<string, Set<string>>;
  states: State[];
}

function sortItems(items: Item[]): Item[] {
  return [...items].sort(
    (a, b) => a.prod - b.prod || a.dot - b.dot || (a.lookahead < b.lookahead ? -1 : 1),
  );
}

/**
 * LR(1) 闭包：对项 [A -> α·Bβ, a]，若 B -> γ 是产生式，
 * 则对每个 b ∈ First(βa) 加入项 [B -> ·γ, b]。
 */
export function closure(
  items: Item[],
  g: Grammar,
  first: Map<string, Set<string>>,
): Item[] {
  const result = new Map<string, Item>();
  const queue: Item[] = [];
  for (const it of items) {
    const k = itemKey(it);
    if (!result.has(k)) {
      result.set(k, it);
      queue.push(it);
    }
  }
  while (queue.length > 0) {
    const it = queue.pop()!;
    const prod = g.productions[it.prod];
    if (it.dot >= prod.rhs.length) continue;
    const B = prod.rhs[it.dot];
    if (!g.nonterminals.has(B)) continue;
    // First(βa)：β 为圆点后剩余部分，a 为当前项的向前看符号
    const beta = prod.rhs.slice(it.dot + 1);
    const lookaheads = firstOfSequence([...beta, it.lookahead], first);
    for (const p of g.byLhs.get(B)!) {
      for (const b of lookaheads) {
        const next: Item = { prod: p.id, dot: 0, lookahead: b };
        const k = itemKey(next);
        if (!result.has(k)) {
          result.set(k, next);
          queue.push(next);
        }
      }
    }
  }
  return [...result.values()];
}

/** goto(I, X)：将 I 中所有 "圆点后为 X" 的项移动圆点，再取闭包。 */
export function gotoItems(
  items: Item[],
  symbol: string,
  g: Grammar,
  first: Map<string, Set<string>>,
): Item[] {
  const moved: Item[] = [];
  for (const it of items) {
    const prod = g.productions[it.prod];
    if (it.dot < prod.rhs.length && prod.rhs[it.dot] === symbol) {
      moved.push({ prod: it.prod, dot: it.dot + 1, lookahead: it.lookahead });
    }
  }
  return moved.length > 0 ? closure(moved, g, first) : [];
}

/** 构建规范 LR(1) 项集族（自动机）。 */
export function buildAutomaton(sourceGrammar: Grammar): Automaton {
  const g = augmentGrammar(sourceGrammar);
  const first = computeFirst(g);

  const startKernel: Item[] = [{ prod: 0, dot: 0, lookahead: END }];
  const startItems = sortItems(closure(startKernel, g, first));

  const states: State[] = [];
  const kernelKeyOf = (items: Item[]) =>
    items
      .filter((it) => it.dot > 0 || g.productions[it.prod].lhs === g.start)
      .map(itemKey)
      .join("|");

  const seen = new Map<string, number>();
  const push = (items: Item[]): number => {
    const key = kernelKeyOf(items);
    const existing = seen.get(key);
    if (existing !== undefined) return existing;
    const id = states.length;
    seen.set(key, id);
    states.push({
      id,
      items,
      kernel: items.filter((it) => it.dot > 0 || g.productions[it.prod].lhs === g.start),
      transitions: new Map(),
    });
    return id;
  };

  push(startItems);
  for (let i = 0; i < states.length; i++) {
    const state = states[i];
    // 收集圆点后的所有符号
    const symbols = new Set<string>();
    for (const it of state.items) {
      const prod = g.productions[it.prod];
      if (it.dot < prod.rhs.length) symbols.add(prod.rhs[it.dot]);
    }
    for (const sym of [...symbols].sort()) {
      const target = gotoItems(state.items, sym, g, first);
      if (target.length > 0) {
        state.transitions.set(sym, push(sortItems(target)));
      }
    }
  }

  return { grammar: g, sourceGrammar, first, states };
}
