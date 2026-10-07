// alpha.ts — random alpha-renaming: renames binders only, keeps binding
// identities (bind ids), node ids, and free-variable names intact.

import { BindingId, Expr } from './core';

export function alphaRename(root: Expr, rand: () => number): Expr {
  const renamed = new Map<BindingId, string>();
  const fresh = () => `a${Math.floor(rand() * 0xffffffff).toString(36)}`;

  function go(e: Expr): Expr {
    switch (e.k) {
      case 'lit':
        return e;
      case 'var': {
        const n = renamed.get(e.ref);
        return n !== undefined && n !== e.name ? { ...e, name: n } : e;
      }
      case 'lam': {
        if (!renamed.has(e.bind)) renamed.set(e.bind, fresh());
        const body = go(e.body);
        return { ...e, param: renamed.get(e.bind)!, body };
      }
      case 'let': {
        const value = go(e.value); // binder not in scope in its own value
        renamed.set(e.bind, fresh());
        const body = go(e.body);
        return { ...e, name: renamed.get(e.bind)!, value, body };
      }
      case 'app':
        return { ...e, fn: go(e.fn), arg: go(e.arg) };
      case 'if':
        return { ...e, c: go(e.c), t: go(e.t), e: go(e.e) };
      case 'prim':
        return { ...e, args: e.args.map(go) };
      case 'return':
        return { ...e, value: go(e.value) };
    }
  }

  return go(root);
}
