/**
 * 二元子句蕴含图：子句 (a ∨ b) 产生两条边 ¬a→b 与 ¬b→a。
 * 从假设文字出发做可达性传播；若某特性 X 的正负文字同时被推出，
 * 则假设不可满足（2-SAT 判定是完备的），并给出两条推导链作为解释。
 */

import type { Clause } from './model.ts';
import { notLit, litName } from './model.ts';

export interface Edge {
  to: number;
  desc: string; // 来源子句描述
}

export type Graph = Map<number, Edge[]>;

export function buildImplicationGraph(clauses: Clause[]): Graph {
  const g: Graph = new Map();
  const add = (from: number, to: number, desc: string) => {
    let arr = g.get(from);
    if (!arr) { arr = []; g.set(from, arr); }
    arr.push({ to, desc });
  };
  for (const c of clauses) {
    add(notLit(c.a), c.b, c.desc);
    add(notLit(c.b), c.a, c.desc);
  }
  return g;
}

interface Parent {
  from: number; // -1 表示假设文字本身
  desc: string;
}

/** 从假设文字集合出发做 BFS，返回每个被推出文字的父指针。 */
function propagate(units: number[], g: Graph): Map<number, Parent> {
  const parent = new Map<number, Parent>();
  const queue: number[] = [];
  for (const u of units) {
    if (!parent.has(u)) {
      parent.set(u, { from: -1, desc: '' });
      queue.push(u);
    }
  }
  while (queue.length > 0) {
    const cur = queue.shift()!;
    for (const e of g.get(cur) ?? []) {
      if (!parent.has(e.to)) {
        parent.set(e.to, { from: cur, desc: e.desc });
        queue.push(e.to);
      }
    }
  }
  return parent;
}

function chainTo(target: number, parent: Map<number, Parent>): { lit: number; desc: string }[] {
  const steps: { lit: number; desc: string }[] = [];
  let cur = target;
  while (true) {
    const p = parent.get(cur)!;
    if (p.from === -1) break;
    steps.push({ lit: cur, desc: p.desc });
    cur = p.from;
  }
  steps.reverse();
  return steps;
}

function renderChain(unit: number, target: number, parent: Map<number, Parent>, features: string[]): string {
  const steps = chainTo(target, parent);
  let s = litName(unit, features);
  for (const st of steps) {
    s += ` ⇒ ${litName(st.lit, features)}（${st.desc}）`;
  }
  return s;
}

/**
 * 解释为何假设文字 units（某对特性的取值组合）不可达。
 * 返回解释文本行；若实际上可达（不应发生）返回 null。
 */
export function explainUnreachable(units: number[], clauses: Clause[], features: string[]): string[] | null {
  const g = buildImplicationGraph(clauses);
  const parent = propagate(units, g);
  // 找同时被推出正负文字的特性
  for (const lit of parent.keys()) {
    const neg = notLit(lit);
    if (parent.has(neg)) {
      const posLit = (lit & 1) === 0 ? lit : neg;
      const negLit = notLit(posLit);
      const lines: string[] = [];
      lines.push(`假设 ${units.map(u => litName(u, features)).join(' 且 ')}：`);
      lines.push(`  推导链 1: ${renderChain(chainSource(posLit, parent, units), posLit, parent, features)}`);
      const src2 = chainSource(negLit, parent, units);
      lines.push(`  推导链 2: ${renderChain(src2, negLit, parent, features)}`);
      lines.push(`  矛盾：${litName(posLit, features)} 与 ${litName(negLit, features)} 同时成立，故该组合不可达。`);
      return lines;
    }
  }
  return null;
}

/** 找到推出 target 的链所源自的假设文字。 */
function chainSource(target: number, parent: Map<number, Parent>, units: number[]): number {
  let cur = target;
  while (true) {
    const p = parent.get(cur)!;
    if (p.from === -1) return cur;
    cur = p.from;
  }
}
