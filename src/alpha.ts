// Random alpha-renaming: gives every binder a fresh random name and rewrites
// variable occurrences to match. BindIds are untouched, so a correct
// refactorer must observe an identical capture set before and after renaming.
// The name pool is deliberately tiny to force collisions between distinct
// binders — analysis keyed on names would break; keyed on BindId it cannot.

import type { BindId, Expr, Program } from './ast.ts';

export function alphaRenameExpr(
  e: Expr,
  rng: () => number,
  pool: string[] = ['a', 'b', 'c'],
): Expr {
  const renamed = new Map<BindId, string>();
  const nameOf = (bind: BindId, fallback: string): string => {
    let n = renamed.get(bind);
    if (n === undefined) {
      n = pool[Math.floor(rng() * pool.length)];
      renamed.set(bind, n);
    }
    return n ?? fallback;
  };

  function go(e: Expr): Expr {
    switch (e.kind) {
      case 'num':
      case 'bool':
        return e;
      case 'var':
        return { ...e, name: renamed.get(e.ref) ?? e.name };
      case 'lam':
        return {
          ...e,
          params: e.params.map((p) => ({ bind: p.bind, name: nameOf(p.bind, p.name) })),
          body: go(e.body),
        };
      case 'app':
        return { ...e, fn: go(e.fn), args: e.args.map(go) };
      case 'let':
        return {
          ...e,
          binder: { bind: e.binder.bind, name: nameOf(e.binder.bind, e.binder.name) },
          value: go(e.value),
          body: go(e.body),
        };
      case 'if':
        return { ...e, cond: go(e.cond), then: go(e.then), els: go(e.els) };
      case 'bin':
        return { ...e, lhs: go(e.lhs), rhs: go(e.rhs) };
      case 'return':
        return { ...e, value: go(e.value) };
    }
  }

  return go(e);
}

export function alphaRenameProgram(p: Program, rng: () => number): Program {
  return {
    functions: p.functions.map((f) => ({ ...f, body: alphaRenameExpr(f.body, rng) })),
    root: alphaRenameExpr(p.root, rng),
  };
}
