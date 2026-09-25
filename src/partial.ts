import { and, lit, not, or, toString, type Expr } from './expr.ts';
import { evalExpr } from './expr.ts';
import { F, T, U } from './tri.ts';

/**
 * Partial evaluation: fold every attribute present in `known` to a constant
 * (`true` / `false`, or `unknown` when the attribute is known to be missing
 * or non-integer) and simplify the boolean skeleton. Attributes absent from
 * `known` stay symbolic. `unknown` is preserved as `unknown`; it is never
 * rewritten to `false`.
 *
 * The result is the residual condition over the still-unknown attributes.
 */
export function partialEval(e: Expr, known: ReadonlyMap<string, unknown>): Expr {
  switch (e.kind) {
    case 'lit':
      return e;
    case 'cmp':
    case 'in':
    case 'range': {
      if (!known.has(e.attr)) return e;
      return lit(evalExpr(e, (a) => known.get(a)));
    }
    case 'not': {
      const c = partialEval(e.child, known);
      if (c.kind === 'lit') {
        return lit(c.value === T ? F : c.value === F ? T : U);
      }
      return not(c);
    }
    case 'and': {
      const kept: Expr[] = [];
      let sawUnknown = false;
      for (const child of e.children) {
        const c = partialEval(child, known);
        if (c.kind === 'lit') {
          if (c.value === F) return lit(F); // false dominates
          if (c.value === U) sawUnknown = true;
          continue; // drop true
        }
        kept.push(c);
      }
      if (kept.length === 0) return lit(sawUnknown ? U : T);
      // unknown literals still constrain the outcome; keep them explicit.
      const children = sawUnknown ? [lit(U), ...kept] : kept;
      return children.length === 1 ? children[0]! : and(...children);
    }
    case 'or': {
      const kept: Expr[] = [];
      let sawUnknown = false;
      for (const child of e.children) {
        const c = partialEval(child, known);
        if (c.kind === 'lit') {
          if (c.value === T) return lit(T); // true dominates
          if (c.value === U) sawUnknown = true;
          continue; // drop false
        }
        kept.push(c);
      }
      if (kept.length === 0) return lit(sawUnknown ? U : F);
      const children = sawUnknown ? [lit(U), ...kept] : kept;
      return children.length === 1 ? children[0]! : or(...children);
    }
  }
}

export function residualText(e: Expr, known: ReadonlyMap<string, unknown>): string {
  return toString(partialEval(e, known));
}
