// Free-variable analysis over binding identities.
// Returns captured binders in first-occurrence (preorder) order.
// Binders introduced inside the subtree never leak, and shadowed
// occurrences resolve to their own BindId, never to an outer same-name one.

import type { Binder, BindId, Expr } from './ast.ts';

export function freeVarsOf(root: Expr): Binder[] {
  const out = new Map<BindId, string>(); // preserves first-occurrence order

  function go(e: Expr, boundInside: Set<BindId>): void {
    switch (e.kind) {
      case 'num':
      case 'bool':
        return;
      case 'var':
        if (!boundInside.has(e.ref) && !out.has(e.ref)) out.set(e.ref, e.name);
        return;
      case 'lam': {
        boundInside = new Set(boundInside);
        for (const p of e.params) boundInside.add(p.bind);
        go(e.body, boundInside);
        return;
      }
      case 'let': {
        go(e.value, boundInside);
        boundInside = new Set(boundInside);
        boundInside.add(e.binder.bind);
        go(e.body, boundInside);
        return;
      }
      case 'app':
        go(e.fn, boundInside);
        for (const a of e.args) go(a, boundInside);
        return;
      case 'if':
        go(e.cond, boundInside);
        go(e.then, boundInside);
        go(e.els, boundInside);
        return;
      case 'bin':
        go(e.lhs, boundInside);
        go(e.rhs, boundInside);
        return;
      case 'return':
        go(e.value, boundInside);
        return;
    }
  }

  go(root, new Set());
  return [...out.entries()].map(([bind, name]) => ({ bind, name }));
}
