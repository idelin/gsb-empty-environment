// analyze.ts — free-variable analysis keyed on binding identity, first-occurrence order.

import { BindingId, Expr } from './core';

export interface FreeVar {
  name: string;
  ref: BindingId;
}

// Free variables of `root`, deduplicated by binding id, in pre-order
// first-occurrence order. Binders inside the subtree (lam params, let
// bindings) shadow outer bindings, so shadowed/locals never appear here.
export function freeVars(root: Expr): FreeVar[] {
  const bound = new Set<BindingId>();
  const seen = new Set<BindingId>();
  const out: FreeVar[] = [];

  function walk(e: Expr): void {
    switch (e.k) {
      case 'var':
        if (!bound.has(e.ref) && !seen.has(e.ref)) {
          seen.add(e.ref);
          out.push({ name: e.name, ref: e.ref });
        }
        break;
      case 'lam':
        bound.add(e.bind);
        walk(e.body);
        bound.delete(e.bind);
        break;
      case 'let':
        walk(e.value);
        bound.add(e.bind);
        walk(e.body);
        bound.delete(e.bind);
        break;
      case 'lit':
        break;
      case 'app':
        walk(e.fn);
        walk(e.arg);
        break;
      case 'if':
        walk(e.c);
        walk(e.t);
        walk(e.e);
        break;
      case 'prim':
        for (const a of e.args) walk(a);
        break;
      case 'return':
        walk(e.value);
        break;
    }
  }

  walk(root);
  return out;
}
