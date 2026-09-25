import type { Expr } from './expr.ts';
import { triAnd, triNot, triOf, triOr, type Tri } from './logic.ts';

export type Pred =
  | { type: 'in'; values: readonly number[] }
  | { type: 'range'; min: number | null; max: number | null };

export type Node = { id: number; key: string } & (
  | { kind: 'lit'; value: Tri }
  | { kind: 'cond'; attr: string; pred: Pred }
  | { kind: 'and' | 'or'; args: readonly Node[] }
  | { kind: 'not'; arg: Node }
);

function predKey(pred: Pred): string {
  return pred.type === 'in'
    ? `in:${pred.values.join(',')}`
    : `range:${pred.min ?? ''}:${pred.max ?? ''}`;
}

/** 叶子条件求值；属性缺失（undefined）=> unknown，绝不当作 false。 */
export function evalPred(pred: Pred, value: number | undefined): Tri {
  if (value === undefined) return 'unknown';
  if (pred.type === 'in') return triOf(pred.values.includes(value));
  const ok =
    (pred.min === null || value >= pred.min) &&
    (pred.max === null || value <= pred.max);
  return triOf(ok);
}

/**
 * 哈希consed 共享决策 DAG 编译器。
 * 只做 Kleene 安全的折叠：常量湮灭/单位元、双重否定。
 * 刻意不做补律（x AND NOT x => false）与排中律（x OR NOT x => true），
 * 因为 x 为 unknown 时两者都必须保持 unknown。
 * and/or 子节点保持书写顺序，保证求值短路顺序与读取依赖不被重排。
 */
export class Compiler {
  private intern = new Map<string, Node>();
  private nextId = 0;

  private internNode(key: string, make: (id: number, key: string) => Node): Node {
    const hit = this.intern.get(key);
    if (hit) return hit;
    const node = make(this.nextId++, key);
    this.intern.set(key, node);
    return node;
  }

  lit(value: Tri): Node {
    return this.internNode(`lit:${value}`, (id, key) => ({ id, key, kind: 'lit', value }));
  }

  cond(attr: string, pred: Pred): Node {
    const key = `cond:${attr}:${predKey(pred)}`;
    return this.internNode(key, (id, k) => ({ id, key: k, kind: 'cond', attr, pred }));
  }

  neg(arg: Node): Node {
    if (arg.kind === 'lit') return this.lit(triNot(arg.value));
    if (arg.kind === 'not') return arg.arg;
    return this.internNode(`not:${arg.id}`, (id, key) => ({ id, key, kind: 'not', arg }));
  }

  join(kind: 'and' | 'or', args: readonly Node[]): Node {
    const annihilator: Tri = kind === 'and' ? 'false' : 'true';
    const identity: Tri = kind === 'and' ? 'true' : 'false';
    const flat: Node[] = [];
    const seen = new Set<number>();
    for (const arg of args) {
      const items = arg.kind === kind ? arg.args : [arg];
      for (const item of items) {
        if (!seen.has(item.id)) {
          seen.add(item.id);
          flat.push(item);
        }
      }
    }
    if (flat.some((n) => n.kind === 'lit' && n.value === annihilator)) {
      return this.lit(annihilator);
    }
    const kept = flat.filter((n) => !(n.kind === 'lit' && n.value === identity));
    if (kept.length === 0) return this.lit(identity);
    if (kept.length === 1) return kept[0];
    const key = `${kind}:${kept.map((n) => n.id).join(',')}`;
    return this.internNode(key, (id, k) => ({ id, key: k, kind, args: kept }));
  }

  compile(expr: Expr): Node {
    switch (expr.kind) {
      case 'lit':
        return this.lit(triOf(expr.value));
      case 'in':
        return this.cond(expr.attr, { type: 'in', values: expr.values });
      case 'range':
        return this.cond(expr.attr, { type: 'range', min: expr.min, max: expr.max });
      case 'not':
        return this.neg(this.compile(expr.arg));
      case 'and':
      case 'or':
        return this.join(expr.kind, expr.args.map((a) => this.compile(a)));
    }
  }

  get size(): number {
    return this.intern.size;
  }
}

/** 纯同步求值（无缓存、无 IO），用于解释最小化与残差求值。 */
export function evalNode(node: Node, resolve: (attr: string) => number | undefined): Tri {
  switch (node.kind) {
    case 'lit':
      return node.value;
    case 'cond':
      return evalPred(node.pred, resolve(node.attr));
    case 'not':
      return triNot(evalNode(node.arg, resolve));
    case 'and': {
      let acc: Tri = 'true';
      for (const child of node.args) {
        acc = triAnd(acc, evalNode(child, resolve));
        if (acc === 'false') break;
      }
      return acc;
    }
    case 'or': {
      let acc: Tri = 'false';
      for (const child of node.args) {
        acc = triOr(acc, evalNode(child, resolve));
        if (acc === 'true') break;
      }
      return acc;
    }
  }
}
