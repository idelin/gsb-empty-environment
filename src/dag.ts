import type { Expr } from './expr.ts';

/**
 * A compiled decision DAG. Structurally identical sub-expressions are
 * hash-consed into a single shared node. Child order is preserved exactly
 * as written so short-circuit evaluation order (and therefore which
 * attributes get read) is unchanged by compilation.
 */
export interface DagNode {
  readonly id: number;
  readonly expr: Expr;
  readonly children: readonly DagNode[];
  /** Sorted attribute names this subtree may read. */
  readonly deps: readonly string[];
  readonly key: string;
}

export interface Dag {
  readonly root: DagNode;
  readonly nodes: readonly DagNode[];
}

export function compile(expr: Expr): Dag {
  const intern = new Map<string, DagNode>();
  const nodes: DagNode[] = [];

  const build = (e: Expr): DagNode => {
    let key: string;
    let childNodes: DagNode[] = [];
    let deps: Set<string>;
    switch (e.kind) {
      case 'lit':
        key = `lit:${e.value}`;
        deps = new Set();
        break;
      case 'cmp':
        key = `cmp:${e.attr}:${e.op}:${e.value}`;
        deps = new Set([e.attr]);
        break;
      case 'in':
        key = `in:${e.attr}:${[...e.set].join(',')}`;
        deps = new Set([e.attr]);
        break;
      case 'range':
        key = `range:${e.attr}:${e.min ?? ''}:${e.max ?? ''}`;
        deps = new Set([e.attr]);
        break;
      case 'not': {
        const c = build(e.child);
        childNodes = [c];
        key = `not(${c.key})`;
        deps = new Set(c.deps);
        break;
      }
      case 'and':
      case 'or': {
        childNodes = e.children.map(build);
        key = `${e.kind}(${childNodes.map((c) => c.key).join('|')})`;
        deps = new Set(childNodes.flatMap((c) => c.deps));
        break;
      }
    }
    const hit = intern.get(key);
    if (hit) return hit;
    const node: DagNode = {
      id: nodes.length,
      expr: e,
      children: childNodes,
      deps: [...deps].sort(),
      key,
    };
    intern.set(key, node);
    nodes.push(node);
    return node;
  };

  const root = build(expr);
  return { root, nodes };
}
