import { attrsOf } from './policy.ts';
import type { Expr } from './policy.ts';
import type { AttrKey, AttributeSource } from './types.ts';

export interface Plan {
  readonly ordered: Expr;
  readonly mustRead: ReadonlySet<AttrKey>;
  readonly optional: ReadonlySet<AttrKey>;
  readonly heuristic: string;
  // The ordering heuristic is a greedy local rule. It does NOT claim to find a
  // globally minimal-cost fetch order (that would require selectivity
  // estimates and cross-condition batch optimisation).
  readonly claimsOptimal: false;
}

function subtreeCost(e: Expr, byAttr: ReadonlyMap<AttrKey, AttributeSource>): number {
  let sum = 0;
  for (const a of attrsOf(e)) {
    const s = byAttr.get(a);
    if (s) {
      sum += s.batchCost + s.attrCost;
    }
  }
  return sum;
}

function orderExpr(e: Expr, byAttr: ReadonlyMap<AttrKey, AttributeSource>): Expr {
  switch (e.kind) {
    case 'and':
    case 'or': {
      const args = e.args.map((a) => orderExpr(a, byAttr));
      args.sort((x, y) => {
        const diff = subtreeCost(x, byAttr) - subtreeCost(y, byAttr);
        if (diff !== 0) {
          return diff;
        }
        const jx = JSON.stringify(x);
        const jy = JSON.stringify(y);
        return jx < jy ? -1 : jx > jy ? 1 : 0;
      });
      return { ...e, args };
    }
    case 'not':
      return { ...e, arg: orderExpr(e.arg, byAttr) };
    default:
      return e;
  }
}

function mustReadOf(e: Expr, out: Set<AttrKey>): void {
  switch (e.kind) {
    case 'cmp':
    case 'in':
      out.add(e.attr);
      return;
    case 'not':
      mustReadOf(e.arg, out);
      return;
    case 'and':
    case 'or':
      if (e.args.length > 0) {
        mustReadOf(e.args[0], out);
      }
      return;
  }
}

function buildPlan(ordered: Expr, heuristic: string): Plan {
  const must = new Set<AttrKey>();
  mustReadOf(ordered, must);
  const optional = new Set<AttrKey>();
  for (const a of attrsOf(ordered)) {
    if (!must.has(a)) {
      optional.add(a);
    }
  }
  return { ordered, mustRead: must, optional, heuristic, claimsOptimal: false };
}

export function planPolicy(e: Expr, byAttr: ReadonlyMap<AttrKey, AttributeSource>): Plan {
  return buildPlan(orderExpr(e, byAttr), 'cheapest-subtree-first');
}

export function planAsWritten(e: Expr, _byAttr: ReadonlyMap<AttrKey, AttributeSource>): Plan {
  return buildPlan(e, 'none (as-written)');
}
